// Package rubydex is the built-in provider that reads a Ruby workspace
// through Rubydex's engine, the shared Ruby analysis library, loaded at run
// time from the prebuilt C-ABI shared library each platform gem ships. No
// Ruby interpreter runs: the engine indexes the workspace and the bundle's
// gem paths, resolves constants and ancestry, and the provider walks the
// graph through the library's iterators to emit the same facts the Ruby
// reference script under examples/providers/ruby/rubydex emits.
package rubydex

import (
	"runtime"
	"unsafe"
)

type definitionKind uint32

const (
	definitionClass     definitionKind = 0
	definitionSingleton definitionKind = 1
	definitionModule    definitionKind = 2
	definitionMethod    definitionKind = 7
)

type declarationKind uint32

const (
	declarationClass  declarationKind = 0
	declarationModule declarationKind = 1
)

type cDefinition struct {
	id   uint64
	kind definitionKind
	_    uint32
}

type cDeclaration struct {
	id   uint64
	kind declarationKind
	_    uint32
}

type cConstantReference struct {
	id uint64
	_  uint64
}

type cMethodReference struct {
	id uint64
}

type cLocation struct {
	uri         *byte
	startLine   uint32
	endLine     uint32
	startColumn uint32
	_           uint32
}

type cDiagnosticArray struct {
	_   unsafe.Pointer
	len uintptr
}

// Location is a span the engine measured, lines and columns one-based.
type Location struct {
	URI         string
	StartLine   int
	EndLine     int
	StartColumn int
}

// Library is the set of engine calls the provider needs, bound to one loaded
// shared library. Everything the engine hands out is owned by the caller and
// released through the engine's own free functions after copying: the
// library carries its own allocator, so the C library's free is not its.
type Library struct {
	graphNew           func() uintptr
	graphFree          func(uintptr)
	indexAll           func(uintptr, **byte, uintptr, *uintptr) **byte
	resolve            func(uintptr)
	diagnostics        func(uintptr) *cDiagnosticArray
	diagnosticsFree    func(*cDiagnosticArray)
	docsIterNew        func(uintptr) uintptr
	docsIterNext       func(uintptr, *uint64) bool
	docsIterFree       func(uintptr)
	docURI             func(uintptr, uint64) *byte
	docDefsIterNew     func(uintptr, uint64) uintptr
	docMethodRefsNew   func(uintptr, uint64) uintptr
	defsIterNext       func(uintptr, *cDefinition) bool
	defsIterFree       func(uintptr)
	defDecl            func(uintptr, uint64) *cDeclaration
	defLocation        func(uintptr, uint64) *cLocation
	locationFree       func(*cLocation)
	declName           func(uintptr, uint64) *byte
	declDefsIterNew    func(uintptr, uint64) uintptr
	declAncestors      func(uintptr, uint64) uintptr
	declsIterNext      func(uintptr, *cDeclaration) bool
	declsIterFree      func(uintptr)
	constRefsIterNew   func(uintptr) uintptr
	constRefsIterNext  func(uintptr, *cConstantReference) bool
	constRefsIterFree  func(uintptr)
	constRefLocation   func(uintptr, uint64) *cLocation
	constRefDocument   func(uintptr, uint64) *uint64
	constRefResolved   func(uintptr, uint64) *cDeclaration
	methodRefsIterNext func(uintptr, *cMethodReference) bool
	methodRefsIterFree func(uintptr)
	methodRefName      func(uintptr, uint64) *byte
	methodRefLocation  func(uintptr, uint64) *cLocation
	methodRefReceiver  func(uintptr, uint64) *cDeclaration
	freeString         func(*byte)
	freeStringArray    func(**byte, uintptr)
	freeDeclaration    func(*cDeclaration)
	freeU64            func(*uint64)
}

// ownedString copies an engine-allocated C string and releases it.
func (l *Library) ownedString(p *byte) string {
	if p == nil {
		return ""
	}
	n := 0
	for *(*byte)(unsafe.Add(unsafe.Pointer(p), n)) != 0 {
		n++
	}
	s := string(unsafe.Slice(p, n))
	l.freeString(p)
	return s
}

func (l *Library) location(loc *cLocation) (Location, bool) {
	if loc == nil {
		return Location{}, false
	}
	out := Location{
		URI:         l.borrowedString(loc.uri),
		StartLine:   int(loc.startLine) + 1,
		EndLine:     int(loc.endLine) + 1,
		StartColumn: int(loc.startColumn) + 1,
	}
	l.locationFree(loc)
	return out, true
}

func (l *Library) borrowedString(p *byte) string {
	if p == nil {
		return ""
	}
	n := 0
	for *(*byte)(unsafe.Add(unsafe.Pointer(p), n)) != 0 {
		n++
	}
	return string(unsafe.Slice(p, n))
}

type graph struct {
	lib    *Library
	handle uintptr
}

func (l *Library) newGraph() *graph {
	return &graph{lib: l, handle: l.graphNew()}
}

func (g *graph) close() {
	g.lib.graphFree(g.handle)
}

// index hands the engine every path at once, the way the gem does, and
// returns how many of them it could not read.
func (g *graph) index(paths []string) int {
	if len(paths) == 0 {
		return 0
	}
	buffers := make([][]byte, len(paths))
	pointers := make([]*byte, len(paths))
	for i, p := range paths {
		buffers[i] = append([]byte(p), 0)
		pointers[i] = &buffers[i][0]
	}
	var errorCount uintptr
	errs := g.lib.indexAll(g.handle, &pointers[0], uintptr(len(pointers)), &errorCount)
	if errs != nil {
		g.lib.freeStringArray(errs, errorCount)
	}
	runtime.KeepAlive(buffers)
	return int(errorCount)
}

func (g *graph) resolve() {
	g.lib.resolve(g.handle)
}

func (g *graph) diagnosticCount() int {
	array := g.lib.diagnostics(g.handle)
	if array == nil {
		return 0
	}
	n := int(array.len)
	g.lib.diagnosticsFree(array)
	return n
}

func (g *graph) documents() []uint64 {
	iter := g.lib.docsIterNew(g.handle)
	defer g.lib.docsIterFree(iter)
	var out []uint64
	var id uint64
	for g.lib.docsIterNext(iter, &id) {
		out = append(out, id)
	}
	return out
}

func (g *graph) documentURI(id uint64) string {
	return g.lib.ownedString(g.lib.docURI(g.handle, id))
}

func (g *graph) definitions(doc uint64) []cDefinition {
	iter := g.lib.docDefsIterNew(g.handle, doc)
	defer g.lib.defsIterFree(iter)
	var out []cDefinition
	var d cDefinition
	for g.lib.defsIterNext(iter, &d) {
		out = append(out, d)
	}
	return out
}

func (g *graph) definitionDeclaration(id uint64) (cDeclaration, bool) {
	p := g.lib.defDecl(g.handle, id)
	if p == nil {
		return cDeclaration{}, false
	}
	decl := *p
	g.lib.freeDeclaration(p)
	return decl, true
}

func (g *graph) definitionLocation(id uint64) (Location, bool) {
	return g.lib.location(g.lib.defLocation(g.handle, id))
}

func (g *graph) declarationName(id uint64) string {
	return g.lib.ownedString(g.lib.declName(g.handle, id))
}

func (g *graph) declarationDefinitions(id uint64) []cDefinition {
	iter := g.lib.declDefsIterNew(g.handle, id)
	if iter == 0 {
		return nil
	}
	defer g.lib.defsIterFree(iter)
	var out []cDefinition
	var d cDefinition
	for g.lib.defsIterNext(iter, &d) {
		out = append(out, d)
	}
	return out
}

func (g *graph) ancestors(id uint64) []cDeclaration {
	iter := g.lib.declAncestors(g.handle, id)
	if iter == 0 {
		return nil
	}
	defer g.lib.declsIterFree(iter)
	var out []cDeclaration
	var d cDeclaration
	for g.lib.declsIterNext(iter, &d) {
		out = append(out, d)
	}
	return out
}

func (g *graph) constantReferences() []cConstantReference {
	iter := g.lib.constRefsIterNew(g.handle)
	defer g.lib.constRefsIterFree(iter)
	var out []cConstantReference
	var r cConstantReference
	for g.lib.constRefsIterNext(iter, &r) {
		out = append(out, r)
	}
	return out
}

func (g *graph) constantReferenceLocation(id uint64) (Location, bool) {
	return g.lib.location(g.lib.constRefLocation(g.handle, id))
}

func (g *graph) constantReferenceDocument(id uint64) (uint64, bool) {
	p := g.lib.constRefDocument(g.handle, id)
	if p == nil {
		return 0, false
	}
	doc := *p
	g.lib.freeU64(p)
	return doc, true
}

func (g *graph) resolvedConstantReference(id uint64) (cDeclaration, bool) {
	p := g.lib.constRefResolved(g.handle, id)
	if p == nil {
		return cDeclaration{}, false
	}
	decl := *p
	g.lib.freeDeclaration(p)
	return decl, true
}

func (g *graph) methodReferences(doc uint64) []cMethodReference {
	iter := g.lib.docMethodRefsNew(g.handle, doc)
	if iter == 0 {
		return nil
	}
	defer g.lib.methodRefsIterFree(iter)
	var out []cMethodReference
	var r cMethodReference
	for g.lib.methodRefsIterNext(iter, &r) {
		out = append(out, r)
	}
	return out
}

func (g *graph) methodReferenceName(id uint64) string {
	return g.lib.ownedString(g.lib.methodRefName(g.handle, id))
}

func (g *graph) methodReferenceLocation(id uint64) (Location, bool) {
	return g.lib.location(g.lib.methodRefLocation(g.handle, id))
}

func (g *graph) methodReferenceReceiver(id uint64) (cDeclaration, bool) {
	p := g.lib.methodRefReceiver(g.handle, id)
	if p == nil {
		return cDeclaration{}, false
	}
	decl := *p
	g.lib.freeDeclaration(p)
	return decl, true
}
