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
func resolveImports(allFacts []facts.Fact, modules map[string]bool, pkgDirs map[string]bool) {
	idx := buildSuffixIndex(modules, pkgDirs)
	topPkgs := importableRoots(modules, pkgDirs)

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
func resolveCallTargets(allFacts []facts.Fact, fileModules map[string]bool, pkgDirs map[string]bool) {
	fileIdx := buildSuffixIndex(fileModules, pkgDirs)
	topPkgs := importableRoots(fileModules, pkgDirs)
	reexports := buildReexportIndex(allFacts, pkgDirs)

	// Every symbol name in the snapshot, so a class-qualified chain can only bind to
	// a name that really exists (see resolveDottedTarget step 3).
	symbols := make(map[string]bool)
	for i := range allFacts {
		if allFacts[i].Kind == facts.KindSymbol {
			symbols[allFacts[i].Name] = true
		}
	}

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
				resolved, keep := resolveDottedTarget(rel.Target, fileIdx, topPkgs, importerDir, reexports, symbols)
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
func buildReexportIndex(allFacts []facts.Fact, pkgDirs map[string]bool) reexportIndex {
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
	return reexportIndex{byDir: byDir, dirs: buildSuffixIndex(dirs, pkgDirs)}
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
func resolveDottedTarget(dotted string, fileIdx suffixIndex, topPkgs map[string]bool, importerDir string, reexports reexportIndex, symbols map[string]bool) (string, bool) {
	li := strings.LastIndexByte(dotted, '.')
	if li <= 0 {
		return dotted, true
	}
	modulePrefix := dotted[:li]

	// Walk the module/symbol split point leftwards, longest module prefix first, so
	// the symbol part grows: "pkg.mod.Cls.method" is tried as (pkg.mod.Cls, method)
	// and then (pkg.mod, Cls.method). The walker qualifies symbols as
	// module.Class.method, so the second shape is the one that names a real symbol.
	//
	// The lookup is EXACT at each position. resolveAbsolute drops trailing segments
	// on failure, which is right for an import (a.b.c may name a symbol inside
	// package a.b) but wrong here: the segments it drops are the class the symbol
	// hangs off, so it silently turned "mod.Cls.method" into "mod.method" — not a
	// dangling target but a WRONG one, pointing at whatever else happens to bear
	// that name.
	//
	// A multi-segment symbol part is a weaker claim than a single one, so it must be
	// CONFIRMED against the snapshot's symbol names before being accepted; otherwise
	// a shorter prefix would mint a plausible-but-wrong edge, which is worse than
	// leaving the target dangling. symbols may be nil, which disables confirmation
	// and therefore the multi-segment shapes entirely.
	for cut := li; cut > 0; cut = strings.LastIndexByte(dotted[:cut], '.') {
		prefix, symbolPath := dotted[:cut], dotted[cut+1:]
		confirmed := strings.IndexByte(symbolPath, '.') < 0 // single segment: legacy shape

		if dir := resolveModuleExact(prefix, fileIdx, topPkgs, importerDir); dir != "" {
			if cand := dir + "." + symbolPath; confirmed || symbols[cand] {
				return cand, true
			}
		}
		// The prefix may name a PACKAGE whose __init__.py re-exports the symbol; bind
		// to the module that actually defines it.
		if mod := reexports.lookup(prefix, firstSeg(symbolPath), topPkgs, importerDir); mod != "" {
			if cand := mod + "." + symbolPath; confirmed || symbols[cand] {
				return cand, true
			}
		}
	}

	// Internal, but nothing more precise is known: keep the dotted target so
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

// packageDirs returns the directories containing an __init__.py, i.e. the ones
// Python treats as packages. buildSuffixIndex uses it to tell a genuine top-level
// package from a like-named directory nested inside another package.
func packageDirs(pyFiles []string) map[string]bool {
	out := make(map[string]bool)
	for _, f := range pyFiles {
		if !isInitFile(f) {
			continue
		}
		dir := fileDir(f)
		if dir == "." {
			dir = ""
		}
		out[dir] = true
	}
	return out
}

// buildSuffixIndex indexes every module dir by its trailing-segment suffixes, so
// an import written relative to a source root ("airflow.models.dag") finds the
// module at "airflow-core/src/airflow/models/dag".
//
// A suffix is only registered where it is genuinely importable. Python's rule is
// that a directory is a top-level package only if its PARENT is not itself a
// package: "airflow-core/src/airflow" qualifies because "src" holds no
// __init__.py, but a nested "…/relational/sqlalchemy" does not, because
// "relational" is a package and so that directory is only reachable as
// relational.sqlalchemy.
//
// Registering every suffix unconditionally let an internal directory that merely
// SHARES A NAME with a third-party package capture its imports: a repo with
// "…/databases/relational/sqlalchemy" and "cognee/alembic" had plain
// `import sqlalchemy` resolve internally, marking those dependencies source:
// internal and keeping ~500 third-party call edges in the graph as if they were
// first-party.
//
// pkgDirs is the set of directories containing an __init__.py. When it is empty
// (a caller with no package information) the rule cannot fire and the index keeps
// its historical permissive shape, so this only ever tightens where there is
// evidence to tighten on.
func buildSuffixIndex(modules map[string]bool, pkgDirs map[string]bool) suffixIndex {
	idx := make(suffixIndex)
	for dir := range modules {
		if dir == "" || dir == "." {
			continue
		}
		segs := strings.Split(dir, "/")
		for i := range segs {
			// i == 0 is the full repo-relative path, always addressable. Beyond that,
			// the match starts at segs[i], so segs[:i] must not be a package.
			if i > 0 && pkgDirs[strings.Join(segs[:i], "/")] {
				continue
			}
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

// importableRoots returns the segment names an absolute import may START with —
// the names reachable on sys.path rather than every directory name in the tree.
//
// It gates resolveAbsolute (an import whose first segment names no internal root
// cannot be internal) and resolveDottedTarget's keep-dotted branch. Both formerly
// used every path segment at any depth, which is why a nested
// "…/relational/sqlalchemy" made `sqlalchemy.Column` look internal even after
// suffix matching was tightened: the fix has to hold in both places or the edge is
// merely retained one step later.
//
// A segment qualifies under the same package-boundary rule buildSuffixIndex uses:
// it is at the repo root, or the path above it is not itself a package. With
// pkgDirs empty the rule cannot fire and every segment qualifies, preserving the
// historical permissive behaviour.
func importableRoots(modules map[string]bool, pkgDirs map[string]bool) map[string]bool {
	roots := make(map[string]bool)
	for dir := range modules {
		segs := strings.Split(dir, "/")
		for i, s := range segs {
			if s == "" || s == "." {
				continue
			}
			if i > 0 && pkgDirs[strings.Join(segs[:i], "/")] {
				break // everything deeper is a subpackage, not a root
			}
			roots[s] = true
		}
	}
	return roots
}

// resolveModuleExact maps a dotted path to a module dir WITHOUT dropping trailing
// segments, the difference from resolveAbsolute that matters for a call target:
// shortening is correct for an import (a.b.c may name a symbol inside package a.b)
// but for a call it discards the class the symbol belongs to.
func resolveModuleExact(dotted string, idx suffixIndex, topPkgs map[string]bool, importerDir string) string {
	if dotted == "" {
		return ""
	}
	seg0 := firstSeg(dotted)
	if pyStdlib[seg0] || !topPkgs[seg0] {
		return ""
	}
	for _, dir := range idx[dotted] {
		if dir != importerDir {
			return dir // pre-sorted: nearest source root wins
		}
	}
	return ""
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
