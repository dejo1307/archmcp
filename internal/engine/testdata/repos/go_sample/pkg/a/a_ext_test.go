package a_test

import (
	"testing"

	"example.com/gosample/pkg/a"
)

// The external-test-package idiom: `package a_test` imports the package under test,
// so the reference is qualified by an import alias and must resolve through
// buildFileImports to the same canonical name ("pkg/a.Gamma"). (v100)
func TestGammaFromExternalPackage(t *testing.T) {
	a.Gamma()
	t.Log("called")
}
