package swiftextractor

import (
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/enola-labs/enola/internal/facts"
	"gopkg.in/yaml.v3"
)

// XcodeGen support: resolve Swift modules at the *target* level by parsing the
// project.yml that defines the Xcode targets, rather than grouping files by their
// leaf directory. A file's module becomes the primary source root of the
// XcodeGen target that owns it (e.g. Sources/Core), so a multi-directory
// target is a single module. Only *product* targets (application / framework /
// app-extension) are modeled; test bundles and preview hosts fall back to the
// leaf-directory behaviour (see moduleResolver.moduleFor returning ok=false).

// projectManifestName is the XcodeGen spec filename probed at the repo root.
const projectManifestName = "project.yml"

// xcodeProject is the merged view of a project.yml and its include: files.
type xcodeProject struct {
	// projectDir is the repo-relative directory containing project.yml; source
	// paths are resolved relative to it.
	projectDir string
	targets    map[string]xcodeTarget
	packages   map[string]xcodePackage
}

// xcodeTarget is one entry under `targets:`.
type xcodeTarget struct {
	Type    string        `yaml:"type"`
	Sources []xcodeSource `yaml:"sources"`
	Deps    []xcodeDep    `yaml:"dependencies"`
}

// xcodeSource is one entry under a target's `sources:`. XcodeGen allows either a
// bare path string or a mapping with path/excludes/type/buildPhase, so it
// unmarshals from both node shapes.
type xcodeSource struct {
	path     string
	excludes []string
	// resource is true for folder-reference / resource build-phase entries
	// (type: folder or buildPhase: resources); those never contribute Swift
	// sources, so they are ignored for module identity and matching.
	resource bool
}

func (s *xcodeSource) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		s.path = value.Value
		return nil
	}
	var m struct {
		Path       string   `yaml:"path"`
		Excludes   []string `yaml:"excludes"`
		Type       string   `yaml:"type"`
		BuildPhase string   `yaml:"buildPhase"`
	}
	if err := value.Decode(&m); err != nil {
		return err
	}
	s.path = m.Path
	s.excludes = m.Excludes
	s.resource = m.Type == "folder" || m.BuildPhase == "resources"
	return nil
}

// xcodeDep is one entry under a target's `dependencies:`. Exactly one of the
// fields is set per entry.
type xcodeDep struct {
	Target    string `yaml:"target"`
	Package   string `yaml:"package"`
	Product   string `yaml:"product"`
	SDK       string `yaml:"sdk"`
	Framework string `yaml:"framework"`
}

// xcodePackage is one entry under `packages:`. Local packages set Path; remote
// packages set URL (the version fields are irrelevant to module resolution).
type xcodePackage struct {
	Path string `yaml:"path"`
	URL  string `yaml:"url"`
}

func (p xcodePackage) isLocal() bool { return p.Path != "" }

// xcodeInclude is one entry under the top-level `include:`. XcodeGen accepts a
// bare path string or a mapping with path/enable, so it unmarshals from both.
type xcodeInclude struct {
	Path   string
	Enable bool
}

func (inc *xcodeInclude) UnmarshalYAML(value *yaml.Node) error {
	inc.Enable = true // includes are enabled unless explicitly disabled
	if value.Kind == yaml.ScalarNode {
		inc.Path = value.Value
		return nil
	}
	var m struct {
		Path   string `yaml:"path"`
		Enable *bool  `yaml:"enable"`
	}
	if err := value.Decode(&m); err != nil {
		return err
	}
	inc.Path = m.Path
	if m.Enable != nil {
		inc.Enable = *m.Enable
	}
	return nil
}

// xcodeFile is the raw shape of a single project.yml / include yaml file.
type xcodeFile struct {
	Include  []xcodeInclude          `yaml:"include"`
	Targets  map[string]xcodeTarget  `yaml:"targets"`
	Packages map[string]xcodePackage `yaml:"packages"`
}

// parseXcodeGenProject reads project.yml at projectRel (repo-relative) and merges
// its include: files into a single flat target/package namespace. Includes are
// resolved relative to the project root; a missing include is skipped. Returns
// nil (no error) when the manifest is absent so callers can treat XcodeGen as an
// optional, additive signal.
func parseXcodeGenProject(repoPath, projectRel string) (*xcodeProject, error) {
	projectDir := filepath.ToSlash(filepath.Dir(projectRel))

	root, err := readXcodeFile(filepath.Join(repoPath, projectRel))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	xp := &xcodeProject{
		projectDir: projectDir,
		targets:    map[string]xcodeTarget{},
		packages:   map[string]xcodePackage{},
	}
	mergeXcodeFile(xp, root)

	// Includes are relative to the project root (the dir containing project.yml).
	for _, inc := range root.Include {
		if !inc.Enable || inc.Path == "" {
			continue
		}
		incRel := path.Join(projectDir, filepath.ToSlash(inc.Path))
		incFile, err := readXcodeFileCaseInsensitive(repoPath, incRel)
		if err != nil {
			continue // a missing/unreadable include must not fail the whole parse
		}
		mergeXcodeFile(xp, incFile)
	}
	return xp, nil
}

// mergeXcodeFile folds a parsed yaml file's targets and packages into xp.
// Existing entries win (the root project.yml takes precedence over includes),
// matching XcodeGen's own merge order.
func mergeXcodeFile(xp *xcodeProject, f *xcodeFile) {
	for name, t := range f.Targets {
		if _, exists := xp.targets[name]; !exists {
			xp.targets[name] = t
		}
	}
	for name, p := range f.Packages {
		if _, exists := xp.packages[name]; !exists {
			xp.packages[name] = p
		}
	}
}

func readXcodeFile(absPath string) (*xcodeFile, error) {
	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, err
	}
	var f xcodeFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	return &f, nil
}

// readXcodeFileCaseInsensitive reads a repo-relative yaml path, retrying with a
// case-insensitive directory lookup so specs that reference `XCodegen/…` still
// resolve on case-sensitive filesystems where the directory is `XCodegen`.
func readXcodeFileCaseInsensitive(repoPath, rel string) (*xcodeFile, error) {
	if f, err := readXcodeFile(filepath.Join(repoPath, rel)); err == nil {
		return f, nil
	}
	if resolved, ok := resolveCaseInsensitive(repoPath, rel); ok {
		return readXcodeFile(filepath.Join(repoPath, resolved))
	}
	return nil, os.ErrNotExist
}

// resolveCaseInsensitive walks rel one segment at a time, matching each segment
// against directory entries case-insensitively. Returns the actual-cased
// repo-relative path when every segment resolves.
func resolveCaseInsensitive(repoPath, rel string) (string, bool) {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	cur := ""
	for _, part := range parts {
		entries, err := os.ReadDir(filepath.Join(repoPath, cur))
		if err != nil {
			return "", false
		}
		match := ""
		for _, e := range entries {
			if strings.EqualFold(e.Name(), part) {
				match = e.Name()
				break
			}
		}
		if match == "" {
			return "", false
		}
		cur = path.Join(cur, match)
	}
	return cur, true
}

// --- module resolution ---

// moduleEntry maps a set of files to an owning target module identity.
type moduleEntry struct {
	prefix    string   // directory prefix (repo-relative, slash); "" for an exact-file entry
	exactFile string   // exact file path for individual-file source entries
	excludes  []string // exclude globs, relative to prefix
	identity  string   // owning module identity (the target's primary source root)
	priority  int      // product-type priority; higher wins ties
	subdivide bool     // application/app-extension: split into per-directory packages
}

// moduleResolver maps a repo-relative Swift file to its owning target module.
type moduleResolver struct {
	entries []moduleEntry
	// identities is the set of target source-root identities, used by the caller to
	// suppress duplicate leaf-directory module facts for WHOLE targets.
	identities map[string]bool
	// subdivided is the subset of identities belonging to application/app-extension
	// targets that are split into per-directory packages. Their root identity is NOT
	// suppressed from leaf emission (files at the target root form a real package),
	// and their files run the intra-target type-reference pass.
	subdivided map[string]bool
	// targetIdentity maps a WHOLE (non-subdivided) XcodeGen target name to its module
	// identity, for resolving `dependencies: target:` edges and emitting its module
	// fact. Subdivided targets are intentionally absent (they are never import units).
	targetIdentity map[string]string
}

// subdividable reports whether an XcodeGen target type is split into per-directory
// packages instead of collapsed to one module. Only application and app-extension
// targets qualify: they are never `import` units (so subdividing them cannot dangle
// a cross-target import edge) and tend to be catch-all monoliths whose internal
// structure would otherwise be invisible. Frameworks are import units and stay whole;
// test bundles stay collapsed per bundle.
func subdividable(targetType string) bool {
	switch targetType {
	case "application", "app-extension":
		return true
	}
	return false
}

// targetPriority ranks target types so that, on the rare overlap, an app or
// extension outranks a plain framework, and any product target outranks a test
// bundle. Test bundles are modeled (priority 1) so their files collapse into one
// module per bundle rather than exploding into per-leaf-directory modules; they
// are tagged module_role=test (see moduleRoleForXcodeType) so downstream analyses
// can exclude them. Everything else (aggregate targets, preview hosts — which
// already collapse via a shared source root) returns 0 and is not modeled.
func targetPriority(targetType string) int {
	switch targetType {
	case "application":
		return 4
	case "app-extension":
		return 3
	case "framework":
		return 2
	case "bundle.unit-test", "bundle.ui-testing":
		return 1
	default:
		return 0
	}
}

// moduleRoleForXcodeType maps an XcodeGen target type to a normalized module role
// (facts.ModuleRole*). Leaf-directory-fallback modules instead use
// facts.ModuleRoleForPath; SPM targets classify by target vs testTarget.
func moduleRoleForXcodeType(targetType string) string {
	switch targetType {
	case "application", "framework", "app-extension":
		return facts.ModuleRoleProduction
	case "bundle.unit-test", "bundle.ui-testing":
		return facts.ModuleRoleTest
	default:
		return facts.ModuleRoleTooling
	}
}

// primarySourceRoot returns the target's identity: the first non-resource source
// entry's path, resolved repo-relative. Returns "" when the target has no usable
// source root.
func primarySourceRoot(t xcodeTarget, projectDir string) string {
	for _, s := range t.Sources {
		if s.resource || s.path == "" {
			continue
		}
		return path.Join(projectDir, filepath.ToSlash(s.path))
	}
	return ""
}

// buildModuleResolver builds a resolver from the XcodeGen product targets and the
// SPM target roots (name -> module dir). Either input may be nil.
func buildModuleResolver(xp *xcodeProject, spmRoots map[string]string) *moduleResolver {
	r := &moduleResolver{
		identities:     map[string]bool{},
		subdivided:     map[string]bool{},
		targetIdentity: map[string]string{},
	}

	if xp != nil {
		// Deterministic iteration over targets for stable tie-breaks.
		names := make([]string, 0, len(xp.targets))
		for name := range xp.targets {
			names = append(names, name)
		}
		sort.Strings(names)

		for _, name := range names {
			t := xp.targets[name]
			prio := targetPriority(t.Type)
			if prio == 0 {
				continue // product targets only
			}
			identity := primarySourceRoot(t, xp.projectDir)
			if identity == "" {
				continue
			}
			// Several product targets can share one source root (e.g. the app, a
			// SwiftUI-preview host, and a unit-test host all root at the app
			// directory). Collapse them: the first target (by sorted name) owning an
			// identity is canonical; shadow targets are dropped entirely so they add
			// no duplicate module fact, dependency edges, or source entries.
			if r.identities[identity] {
				continue
			}
			r.identities[identity] = true
			sub := subdividable(t.Type)
			if sub {
				// A subdivided target has no single flat module and is never imported
				// by name, so it is kept out of targetIdentity (no flat module fact, no
				// declared-dependency edges — its per-file imports cover those).
				r.subdivided[identity] = true
			} else {
				r.targetIdentity[name] = identity
			}

			for _, s := range t.Sources {
				if s.resource || s.path == "" {
					continue
				}
				p := path.Join(xp.projectDir, filepath.ToSlash(s.path))
				e := moduleEntry{excludes: s.excludes, identity: identity, priority: prio, subdivide: sub}
				if isLikelyFile(p) {
					e.exactFile = p
				} else {
					e.prefix = p
				}
				r.entries = append(r.entries, e)
			}
		}
	}

	// SPM targets: seed as product-priority prefixes so package sources declare
	// into their SPM target module rather than their leaf directory.
	spmNames := make([]string, 0, len(spmRoots))
	for name := range spmRoots {
		spmNames = append(spmNames, name)
	}
	sort.Strings(spmNames)
	for _, name := range spmNames {
		dir := spmRoots[name]
		r.identities[dir] = true
		r.entries = append(r.entries, moduleEntry{prefix: dir, identity: dir, priority: 2})
	}

	return r
}

// isLikelyFile reports whether a source path names an individual file (has a
// recognised source extension) rather than a directory.
func isLikelyFile(p string) bool {
	switch strings.ToLower(path.Ext(p)) {
	case ".swift", ".m", ".mm", ".h", ".c", ".cpp":
		return true
	}
	return false
}

// resolveTarget returns the winning target identity for a repo-relative Swift file,
// whether that target is subdivided, and whether any product target matched. ok is
// false when no target covers the file (caller falls back to the leaf directory) or
// when the best match is ambiguously shared by multiple equal-priority targets.
func (r *moduleResolver) resolveTarget(relFile string) (identity string, subdivide bool, ok bool) {
	if r == nil {
		return "", false, false
	}
	rel := filepath.ToSlash(relFile)

	type cand struct {
		identity  string
		priority  int
		specific  int // match specificity; higher is more specific
		subdivide bool
	}
	var cands []cand
	for _, e := range r.entries {
		switch {
		case e.exactFile != "":
			if e.exactFile != rel {
				continue
			}
			// Exact-file entries are the most specific possible match.
			cands = append(cands, cand{e.identity, e.priority, len(rel) + 1, e.subdivide})
		case e.prefix != "":
			if rel != e.prefix && !strings.HasPrefix(rel, e.prefix+"/") {
				continue
			}
			subPath := strings.TrimPrefix(strings.TrimPrefix(rel, e.prefix), "/")
			if matchExcludes(subPath, e.excludes) {
				continue
			}
			cands = append(cands, cand{e.identity, e.priority, len(e.prefix), e.subdivide})
		}
	}
	if len(cands) == 0 {
		return "", false, false
	}

	// Winner: highest priority, then most specific match. If several distinct
	// identities tie at the top, the file is shared → ambiguous, fall back.
	best := cands[0]
	for _, c := range cands[1:] {
		if c.priority > best.priority ||
			(c.priority == best.priority && c.specific > best.specific) {
			best = c
		}
	}
	distinct := map[string]bool{}
	for _, c := range cands {
		if c.priority == best.priority && c.specific == best.specific {
			distinct[c.identity] = true
		}
	}
	if len(distinct) != 1 {
		return "", false, false // shared across equal-priority targets
	}
	return best.identity, best.subdivide, true
}

// moduleFor returns the owning module identity for a repo-relative Swift file. For a
// subdivided application/app-extension target it is the file's LEAF DIRECTORY, giving
// Go/Ruby-style per-directory packages; for any other resolved target it is the flat
// target source-root identity. ok is false when no target covers the file (caller
// falls back to the leaf directory).
func (r *moduleResolver) moduleFor(relFile string) (string, bool) {
	identity, subdivide, ok := r.resolveTarget(relFile)
	if !ok {
		return "", false
	}
	if subdivide {
		return path.Dir(filepath.ToSlash(relFile)), true
	}
	return identity, true
}

// subdividesFile reports whether a file belongs to a subdivided (application/
// app-extension) target. Callers use it to run the type-reference dependency pass
// INTRA-target: files within one Swift module never `import` one another, so type
// references are the only source of the directory→directory coupling edges that give
// the per-directory sub-packages meaningful Ca/Ce.
func (r *moduleResolver) subdividesFile(relFile string) bool {
	_, subdivide, ok := r.resolveTarget(relFile)
	return ok && subdivide
}

// matchExcludes reports whether sub (a path relative to a source prefix) matches
// any of the exclude globs. Patterns without a slash match against the basename
// (so "*.md" and "Info.plist" work at any depth); patterns with a slash match the
// full relative path.
func matchExcludes(sub string, patterns []string) bool {
	if sub == "" || len(patterns) == 0 {
		return false
	}
	base := path.Base(sub)
	for _, pat := range patterns {
		pat = filepath.ToSlash(pat)
		if strings.Contains(pat, "/") {
			if ok, _ := path.Match(pat, sub); ok {
				return true
			}
			continue
		}
		if ok, _ := path.Match(pat, base); ok {
			return true
		}
	}
	return false
}

// --- fact emission ---

// emitXcodeGenFacts produces the module facts for each modeled product target
// and the dependency facts for their declared dependencies. dirToFile maps a
// module identity to a representative source file (from the AST walk); it is used
// to anchor each dependency fact's File inside the source module's directory so
// the facts graph synthesises a module→module import edge.
func emitXcodeGenFacts(r *moduleResolver, xp *xcodeProject, spmRoots map[string]string, dirToFile map[string]string) []facts.Fact {
	if xp == nil || r == nil {
		return nil
	}
	var out []facts.Fact

	names := make([]string, 0, len(xp.targets))
	for name := range xp.targets {
		names = append(names, name)
	}
	sort.Strings(names)

	// Module fact per modeled product target.
	for _, name := range names {
		identity, ok := r.targetIdentity[name]
		if !ok {
			continue
		}
		out = append(out, facts.Fact{
			Kind: facts.KindModule,
			Name: identity,
			File: identity,
			Props: map[string]any{
				"language":     "swift",
				"xcode_target": name,
				"xcode_type":   xp.targets[name].Type,
				"module_role":  moduleRoleForXcodeType(xp.targets[name].Type),
			},
		})
	}

	// Dependency fact per declared edge, deduped by (from,to).
	type edge struct{ from, to string }
	seen := map[edge]bool{}
	for _, name := range names {
		from, ok := r.targetIdentity[name]
		if !ok {
			continue
		}
		file := moduleAnchorFile(from, dirToFile)
		for _, d := range xp.targets[name].Deps {
			to, source := resolveXcodeDep(d, r, xp, spmRoots)
			if to == "" || to == from {
				continue
			}
			e := edge{from, to}
			if seen[e] {
				continue
			}
			seen[e] = true
			out = append(out, facts.Fact{
				Kind: facts.KindDependency,
				Name: from + " -> " + to,
				File: file,
				Props: map[string]any{
					"language": "swift",
					"source":   source,
					"xcode":    true,
				},
				Relations: []facts.Relation{{Kind: facts.RelImports, Target: to}},
			})
		}
	}
	return out
}

// resolveXcodeDep resolves an XcodeGen dependency entry to a target string and a
// source classification ("internal"/"external"). Returns "" to skip the edge.
func resolveXcodeDep(d xcodeDep, r *moduleResolver, xp *xcodeProject, spmRoots map[string]string) (string, string) {
	switch {
	case d.Target != "":
		if id, ok := r.targetIdentity[d.Target]; ok {
			return id, "internal"
		}
		return "", "" // depends on a non-modeled (e.g. test) target
	case d.Package != "":
		pkg, ok := xp.packages[d.Package]
		if ok && pkg.isLocal() {
			// Local package: resolve the imported product/package to its SPM
			// target module identity when known.
			if id, ok := spmRoots[d.Product]; ok {
				return id, "internal"
			}
			if id, ok := spmRoots[d.Package]; ok {
				return id, "internal"
			}
		}
		// Remote (or unresolved local) package: external, keyed by product name.
		if d.Product != "" {
			return d.Product, "external"
		}
		return d.Package, "external"
	case d.Framework != "":
		return strings.TrimSuffix(strings.TrimSuffix(d.Framework, ".xcframework"), ".framework"), "external"
	case d.SDK != "":
		return strings.TrimSuffix(d.SDK, ".framework"), "external"
	}
	return "", ""
}

// moduleAnchorFile returns a file path whose immediate parent directory is
// exactly identity, so the facts graph's dependency→module bridge
// (fileDirectory(File) == module Name) fires. It reuses a real representative
// file only when that file lives directly in the identity directory; otherwise
// (the common case for multi-directory targets) it returns a synthetic anchor
// inside the identity directory.
func moduleAnchorFile(identity string, dirToFile map[string]string) string {
	if f, ok := dirToFile[identity]; ok && slashDir(f) == identity {
		return f
	}
	return identity + "/" + projectManifestName
}

// slashDir returns the parent directory of a slash-separated path.
func slashDir(p string) string {
	p = filepath.ToSlash(p)
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[:i]
	}
	return "."
}
