//go:build !windows

package rubydex

import (
	"fmt"

	"github.com/ebitengine/purego"
)

// Open loads the engine library at path and binds the calls the provider uses.
func Open(path string) (*Library, error) {
	handle, err := purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		return nil, fmt.Errorf("loading %s: %w", path, err)
	}
	lib := &Library{}
	bindings := []struct {
		target any
		symbol string
	}{
		{&lib.graphNew, "rdx_graph_new"},
		{&lib.graphFree, "rdx_graph_free"},
		{&lib.indexAll, "rdx_index_all"},
		{&lib.resolve, "rdx_graph_resolve"},
		{&lib.diagnostics, "rdx_graph_diagnostics"},
		{&lib.diagnosticsFree, "rdx_diagnostics_free"},
		{&lib.docsIterNew, "rdx_graph_documents_iter_new"},
		{&lib.docsIterNext, "rdx_graph_documents_iter_next"},
		{&lib.docsIterFree, "rdx_graph_documents_iter_free"},
		{&lib.docURI, "rdx_document_uri"},
		{&lib.docDefsIterNew, "rdx_document_definitions_iter_new"},
		{&lib.docMethodRefsNew, "rdx_document_method_references_iter_new"},
		{&lib.defsIterNext, "rdx_definitions_iter_next"},
		{&lib.defsIterFree, "rdx_definitions_iter_free"},
		{&lib.defDecl, "rdx_definition_declaration"},
		{&lib.defLocation, "rdx_definition_location"},
		{&lib.locationFree, "rdx_location_free"},
		{&lib.declName, "rdx_declaration_name"},
		{&lib.declDefsIterNew, "rdx_declaration_definitions_iter_new"},
		{&lib.declAncestors, "rdx_declaration_ancestors"},
		{&lib.declsIterNext, "rdx_graph_declarations_iter_next"},
		{&lib.declsIterFree, "rdx_graph_declarations_iter_free"},
		{&lib.constRefsIterNew, "rdx_graph_constant_references_iter_new"},
		{&lib.constRefsIterNext, "rdx_constant_references_iter_next"},
		{&lib.constRefsIterFree, "rdx_constant_references_iter_free"},
		{&lib.constRefLocation, "rdx_constant_reference_location"},
		{&lib.constRefDocument, "rdx_constant_reference_document"},
		{&lib.constRefResolved, "rdx_resolved_constant_reference_declaration"},
		{&lib.constRefName, "rdx_constant_reference_name"},
		{&lib.methodRefsIterNext, "rdx_method_references_iter_next"},
		{&lib.methodRefsIterFree, "rdx_method_references_iter_free"},
		{&lib.methodRefName, "rdx_method_reference_name"},
		{&lib.methodRefLocation, "rdx_method_reference_location"},
		{&lib.methodRefReceiver, "rdx_method_reference_receiver_declaration"},
		{&lib.freeString, "free_c_string"},
		{&lib.freeStringArray, "free_c_string_array"},
		{&lib.freeDeclaration, "free_c_declaration"},
		{&lib.freeU64, "free_u64"},
	}
	for _, b := range bindings {
		if _, err := purego.Dlsym(handle, b.symbol); err != nil {
			return nil, fmt.Errorf("%s does not export %s: the library is not the Rubydex build this enola was written against", path, b.symbol)
		}
		purego.RegisterLibFunc(b.target, handle, b.symbol)
	}
	return lib, nil
}
