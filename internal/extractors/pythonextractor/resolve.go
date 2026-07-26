package pythonextractor

import (
	"sort"
	"strings"

	"github.com/enola-labs/enola/internal/facts"
)

// resolveImports rewrites, in place, each dependency fact's `imports` relation
// Target so it matches the slash-directory Name of the internal module it refers
// to, and sets Props["source"] = "internal" | "external" | "stdlib".
//
// The Python extractor emits raw dotted import paths as relation Targets
// ("airflow.models.dag"), but module facts are named by slash directory
// ("airflow-core/src/airflow/models"). Downstream consumers (the graph index,
// package metrics, and the explain hotspots) all match the Target against module
// Names, so without this pass Python imports never resolve to internal modules
// and coupling collapses to zero. This mirrors the Go extractor, which resolves
// imports to slash paths at extraction time by stripping the go.mod module path.
func resolveImports(allFacts []facts.Fact, modules map[string]bool) {
	idx := buildSuffixIndex(modules)
	topPkgs := topLevelSegments(modules)

	for i := range allFacts {
		f := &allFacts[i]
		if f.Kind != facts.KindDependency {
			continue
		}
		importerDir := fileDir(f.File)
		for j := range f.Relations {
			rel := &f.Relations[j]
			if rel.Kind != facts.RelImports {
				continue
			}

			raw := rel.Target
			source := "external"
			switch {
			case strings.HasPrefix(raw, "."):
				// Relative imports are intra-project by definition.
				if dir, ok := resolveRelative(raw, importerDir); ok && dir != "" && dir != importerDir {
					rel.Target = dir
				}
				source = "internal"
			default:
				if dir := resolveAbsolute(raw, idx, topPkgs, importerDir); dir != "" {
					rel.Target = dir
					source = "internal"
				} else if pyStdlib[firstSeg(raw)] {
					source = "stdlib"
				}
			}

			if f.Props == nil {
				f.Props = map[string]any{}
			}
			f.Props["source"] = source
		}
	}
}

// resolveCallTargets rewrites, in place, the dotted call/instantiate targets that
// the walker emits for absolute imports (e.g. "airflow.models.dag.DAG") into the
// canonical slash symbol name ("airflow-core/src/airflow/models/dag.DAG"), and
// drops targets that resolve to stdlib/third-party code.
//
// Absolute intra-project imports are recorded by the walker as raw dotted paths so
// a call edge is emitted at all (relative imports already resolve at walk time).
// This pass — which needs the full file/module set, known only after every file is
// parsed — turns those dotted targets into exact symbol names where possible so the
// dead-code detector and the graph both see the reference. A target whose first
// segment names no internal directory (numpy, sqlalchemy) or a stdlib module is
// external: its edge is removed to avoid short-name collisions that would hide real
// dead code. Same-module and relative targets (already slash paths) are untouched.
func resolveCallTargets(allFacts []facts.Fact, fileModules map[string]bool) {
	fileIdx := buildSuffixIndex(fileModules)
	topPkgs := topLevelSegments(fileModules)
	reexports := buildReexportIndex(allFacts)

	for i := range allFacts {
		f := &allFacts[i]
		switch f.Kind {
		case facts.KindSymbol, facts.KindFileRef, facts.KindTestRef:
		default:
			continue
		}
		if len(f.Relations) == 0 {
			continue
		}
		importerDir := fileDir(f.File)
		out := f.Relations[:0]
		for _, rel := range f.Relations {
			if (rel.Kind == facts.RelCalls || rel.Kind == facts.RelInstantiates) && isDottedCallTarget(rel.Target) {
				resolved, keep := resolveDottedTarget(rel.Target, fileIdx, topPkgs, importerDir, reexports)
				if !keep {
					continue // external/stdlib → drop the edge
				}
				rel.Target = resolved
			}
			out = append(out, rel)
		}
		f.Relations = out
	}
}

// isDottedCallTarget reports whether a call target is an unresolved dotted path
// (e.g. "a.b.c") rather than an already-resolved slash symbol name
// ("dir/mod.sym") or a bare short name ("Foo").
func isDottedCallTarget(t string) bool {
	return strings.IndexByte(t, '.') >= 0 && strings.IndexByte(t, '/') < 0
}

// reexportIndex answers "package P re-exports name N — which module defines it?".
//
// It exists because a package is a DIRECTORY, and directories are not modules. For
// `from pkg.sub import thing` the call target is "pkg.sub.thing", whose module
// prefix "pkg.sub" matches no file: the module set holds "pkg/sub/__init__" and
// "pkg/sub/thing", never "pkg/sub". Absolute resolution therefore fails and the
// target stays a dotted string matching no node — a dangling edge. A real corpus
// had 674 such targets carrying 4,660 call edges, including every one of the 34
// router factories its API composition root wires up, leaving that file connected
// to nothing internal in the graph.
type reexportIndex struct {
	// byDir maps a package dir to each re-exported name's defining module.
	byDir map[string]map[string]string
	// dirs indexes those package dirs by dotted suffix, so a dotted module prefix
	// can be matched the same way resolveAbsolute matches module paths (import
	// paths are relative to a source root, so dots do not map 1:1 onto slashes).
	dirs suffixIndex
}

// buildReexportIndex reads the re-export data the walker already emits on every
// __init__.py from-import: Props["reexports"] (the imported short names) plus the
// relation Target naming the module they come from. resolveImports has run by now,
// so that Target is already a slash module path — only internal, resolved ones are
// indexed, since an unresolved or external source cannot name an internal symbol.
//
// A name re-exported from two DIFFERENT modules in the same package is dropped
// rather than resolved arbitrarily: binding a call to the wrong definition would
// fabricate an edge, and a missing edge is the safer error (the same rule the
// dotted/bare-name paths follow).
func buildReexportIndex(allFacts []facts.Fact) reexportIndex {
	byDir := map[string]map[string]string{}
	ambiguous := map[string]bool{}

	for i := range allFacts {
		f := &allFacts[i]
		if f.Kind != facts.KindDependency || !isInitFile(f.File) {
			continue
		}
		names := stringSliceProp(f.Props, "reexports")
		if len(names) == 0 {
			continue
		}
		var source string
		for _, rel := range f.Relations {
			if rel.Kind == facts.RelImports {
				source = rel.Target
				break
			}
		}
		// Only a resolved internal module can define an internal symbol. An
		// unresolved dotted path or a third-party package is not usable here.
		if source == "" || !strings.ContainsRune(source, '/') {
			continue
		}
		dir := fileDir(f.File)
		if byDir[dir] == nil {
			byDir[dir] = map[string]string{}
		}
		for _, n := range names {
			if n == "" {
				continue
			}
			key := dir + "\x00" + n
			if prev, ok := byDir[dir][n]; ok && prev != source {
				ambiguous[key] = true
				continue
			}
			if !ambiguous[key] {
				byDir[dir][n] = source
			}
		}
	}
	for key := range ambiguous {
		dir, n, _ := strings.Cut(key, "\x00")
		delete(byDir[dir], n)
	}

	dirs := make(map[string]bool, len(byDir))
	for d := range byDir {
		if len(byDir[d]) > 0 {
			dirs[d] = true
		}
	}
	return reexportIndex{byDir: byDir, dirs: buildSuffixIndex(dirs)}
}

// lookup resolves a dotted package prefix plus a symbol to the module that
// actually defines it, or "" when the package re-exports no such name.
func (r reexportIndex) lookup(modulePrefix, symbol string, topPkgs map[string]bool, importerDir string) string {
	if len(r.byDir) == 0 {
		return ""
	}
	dir := resolveAbsolute(modulePrefix, r.dirs, topPkgs, importerDir)
	if dir == "" {
		return ""
	}
	return r.byDir[dir][symbol]
}

// isInitFile reports whether a repo-relative path is a package __init__.py.
func isInitFile(f string) bool {
	return f == "__init__.py" || strings.HasSuffix(f, "/__init__.py")
}

// stringSliceProp reads a []string prop, tolerating the []any shape a fact takes
// on after a JSON round-trip.
func stringSliceProp(props map[string]any, key string) []string {
	switch v := props[key].(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, e := range v {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// resolveDottedTarget maps a dotted call target ("a.b.c.sym") to a canonical slash
// symbol name when its module prefix resolves to an internal file or to a package
// that re-exports the symbol, keeps it dotted when the prefix is internal but
// neither of those, and reports keep=false when the prefix is stdlib/third-party
// (drop the edge).
//
// The three attempts are ordered cheapest-and-most-certain first, so adding the
// re-export step can only rescue targets that previously dangled — a target the
// module lookup already resolved never reaches it.
func resolveDottedTarget(dotted string, fileIdx suffixIndex, topPkgs map[string]bool, importerDir string, reexports reexportIndex) (string, bool) {
	li := strings.LastIndexByte(dotted, '.')
	if li <= 0 {
		return dotted, true
	}
	modulePrefix := dotted[:li]
	symbol := dotted[li+1:]

	// 1. The prefix names a module file outright.
	if dir := resolveAbsolute(modulePrefix, fileIdx, topPkgs, importerDir); dir != "" {
		return dir + "." + symbol, true
	}

	// 2. The prefix names a PACKAGE whose __init__.py re-exports the symbol. Bind to
	// the module that actually defines it, so the edge lands on a real node instead
	// of dangling as a dotted string.
	if mod := reexports.lookup(modulePrefix, symbol, topPkgs, importerDir); mod != "" {
		return mod + "." + symbol, true
	}

	// 3. Internal, but nothing more precise is known: keep the dotted target so
	// downstream short-name matching still marks the symbol used. This carries no
	// graph edge — it only feeds the dead-code heuristic.
	seg0 := firstSeg(modulePrefix)
	if topPkgs[seg0] && !pyStdlib[seg0] {
		return dotted, true
	}
	return "", false
}

// suffixIndex maps a dotted-suffix key ("a.b.c", "b.c", "c") to the module dirs
// whose trailing path segments produce that key. Buckets are pre-sorted so the
// nearest source root (shortest physical path) is first.
type suffixIndex map[string][]string

// buildSuffixIndex indexes every module dir by each of its trailing-segment
// suffixes. For dir "a/b/c" it registers "a.b.c"->dir, "b.c"->dir, "c"->dir.
func buildSuffixIndex(modules map[string]bool) suffixIndex {
	idx := make(suffixIndex)
	for dir := range modules {
		if dir == "" || dir == "." {
			continue
		}
		segs := strings.Split(dir, "/")
		for i := range segs {
			key := strings.Join(segs[i:], ".")
			idx[key] = append(idx[key], dir)
		}
	}
	// Pre-sort each bucket: shortest physical path first (nearest source root),
	// then lexicographic — deterministic regardless of map iteration order.
	for key, dirs := range idx {
		sort.Slice(dirs, func(a, b int) bool {
			sa := strings.Count(dirs[a], "/")
			sb := strings.Count(dirs[b], "/")
			if sa != sb {
				return sa < sb
			}
			return dirs[a] < dirs[b]
		})
		idx[key] = dirs
	}
	return idx
}

// topLevelSegments returns the set of all path segments appearing in any module
// dir. It is used as a cheap, safe early-exit gate in resolveAbsolute: an import
// whose first dotted segment names no internal directory cannot be internal, so
// it is left for stdlib/external classification. It is permissive by design —
// it never wrongly rejects an internal import (the failure mode we are fixing).
func topLevelSegments(modules map[string]bool) map[string]bool {
	segs := make(map[string]bool)
	for dir := range modules {
		for _, s := range strings.Split(dir, "/") {
			if s != "" && s != "." {
				segs[s] = true
			}
		}
	}
	return segs
}

// resolveAbsolute maps a dotted absolute import ("airflow.models.dag") to the
// slash dir of the nearest matching internal module, or "" if none. importerDir
// is used only to skip a self-match. It tries the most specific dotted path
// first, then drops trailing segments (so "from a.b import c" — Target "a.b" —
// and "import a.b.c" both resolve to the package dir).
func resolveAbsolute(dotted string, idx suffixIndex, topPkgs map[string]bool, importerDir string) string {
	if dotted == "" {
		return ""
	}
	segs := strings.Split(dotted, ".")
	if pyStdlib[segs[0]] {
		// A stdlib top-level name (e.g. "typing", "abc") is never an internal
		// import, even if some internal directory happens to share that name
		// (e.g. a tests/.../typing dir). Suffix-matching such names produces
		// phantom couplings, so classify as stdlib instead.
		return ""
	}
	if !topPkgs[segs[0]] {
		return "" // first segment is not an internal directory → not internal
	}
	for end := len(segs); end >= 1; end-- {
		cand := strings.Join(segs[:end], ".")
		bucket := idx[cand]
		if len(bucket) == 0 {
			continue
		}
		for _, dir := range bucket {
			if dir != importerDir {
				return dir // pre-sorted: nearest source root wins
			}
		}
		// Only a self-match at this candidate; treat as no internal target so we
		// never emit a self-coupling edge.
		return ""
	}
	return ""
}

// resolveRelative maps a relative import (".", ".models", "..models.dag") to a
// slash dir, computed against importerDir. N leading dots means: start at the
// importer's dir, then go up (N-1) levels; the remaining dotted tail becomes
// slash segments. The returned dir need not be a known module — graph.go walks
// up to the nearest real ancestor.
func resolveRelative(raw, importerDir string) (string, bool) {
	n := countLeadingDots(raw)
	if n == 0 {
		return "", false
	}
	tail := strings.TrimLeft(raw, ".")
	base := importerDir
	if base == "" {
		base = "."
	}
	for i := 0; i < n-1; i++ {
		base = parentDir(base)
	}
	if tail != "" {
		tailSlash := strings.ReplaceAll(tail, ".", "/")
		if base == "." {
			base = tailSlash
		} else {
			base = base + "/" + tailSlash
		}
	}
	if base == "" {
		base = "."
	}
	return base, true
}

// --- small helpers (kept local; do not import explain's equivalents) ---

// fileDir returns the directory portion of a slash file path, or "." for a
// bare filename.
func fileDir(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[:i]
	}
	return "."
}

// parentDir returns the parent of a slash dir path, clamped at ".".
func parentDir(p string) string {
	if p == "" || p == "." {
		return "."
	}
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[:i]
	}
	return "."
}

// firstSeg returns the first dotted segment ("json.decoder" -> "json").
func firstSeg(dotted string) string {
	if i := strings.IndexByte(dotted, '.'); i >= 0 {
		return dotted[:i]
	}
	return dotted
}

// countLeadingDots counts the leading '.' characters of a relative import.
func countLeadingDots(s string) int {
	n := 0
	for n < len(s) && s[n] == '.' {
		n++
	}
	return n
}

// pyStdlib is the set of Python standard-library top-level module names. Used to
// split non-internal imports into "stdlib" vs "external" in the dependency
// breakdown (mirrors the Go extractor's stdlib classification).
var pyStdlib = map[string]bool{
	"__future__": true, "abc": true, "argparse": true, "array": true, "ast": true,
	"asyncio": true, "base64": true, "bisect": true, "builtins": true, "bz2": true,
	"calendar": true, "cgi": true, "cmath": true, "cmd": true, "codecs": true,
	"collections": true, "concurrent": true, "configparser": true, "contextlib": true,
	"contextvars": true, "copy": true, "copyreg": true, "csv": true, "ctypes": true,
	"dataclasses": true, "datetime": true, "decimal": true, "difflib": true, "dis": true,
	"doctest": true, "email": true, "encodings": true, "enum": true, "errno": true,
	"faulthandler": true, "fcntl": true, "filecmp": true, "fileinput": true, "fnmatch": true,
	"fractions": true, "ftplib": true, "functools": true, "gc": true, "getopt": true,
	"getpass": true, "gettext": true, "glob": true, "graphlib": true, "gzip": true,
	"hashlib": true, "heapq": true, "hmac": true, "html": true, "http": true,
	"imaplib": true, "importlib": true, "inspect": true, "io": true, "ipaddress": true,
	"itertools": true, "json": true, "keyword": true, "linecache": true, "locale": true,
	"logging": true, "lzma": true, "mailbox": true, "marshal": true, "math": true,
	"mimetypes": true, "mmap": true, "multiprocessing": true, "numbers": true, "operator": true,
	"os": true, "pathlib": true, "pdb": true, "pickle": true, "pickletools": true,
	"pkgutil": true, "platform": true, "plistlib": true, "posixpath": true, "pprint": true,
	"profile": true, "pstats": true, "pty": true, "pwd": true, "py_compile": true,
	"queue": true, "quopri": true, "random": true, "re": true, "reprlib": true,
	"resource": true, "runpy": true, "sched": true, "secrets": true, "select": true,
	"selectors": true, "shelve": true, "shlex": true, "shutil": true, "signal": true,
	"site": true, "smtplib": true, "socket": true, "socketserver": true, "sqlite3": true,
	"ssl": true, "stat": true, "statistics": true, "string": true, "stringprep": true,
	"struct": true, "subprocess": true, "symtable": true, "sys": true, "sysconfig": true,
	"tarfile": true, "tempfile": true, "termios": true, "textwrap": true, "threading": true,
	"time": true, "timeit": true, "tkinter": true, "token": true, "tokenize": true,
	"tomllib": true, "trace": true, "traceback": true, "tracemalloc": true, "tty": true,
	"types": true, "typing": true, "unicodedata": true, "unittest": true, "urllib": true,
	"uuid": true, "venv": true, "warnings": true, "wave": true, "weakref": true,
	"webbrowser": true, "xml": true, "xmlrpc": true, "zipapp": true, "zipfile": true,
	"zipimport": true, "zlib": true, "zoneinfo": true,
}
