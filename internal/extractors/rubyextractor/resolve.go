package rubyextractor

import (
	"sort"
	"strings"

	"github.com/enola-labs/enola/internal/facts"
)

// resolveImports derives internal module-coupling edges for Ruby and returns them
// as synthetic dependency facts. Rails autoloads constants, so internal coupling
// is expressed through constant references — class inheritance, include/extend
// mixins, ActiveRecord associations, and method calls — whose relation targets are
// Ruby constant names, not directory paths. None of those relation kinds are
// counted by the coupling consumers (graph, package metrics, explain hotspots),
// which only count dependency facts whose `imports` target matches a module Name.
//
// This pass builds a constant -> declaring-module-dir index, resolves every
// cross-module constant reference to a srcDir -> destDir edge, and emits one
// synthetic dependency fact per unique edge (with an `imports` relation to the
// destination module dir). It also resolves require_relative paths and Packwerk
// package.yml dependencies, and classifies require/require_relative facts as
// internal/stdlib/external in place. This mirrors the Python extractor's resolve
// pass; it never guesses — every edge comes from a real parsed reference.
func resolveImports(allFacts []facts.Fact, isRails bool) []facts.Fact {
	ix := buildConstIndex(allFacts)
	moduleNames := collectModuleNames(allFacts)

	edges := map[[2]string]bool{}
	add := func(src, dst string) {
		if src == "" || dst == "" || src == dst {
			return // skip empties and self-edges
		}
		edges[[2]string{src, dst}] = true
	}

	for i := range allFacts {
		f := &allFacts[i]
		switch f.Kind {
		case facts.KindSymbol:
			src := declaresTarget(f)
			for _, rel := range f.Relations {
				switch rel.Kind {
				case facts.RelImplements: // inheritance
					add(src, ix.resolve(rel.Target))
				case facts.RelCalls:
					if c := constFromCall(rel.Target); c != "" {
						add(src, ix.resolve(c))
					}
				}
			}
		case facts.KindDependency:
			src := fileDir(f.File)
			for j := range f.Relations {
				rel := &f.Relations[j]
				switch rel.Kind {
				case facts.RelImplements: // include/extend/prepend mixins
					if dst := ix.resolve(rel.Target); dst != "" {
						add(src, dst)
						setSource(f, "internal")
					} else {
						setSource(f, "external")
					}
				case facts.RelDependsOn: // ActiveRecord associations
					if dst := ix.resolve(rel.Target); dst != "" {
						add(src, dst)
						setSource(f, "internal")
					} else {
						setSource(f, "external")
					}
				case facts.RelImports: // require / require_relative
					classifyRequire(f, rel, src, moduleNames, add)
				}
			}
		case facts.KindModule:
			// Packwerk package.yml dependencies: explicit module -> module edges.
			for _, rel := range f.Relations {
				if rel.Kind == facts.RelDependsOn {
					add(packwerkDir(f.Name), packwerkDir(rel.Target))
				}
			}
		}
	}

	return emitEdges(edges, isRails)
}

// constIndex resolves a Ruby constant reference to the slash dir of the module
// that declares it.
type constIndex struct {
	qualified map[string]string   // "Orders::Order" -> "app/models/orders"
	bare      map[string][]string // "Order" -> ["app/models/orders", ...] (sorted, deduped)
}

// buildConstIndex indexes every class/module/constant symbol by its qualified and
// bare names. Source dir is the declares-relation target (fallback fileDir).
func buildConstIndex(allFacts []facts.Fact) *constIndex {
	ix := &constIndex{qualified: map[string]string{}, bare: map[string][]string{}}
	for i := range allFacts {
		f := &allFacts[i]
		if f.Kind != facts.KindSymbol {
			continue
		}
		switch sk, _ := f.Props["symbol_kind"].(string); sk {
		case facts.SymbolClass, facts.SymbolInterface, facts.SymbolConstant:
		default:
			continue
		}
		dir := declaresTarget(f)
		if dir == "" {
			continue
		}
		qn := stripLeadingColons(f.Name)
		if qn == "" {
			continue
		}
		// Qualified: prefer the shortest declaring dir on collision (nearest root).
		if cur, ok := ix.qualified[qn]; !ok || shorter(dir, cur) {
			ix.qualified[qn] = dir
		}
		bare := lastSegment(qn)
		ix.bare[bare] = append(ix.bare[bare], dir)
	}
	for k, dirs := range ix.bare {
		ix.bare[k] = sortDedupDirs(dirs)
	}
	return ix
}

// resolve returns the declaring module dir of a constant reference, or "".
func (ix *constIndex) resolve(ref string) string {
	ref = stripLeadingColons(ref)
	if ref == "" {
		return ""
	}
	if dir, ok := ix.qualified[ref]; ok {
		return dir
	}
	if dirs := ix.bare[lastSegment(ref)]; len(dirs) > 0 {
		return dirs[0] // pre-sorted: shortest dir, then lexicographic
	}
	return ""
}

// classifyRequire resolves a require/require_relative dependency fact: relative
// requires are intra-project (resolved to a module dir when possible); absolute
// requires are stdlib or external. Sets Props["source"] in place.
func classifyRequire(f *facts.Fact, rel *facts.Relation, src string, moduleNames map[string]bool, add func(s, d string)) {
	raw := rel.Target
	isRel, _ := f.Props["require_relative"].(bool)
	switch {
	case isRel || strings.HasPrefix(raw, "."):
		if dst := resolveRequireRelative(raw, src, moduleNames); dst != "" {
			add(src, dst)
		}
		setSource(f, "internal")
	case rubyStdlib[raw] || rubyStdlib[firstPathSeg(raw)]:
		setSource(f, "stdlib")
	default:
		setSource(f, "external")
	}
}

// resolveRequireRelative resolves a relative require path (e.g. "../helper")
// against the importing file's dir, then walks up to the nearest known module.
// Returns "" if it cannot be placed inside the project.
func resolveRequireRelative(raw, importerDir string, moduleNames map[string]bool) string {
	p := strings.TrimSuffix(raw, ".rb")
	base := importerDir
	if base == "" {
		base = "."
	}
	for _, seg := range strings.Split(p, "/") {
		switch seg {
		case "", ".":
			// stay
		case "..":
			base = parentDir(base)
		default:
			if base == "." {
				base = seg
			} else {
				base = base + "/" + seg
			}
		}
	}
	// The resolved path points at a file; its module is the containing dir, walked
	// up to the nearest known module.
	return nearestModule(fileDir(base), moduleNames)
}

// nearestModule walks up dir's ancestors until it finds a known module, or "".
func nearestModule(dir string, moduleNames map[string]bool) string {
	cur := dir
	for cur != "" && cur != "." {
		if moduleNames[cur] {
			return cur
		}
		cur = parentDir(cur)
	}
	if moduleNames[cur] {
		return cur
	}
	return ""
}

// emitEdges builds one synthetic dependency fact per unique edge. File is set to
// "<srcDir>/_coupling.rb" so that consumers deriving the source module via
// fileDir(File) recover exactly srcDir (a bare srcDir would lose its last segment).
func emitEdges(edges map[[2]string]bool, isRails bool) []facts.Fact {
	out := make([]facts.Fact, 0, len(edges))
	for e := range edges {
		src, dst := e[0], e[1]
		props := map[string]any{
			"language":           "ruby",
			"source":             "internal",
			"synthetic_coupling": true,
		}
		if isRails {
			props["framework"] = "rails"
		}
		out = append(out, facts.Fact{
			Kind:      facts.KindDependency,
			Name:      src + " -> " + dst,
			File:      src + "/_coupling.rb",
			Props:     props,
			Relations: []facts.Relation{{Kind: facts.RelImports, Target: dst}},
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// --- helpers ---

// constFromCall turns a calls-target into its receiver constant, or "" when the
// receiver is not a constant. Splits on the LAST '.' so the inner '::' of a
// namespaced receiver is preserved: "Foo::Bar.method" -> "Foo::Bar",
// "Account.active" -> "Account", "var.method" -> "" (lowercase receiver).
func constFromCall(target string) string {
	target = stripLeadingColons(target)
	dot := strings.LastIndex(target, ".")
	if dot < 0 {
		return ""
	}
	recv := target[:dot]
	if recv == "" || !startsUpper(recv) {
		return ""
	}
	return recv
}

// declaresTarget returns a fact's declares-relation target (its module dir),
// falling back to the directory of its file.
func declaresTarget(f *facts.Fact) string {
	for _, rel := range f.Relations {
		if rel.Kind == facts.RelDeclares {
			return rel.Target
		}
	}
	return fileDir(f.File)
}

// collectModuleNames returns the set of module-fact Names.
func collectModuleNames(allFacts []facts.Fact) map[string]bool {
	m := make(map[string]bool)
	for i := range allFacts {
		if allFacts[i].Kind == facts.KindModule {
			m[allFacts[i].Name] = true
		}
	}
	return m
}

// setSource sets Props["source"] if not already set.
func setSource(f *facts.Fact, source string) {
	if f.Props == nil {
		f.Props = map[string]any{}
	}
	if _, ok := f.Props["source"]; !ok {
		f.Props["source"] = source
	}
}

// packwerkDir normalizes a Packwerk package name/target: "root" -> ".".
func packwerkDir(name string) string {
	if name == "root" {
		return "."
	}
	return name
}

// fileDir returns the directory portion of a slash file path, or "." for a bare
// filename.
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

// stripLeadingColons removes a leading "::" from a Ruby constant reference.
func stripLeadingColons(s string) string {
	return strings.TrimPrefix(s, "::")
}

// lastSegment returns the final "::"-separated segment ("Orders::Order" -> "Order").
func lastSegment(s string) string {
	if i := strings.LastIndex(s, "::"); i >= 0 {
		return s[i+2:]
	}
	return s
}

// firstPathSeg returns the segment before the first '/' ("net/http" -> "net").
func firstPathSeg(s string) string {
	if i := strings.IndexByte(s, '/'); i >= 0 {
		return s[:i]
	}
	return s
}

// startsUpper reports whether the first character is an ASCII uppercase letter
// (a Ruby constant always starts uppercase; a variable receiver does not).
func startsUpper(s string) bool {
	return len(s) > 0 && s[0] >= 'A' && s[0] <= 'Z'
}

// shorter reports whether dir a is "nearer a source root" than b: fewer path
// segments, then lexicographically smaller.
func shorter(a, b string) bool {
	sa, sb := strings.Count(a, "/"), strings.Count(b, "/")
	if sa != sb {
		return sa < sb
	}
	return a < b
}

// sortDedupDirs sorts dirs by the shorter() order and removes duplicates.
func sortDedupDirs(dirs []string) []string {
	sort.Slice(dirs, func(i, j int) bool { return shorter(dirs[i], dirs[j]) })
	out := dirs[:0:0]
	var prev string
	for i, d := range dirs {
		if i == 0 || d != prev {
			out = append(out, d)
		}
		prev = d
	}
	return out
}

// rubyStdlib is the set of Ruby standard-library require names, used to split
// non-internal requires into "stdlib" vs "external" in the dependency breakdown.
var rubyStdlib = map[string]bool{
	"set": true, "json": true, "yaml": true, "psych": true, "date": true,
	"time": true, "securerandom": true, "digest": true, "openssl": true,
	"net/http": true, "net/https": true, "net/smtp": true, "net/imap": true,
	"net/pop": true, "net/ftp": true, "net": true, "fileutils": true,
	"logger": true, "forwardable": true, "singleton": true, "ostruct": true,
	"pathname": true, "uri": true, "base64": true, "csv": true, "erb": true,
	"tempfile": true, "stringio": true, "benchmark": true, "monitor": true,
	"timeout": true, "thread": true, "fiber": true, "socket": true,
	"resolv": true, "ipaddr": true, "zlib": true, "stringscanner": true,
	"strscan": true, "io/console": true, "io/wait": true, "pp": true,
	"pstore": true, "delegate": true, "observer": true, "comparable": true,
	"enumerator": true, "rational": true, "complex": true, "bigdecimal": true,
	"prime": true, "matrix": true, "abbrev": true, "shellwords": true,
	"optparse": true, "getoptlong": true, "tsort": true, "weakref": true,
	"objspace": true, "coverage": true, "ripper": true, "readline": true,
	"etc": true, "fcntl": true, "syslog": true, "open3": true, "open-uri": true,
	"tmpdir": true, "find": true, "rbconfig": true, "mkmf": true, "rubygems": true,
}
