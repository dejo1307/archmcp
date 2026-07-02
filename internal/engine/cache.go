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
// v14: C/C++ static single-arg DEVICE_ATTR/BUS_ATTR expansion, all-ident scan of expanded macros (DEFINE_SHOW_ATTRIBUTE), capitalized C callee resolution.
// v15: C/C++ salvage function-pointer refs from file-scope ERROR regions (macro-opened structs like MACHINE_START/DT_MACHINE_START ... MACHINE_END).
// v16: extend that salvage to file-scope assignment_expression/field_expression fragments (machine_desc blocks parse that way when surrounded by other code).
// v17: full-tree salvage of `.field = fn` macro-struct debris (machine_desc) regardless of where tree-sitter scatters it (skips function bodies).
// v18: Ruby records custom class-body macro names + resolves self/self.class receivers as call edges (dead-code precision). KindTestRef facts index outbound refs from spec/test files.
// v19: Ruby captures call edges outside method bodies — class/module-body qualified & argument-position calls attach to the class fact; top-level/file-scope calls (fixtures, after_initialize blocks) attach to a new KindFileRef fact folded by the orphan collector.
// v20: Ruby call capture outside method bodies generalized to a per-scope pass (whole class/module body + top-level program), so assignment RHS and all non-`call` statements — e.g. `x = GlobalSetting.foo`, `CONST = { Proc.new { Group.bar } }` — are covered, not just bare call statements.
// v21: Ruby records the static prefix of interpolated symbols (`:"report_#{type}"` -> "report_") as a KindFileRef prop, so the dead-code detector treats dynamically dispatched (public_send/send) same-prefix methods as used.
// v22: Ruby records `super` as a call to the same-named ancestor method, and literal-symbol dispatch args (`obj.try(:foo)`, `send(:bar)`, `respond_to?(:baz)`) as calls to the named method.
// v23: Ruby captures no-arg calls on a chained receiver (ActiveRecord scope/class-method chains `Model.scope.final`, `assoc.class_method`, `x.class.method`; cheap attribute reads skipped) and indexes .rake/Rakefile files.
// v24: Ruby walks method default-parameter values for calls (`def f(x = self.class.foo)`) and records single-level predicate/bang calls (`viewer.rich?`, `x.save!`) which — unlike plain attribute reads — are unambiguously method invocations.
// v25: Ruby folds `delegate :a, :b, ..., to: X` method names as calls, and records calls on a bare-method (non-local identifier) receiver when the method name is scope-like (`some_relation.pluck_job_id`).
// v26: Ruby records scope-like (underscored) method calls on ANY identifier receiver, including local relation variables (`items = ...; items.preload_relations`).
// v27: Ruby resolves underscored calls on @ivar/@@cvar/$gvar receivers (`@klass.bo_search_fields`) and indexes view templates (ERB/Slim/HAML) for embedded Ruby calls (helpers/class methods), emitting KindFileRef references.
// v28: Ruby records method calls on a `klass`/`clazz`/`klazz` (or @klass) receiver as class-method dispatch (`klass.inline`), regardless of the method name.
// v29: Ruby extends the interpolated-prefix dispatch heuristic to strings (`"present_#{idx}"`), not just symbols, so send()-by-computed-string-name marks same-prefix methods used.
// v30: interpolated-string prefixes are now gated on dispatcher-proximity (committed only when the enclosing scope also calls send/public_send/…), so cache/Redis-key strings (`"fetch_#{id}"`) no longer hide genuine orphans; interpolated symbols remain unconditional.
// v31: Ruby block parameters (`each do |user| … end`) are now treated as locals, so a bare block var whose name matches an association no longer records a spurious in-loop call (N+1 false positive); and find_in_batches/in_batches are no longer counted as element loops (their block yields a batch, so the inner .each/.map is the real per-element loop) — fixing O(n²) mislabels of single-pass batch scans.
// v32: Ruby no longer flags `super` (climbs the inheritance chain, terminates) or a same-named call on an explicit non-self receiver (SimpleDelegator/decorator, `@delegate.render`/`new.call`) as self-recursion — both set recursive_self spuriously, the dominant recursion false positive.
// v33: Ruby recursion is now gated to same-object self dispatch — `self.class.foo` (instance method calling its sibling class method) and `obj.try(:foo)` (dispatch to a different object) no longer set recursive_self; receiverless calls, `self.foo`, and `Const.foo` matching the method's own full name still do.
// v34: Ruby constant-bounded iterators (`6.times`, `[…].each`, `%w[…]`, ALL-CAPS `CONST.each`) no longer add scaling loop_depth — they run a fixed number of times, so they no longer inflate a genuine O(n) into a false O(n²)/O(n³).
// v35: constant-bounded-loop detection now unwraps trailing size-preserving chain methods (`[a,b].compact.all?`, `%w[…].map.each`), so a bounded literal/constant behind `.compact`/`.uniq`/`.map`/… is still recognized as bounded.
// v36: Ruby module symbols now carry an `abstract` bool prop — true for mixins (modules that define instance methods) and ActiveSupport::Concerns, false for namespace/utility modules — so package-metrics abstractness (A) no longer counts Rails namespaces as abstractions. Bare-constant coupling resolution is also namespace-aware now.
const cacheVersion = "v36"

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
