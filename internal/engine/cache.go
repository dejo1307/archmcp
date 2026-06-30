package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"sort"
	"strings"

	"github.com/enola-labs/enola/internal/extractors"
	"github.com/enola-labs/enola/internal/facts"
	"github.com/enola-labs/enola/pkg/plugin"
)

// cacheVersion is mixed into every cache key. Bump it whenever the fact schema or
// an extractor's output format changes in a way that invalidates stored facts.
// v2: Swift URLSession extractor precision (file-URL exclusion, interpolation fix).
// v3: Python route facts use method/role/bare-path Name (was http_method, verb-in-name).
// v4: Java HTTP client detection (RestTemplate call sites + @FeignClient interfaces).
// v5: PHP HTTP client detection + Laravel/Symfony route DSLs (attributes, YAML/XML config).
// v6: Ruby bare-constant references emitted as RelCalls edges (dead-code precision).
// v7: Ruby skips builtin-constant edges (god-class noise) + serializer attribute/include_ folding.
// v8: C/C++ file-scope registration-macro args (module_init/EXPORT_SYMBOL/DEVICE_ATTR) emitted as module-fact call edges.
// v9: C/C++ function-pointer field assignments (obj->cb = fn) + macro-body call references (IDENT( inside #define) emitted as call edges.
// v10: C/C++ qualifier-prefixed registration macros (static DEFINE_*_PM_OPS(name, suspend, resume)) record their function-name args as call edges.
// v11: C/C++ in-body compound-literal designated initializers (cfg = (struct X){ .cb = fn }) record their function-pointer fields as call edges.
// v12: C/C++ macro-body scan also captures value-position function pointers (.field = fn / = &fn inside #define), e.g. ops tables defined via a macro.
// v13: C/C++ in-extractor macro expansion of file-scope invocations recovers token-pasted callbacks (CONFIGFS_ATTR/DEVICE_ATTR_RO -> name##_show).
const cacheVersion = "v13"

// extractorCache holds per-extractor facts keyed by a content hash of the files
// the extractor depends on. It is loaded from disk at the start of a snapshot and
// written back at the end, carrying forward only the keys used this run so stale
// entries are garbage-collected.
//
// Reuse is correct because an extractor is a deterministic function of its inputs
// (verified: parallel and serial runs produce byte-identical facts), and a key
// captures every input that can change its output — see computeExtractorKeys.
type extractorCache struct {
	prev map[string]json.RawMessage // loaded from disk
	next map[string]json.RawMessage // to persist (this run's keys only)
	hits int
}

// loadExtractorCache reads the cache file at path. A missing or unreadable file
// yields an empty (but usable) cache, so caching degrades to a full run.
func loadExtractorCache(path string) *extractorCache {
	c := &extractorCache{
		prev: map[string]json.RawMessage{},
		next: map[string]json.RawMessage{},
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return c
	}
	var on struct {
		Version string                     `json:"version"`
		Entries map[string]json.RawMessage `json:"entries"`
	}
	if err := json.Unmarshal(data, &on); err != nil || on.Version != cacheVersion {
		return c // treat schema mismatch as a cold cache
	}
	if on.Entries != nil {
		c.prev = on.Entries
	}
	return c
}

// get returns the cached facts for key (deep-copied from JSON, so the caller may
// mutate them freely) and carries the original bytes forward to the next save.
func (c *extractorCache) get(key string) ([]facts.Fact, bool) {
	raw, ok := c.prev[key]
	if !ok {
		return nil, false
	}
	var ff []facts.Fact
	if err := json.Unmarshal(raw, &ff); err != nil {
		return nil, false
	}
	c.next[key] = raw // keep the clean, pre-mutation bytes
	c.hits++
	return ff, true
}

// put stores ff for key. It marshals immediately (before the engine tags or
// otherwise mutates the facts) so the persisted bytes stay clean.
func (c *extractorCache) put(key string, ff []facts.Fact) {
	raw, err := json.Marshal(ff)
	if err != nil {
		return
	}
	c.next[key] = raw
}

// save writes the keys used this run to path.
func (c *extractorCache) save(path string) error {
	on := struct {
		Version string                     `json:"version"`
		Entries map[string]json.RawMessage `json:"entries"`
	}{Version: cacheVersion, Entries: c.next}
	data, err := json.Marshal(on)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// computeExtractorKeys returns a cache key for every extractor that implements
// plugin.FileOwner. A key covers exactly the inputs that can change that
// extractor's output:
//
//   - the content hashes of the files it owns, and
//   - a shared hash of every file no FileOwner owns (configs, manifests, and
//     sources of non-cacheable extractors) — these are where detection markers
//     (tsconfig.json, build.gradle, Gemfile, …) live.
//
// Because cross-language source files never affect another language's output, a
// file owned by a *different* FileOwner is correctly excluded from this
// extractor's key. The shared-config hash over-invalidates (any manifest change
// busts every cache) but never under-invalidates, so reuse is always sound.
func computeExtractorKeys(all []extractors.Extractor, files []string, hashes map[string]string) map[string]string {
	owners := map[string]plugin.FileOwner{}
	for _, ext := range all {
		if fo, ok := ext.(plugin.FileOwner); ok {
			owners[ext.Name()] = fo
		}
	}
	if len(owners) == 0 {
		return nil
	}

	// Partition files: per-owner owned lists + the shared (un-owned) remainder.
	owned := map[string][]string{}
	var shared []string
	for _, f := range files {
		ownedByAny := false
		for name, fo := range owners {
			if fo.OwnsFile(f) {
				owned[name] = append(owned[name], f)
				ownedByAny = true
			}
		}
		if !ownedByAny {
			shared = append(shared, f)
		}
	}
	sharedHash := hashFileSet(shared, hashes)

	keys := make(map[string]string, len(owners))
	for name := range owners {
		h := sha256.New()
		h.Write([]byte(cacheVersion + "\x00" + name + "\x00" + sharedHash + "\x00"))
		h.Write([]byte(hashFileSet(owned[name], hashes)))
		keys[name] = hex.EncodeToString(h.Sum(nil))
	}
	return keys
}

// hashFileSet returns a stable hash over (path, contentHash) pairs, sorted by
// path so the result is independent of the input order.
func hashFileSet(files []string, hashes map[string]string) string {
	sorted := append([]string(nil), files...)
	sort.Strings(sorted)
	h := sha256.New()
	for _, f := range sorted {
		h.Write([]byte(f))
		h.Write([]byte{0})
		h.Write([]byte(hashes[f])) // empty for unreadable files — still deterministic
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// extractorCachePath returns the on-disk location of the cache for a repo.
func extractorCachePath(outDir string) string {
	return strings.TrimRight(outDir, "/") + "/extractor_cache.json"
}
