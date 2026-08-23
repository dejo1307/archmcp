package rubydex

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/enola-labs/enola/internal/facts"
)

const builtInURI = "rubydex:built-in"

var indexableExtensions = map[string]bool{".rb": true, ".rake": true, ".rbs": true, ".ru": true}

// Indexable reports whether the engine indexes a file of this name, which is
// what decides whether its content belongs in the index cache key.
func Indexable(path string) bool {
	return indexableExtensions[filepath.Ext(path)]
}

// Result is what one run of the provider produced: the facts, the census the
// seam records, and a refusal when the workspace could not be read at all.
type Result struct {
	Facts   []facts.Fact
	Census  facts.ProviderCensus
	Refusal string
}

type collector struct {
	g                   *graph
	root                string
	workspace           string
	facts               []facts.Fact
	unresolved          int
	aliasedReferences   int
	undefinedTargets    int
	enclosingReceivers  int
	untypedReceivers    int
	aliasedDeclarations int
	dependencyPaths     int
	bundleAbsent        bool
	documentCache       map[uint64][]span
}

type span struct {
	name        string
	startLine   int
	endLine     int
	startColumn int
}

// Collect indexes the repository at root with the loaded library and returns
// the facts the reference script would have emitted for it.
func Collect(ctx context.Context, lib *Library, root string) Result {
	root, err := filepath.Abs(root)
	if err != nil {
		return Result{Refusal: err.Error()}
	}
	if _, err := os.Stat(filepath.Join(root, "Gemfile")); err != nil {
		return Result{Census: facts.ProviderCensus{ConstructsSkipped: 1, SkipCauses: []facts.CensusCause{{Cause: "no Gemfile: not a workspace Rubydex can index", Count: 1}}}}
	}
	c := &collector{root: root, workspace: "file://" + root + "/", documentCache: map[uint64][]span{}}
	paths, dependencyPaths, bundleAbsent := workspacePaths(ctx, root)
	c.dependencyPaths = dependencyPaths
	c.bundleAbsent = bundleAbsent

	c.g = lib.newGraph()
	defer c.g.close()
	c.g.index(paths)
	c.g.resolve()

	var documents []uint64
	for _, doc := range c.g.documents() {
		if c.inWorkspace(c.g.documentURI(doc)) {
			documents = append(documents, doc)
		}
	}
	for _, doc := range documents {
		for _, definition := range c.g.definitions(doc) {
			c.emitAncestry(definition)
		}
		for _, reference := range c.g.methodReferences(doc) {
			c.emitCall(doc, reference)
		}
	}
	c.emitReferences(c.g.constantReferences())
	sort.Slice(c.facts, func(i, j int) bool { return c.facts[i].Name < c.facts[j].Name })
	return Result{Facts: c.facts, Census: c.census(len(documents), c.g.diagnosticCount())}
}

// workspacePaths mirrors the gem's own listing: the workspace's top-level
// directories and Ruby files, then the lib and app directories of every gem the
// bundle reports, so Rails engines resolve like plain gems do. Without
// a bundle on PATH the workspace alone is indexed and the census says so.
func workspacePaths(ctx context.Context, root string) (paths []string, dependencyPaths int, bundleAbsent bool) {
	entries, _ := os.ReadDir(root)
	for _, entry := range entries {
		full := filepath.Join(root, entry.Name())
		if entry.IsDir() || indexableExtensions[filepath.Ext(entry.Name())] {
			paths = append(paths, full)
		}
	}
	bundle, err := exec.LookPath("bundle")
	if err != nil {
		return paths, 0, true
	}
	cmd := exec.CommandContext(ctx, bundle, "list", "--paths")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return paths, 0, true
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || seen[line] {
			continue
		}
		seen[line] = true
		for _, sub := range []string{"lib", "app"} {
			if info, err := os.Stat(filepath.Join(line, sub)); err == nil && info.IsDir() {
				paths = append(paths, filepath.Join(line, sub))
				dependencyPaths++
			}
		}
	}
	return paths, dependencyPaths, false
}

func (c *collector) inWorkspace(uri string) bool {
	return strings.HasPrefix(uri, c.workspace)
}

func (c *collector) relative(uri string) string {
	return strings.TrimPrefix(uri, c.workspace)
}

func (c *collector) emitAncestry(definition cDefinition) {
	if definition.kind != definitionClass && definition.kind != definitionModule {
		return
	}
	declaration, ok := c.g.definitionDeclaration(definition.id)
	if !ok {
		return
	}
	if declaration.kind != declarationClass && declaration.kind != declarationModule {
		c.aliasedDeclarations++
		return
	}
	location, ok := c.g.definitionLocation(definition.id)
	if !ok {
		return
	}
	name := c.g.declarationName(declaration.id)
	distance := 0
	for _, ancestor := range c.g.ancestors(declaration.id) {
		if c.builtIn(ancestor.id) {
			break
		}
		ancestorName := c.g.declarationName(ancestor.id)
		if distance > 0 && ancestorName != name {
			c.facts = append(c.facts, c.fact("rubydex-ancestor: "+name+" -> "+ancestorName, location, "resolved",
				facts.Relation{Kind: "implements", Target: ancestorName},
				map[string]any{"ancestor_distance": distance, "declared_in_workspace": c.declaredInWorkspace(ancestor.id)}))
		}
		distance++
	}
}

func (c *collector) emitCall(doc uint64, reference cMethodReference) {
	receiver, ok := c.g.methodReferenceReceiver(reference.id)
	if !ok {
		c.untypedReceivers++
		return
	}
	location, ok := c.g.methodReferenceLocation(reference.id)
	if !ok {
		return
	}
	receiverFull := c.g.declarationName(receiver.id)
	receiverName := plainName(receiverFull)
	enclosing := c.enclosingName(doc, location)
	if enclosing != "" && sameLexicalOwner(enclosing, receiverName) {
		c.enclosingReceivers++
		return
	}
	separator := "#"
	if singleton(receiverFull) {
		separator = "."
	}
	callee := receiverName + separator + c.g.methodReferenceName(reference.id)
	caller := enclosing
	if caller == "" {
		caller = c.relative(location.URI)
	}
	c.facts = append(c.facts, c.fact("rubydex-call: "+caller+" -> "+callee, location, "constant-receiver",
		facts.Relation{Kind: "calls", Target: callee}, nil))
}

// A constant reference the engine reports, held with what decides its fate:
// where it sits, whether it is an inner segment of a qualified path, and what
// it resolved to.
type constantReference struct {
	id       uint64
	doc      uint64
	location Location
	inner    bool
	resolved bool
	target   cDeclaration
}

// emitReferences turns the workspace's constant references into dependency
// facts, one per qualified path. The engine reports a reference per segment,
// so `Foo::VERSION` arrives as `Foo` and as `VERSION`, adjacent on one line
// with the separator between them; the leaf is the dependency and the
// segments before it are its path, because a read of `Foo::VERSION` depends
// on `VERSION` and only names `Foo` on the way. A prefix emitted as its own
// dependency landed on whichever reopening of `Foo` a consumer took first.
func (c *collector) emitReferences(ids []cConstantReference) {
	byLine := map[string][]*constantReference{}
	var refs []*constantReference
	for _, id := range ids {
		location, ok := c.g.constantReferenceLocation(id.id)
		if !ok || !c.inWorkspace(location.URI) {
			continue
		}
		ref := &constantReference{id: id.id, location: location}
		ref.doc, _ = c.g.constantReferenceDocument(id.id)
		ref.target, ref.resolved = c.g.resolvedConstantReference(id.id)
		key := location.URI + "\x00" + strconv.Itoa(location.StartLine)
		byLine[key] = append(byLine[key], ref)
		refs = append(refs, ref)
	}
	for _, line := range byLine {
		for _, prefix := range line {
			for _, next := range line {
				if prefix != next && prefix.location.EndColumn+len("::") == next.location.StartColumn {
					prefix.inner = true
				}
			}
		}
	}
	for _, ref := range refs {
		if ref.inner {
			continue
		}
		c.emitReference(ref, c.pathPrefixes(byLine, ref))
	}
}

// pathPrefixes walks back from a leaf over the adjacent segments on its line
// and returns their written names, outermost first.
func (c *collector) pathPrefixes(byLine map[string][]*constantReference, leaf *constantReference) []string {
	line := byLine[leaf.location.URI+"\x00"+strconv.Itoa(leaf.location.StartLine)]
	var prefixes []string
	current := leaf
	for {
		var previous *constantReference
		for _, candidate := range line {
			if candidate.location.EndColumn+len("::") == current.location.StartColumn {
				previous = candidate
			}
		}
		if previous == nil {
			break
		}
		prefixes = append([]string{c.writtenName(previous)}, prefixes...)
		current = previous
	}
	return prefixes
}

func (c *collector) writtenName(ref *constantReference) string {
	if ref.resolved {
		return plainName(c.g.declarationName(ref.target.id))
	}
	return c.g.constantReferenceName(ref.id)
}

func (c *collector) emitReference(ref *constantReference, prefixes []string) {
	referrer := ""
	if ref.doc != 0 {
		referrer = c.enclosingName(ref.doc, ref.location)
	}
	if referrer == "" {
		referrer = c.relative(ref.location.URI)
	}
	written := strings.Join(append(append([]string{}, prefixes...), c.writtenName(ref)), "::")
	if !ref.resolved {
		c.unresolved++
		c.facts = append(c.facts, c.missFact("rubydex-ref: "+referrer+" -> "+written, ref.location, "unresolved", prefixes))
		return
	}
	targetFull := c.g.declarationName(ref.target.id)
	if singleton(targetFull) {
		return
	}
	if ref.target.kind == declarationAlias {
		c.aliasedReferences++
		c.facts = append(c.facts, c.missFact("rubydex-ref: "+referrer+" -> "+written, ref.location, "alias", prefixes))
		return
	}
	targetName := plainName(targetFull)
	props := map[string]any{"declared_in_workspace": c.declaredInWorkspace(ref.target.id)}
	if len(prefixes) > 0 {
		props["path_prefixes"] = prefixes
	}
	files, located := c.definingFiles(ref.target.id)
	switch {
	case !located:
		c.undefinedTargets++
		props["resolution_cause"] = "no_definition"
	case len(files) == 1:
		props["target_file"] = files[0]
	}
	c.facts = append(c.facts, c.fact("rubydex-ref: "+referrer+" -> "+targetName, ref.location, "resolved",
		facts.Relation{Kind: "depends_on", Target: targetName}, props))
}

// definingFiles lists the workspace files a declaration is defined in, so a
// consumer can tell which of a reopened name's files a dependency lands on.
// The second result is false when the engine reports no location at all.
func (c *collector) definingFiles(declaration uint64) ([]string, bool) {
	seen := map[string]bool{}
	var files []string
	located := false
	for _, definition := range c.g.declarationDefinitions(declaration) {
		loc, ok := c.g.definitionLocation(definition.id)
		if !ok {
			continue
		}
		located = true
		if !c.inWorkspace(loc.URI) {
			continue
		}
		if rel := c.relative(loc.URI); !seen[rel] {
			seen[rel] = true
			files = append(files, rel)
		}
	}
	sort.Strings(files)
	return files, located
}

// missFact is a reference that resolved to nothing a rule can walk to. It is
// a dependency fact with no relation, so the miss is counted where it
// happened and named by its cause rather than dropped.
func (c *collector) missFact(name string, location Location, cause string, prefixes []string) facts.Fact {
	props := map[string]any{"resolution_level": "name-only", "resolution_cause": cause}
	if len(prefixes) > 0 {
		props["path_prefixes"] = prefixes
	}
	return facts.Fact{
		Kind:  facts.KindDependency,
		Name:  name,
		File:  c.relative(location.URI),
		Line:  location.StartLine,
		Props: props,
	}
}

func (c *collector) fact(name string, location Location, level string, relation facts.Relation, extra map[string]any) facts.Fact {
	props := map[string]any{"resolution_level": level}
	for k, v := range extra {
		props[k] = v
	}
	return facts.Fact{
		Kind:      facts.KindDependency,
		Name:      name,
		File:      c.relative(location.URI),
		Line:      location.StartLine,
		Props:     props,
		Relations: []facts.Relation{relation},
	}
}

// enclosingName is the innermost class, module or method whose span holds the
// line, named the way the graph names it, or empty at the top level.
func (c *collector) enclosingName(doc uint64, location Location) string {
	spans, cached := c.documentCache[doc]
	if !cached {
		for _, definition := range c.g.definitions(doc) {
			if definition.kind != definitionClass && definition.kind != definitionModule && definition.kind != definitionMethod {
				continue
			}
			declaration, ok := c.g.definitionDeclaration(definition.id)
			if !ok {
				continue
			}
			span := span{name: plainName(c.g.declarationName(declaration.id))}
			if loc, ok := c.g.definitionLocation(definition.id); ok {
				span.startLine, span.endLine, span.startColumn = loc.StartLine, loc.EndLine, loc.StartColumn
			}
			spans = append(spans, span)
		}
		c.documentCache[doc] = spans
	}
	var innermost *span
	for i := range spans {
		s := &spans[i]
		if s.startLine > location.StartLine || s.endLine < location.StartLine {
			continue
		}
		if innermost == nil || s.startLine > innermost.startLine || (s.startLine == innermost.startLine && s.startColumn > innermost.startColumn) {
			innermost = s
		}
	}
	if innermost == nil {
		return ""
	}
	return innermost.name
}

func (c *collector) declaredInWorkspace(declaration uint64) bool {
	for _, definition := range c.g.declarationDefinitions(declaration) {
		if loc, ok := c.g.definitionLocation(definition.id); ok && c.inWorkspace(loc.URI) {
			return true
		}
	}
	return false
}

func (c *collector) builtIn(declaration uint64) bool {
	for _, definition := range c.g.declarationDefinitions(declaration) {
		if loc, ok := c.g.definitionLocation(definition.id); ok && loc.URI == builtInURI {
			return true
		}
	}
	return false
}

func (c *collector) census(filesSeen, diagnostics int) facts.ProviderCensus {
	var causes []facts.CensusCause
	add := func(cause string, count int) {
		if count > 0 {
			causes = append(causes, facts.CensusCause{Cause: cause, Count: count})
		}
	}
	add("unresolved constant reference", c.unresolved)
	add("reference resolves to a constant alias", c.aliasedReferences)
	add("resolved declaration has no definition location", c.undefinedTargets)
	add("receiver is the lexical enclosing class", c.enclosingReceivers)
	add("receiver resolves to no constant", c.untypedReceivers)
	add("declaration is a constant alias, not a class", c.aliasedDeclarations)
	add("rubydex diagnostic", diagnostics)
	if c.bundleAbsent {
		add("bundle not on PATH or not readable: workspace indexed without its gems", 1)
	}
	skipped := 0
	for _, cause := range causes {
		skipped += cause.Count
	}
	return facts.ProviderCensus{
		FilesSeen:          filesSeen,
		DeclarationsParsed: len(c.facts),
		ConstructsSkipped:  skipped,
		SkipCauses:         causes,
	}
}

func sameLexicalOwner(enclosing, receiver string) bool {
	owner := enclosing
	if i := strings.LastIndexAny(enclosing, "#."); i >= 0 {
		owner = enclosing[:i]
	}
	return owner == receiver
}

func singleton(name string) bool {
	return strings.HasSuffix(name, ">")
}

func plainName(name string) string {
	if i := strings.LastIndex(name, "::<"); i >= 0 && strings.HasSuffix(name, ">") {
		name = name[:i]
	}
	return strings.TrimSuffix(name, "()")
}
