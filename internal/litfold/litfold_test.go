package litfold

import "testing"

func TestLitfold_SingleAssignmentAndDupKill(t *testing.T) {
	a := NewAssignments()
	a.Add("url", "${config.HOST}/mcp")
	if v, ok := a.Resolve("url"); !ok || v != "${config.HOST}/mcp" {
		t.Fatalf("single assignment did not resolve: %q %v", v, ok)
	}
	a.Add("url", "/other")
	if _, ok := a.Resolve("url"); ok {
		t.Fatal("a name assigned twice must fold nothing")
	}
	a.Add("url", "/third")
	if _, ok := a.Resolve("url"); ok {
		t.Fatal("a killed name must stay killed")
	}
	if _, ok := a.Resolve("absent"); ok {
		t.Fatal("an unassigned name must not resolve")
	}
}

func TestLitfold_WrapperLiteralPath(t *testing.T) {
	if p, ok := WrapperLiteralPath(`build_url("/pageview")`); !ok || p != "/pageview" {
		t.Fatalf("wrapper path = %q %v, want /pageview", p, ok)
	}
	if _, ok := WrapperLiteralPath(`t("greeting.hello")`); ok {
		t.Fatal("a non-path wrapper literal must derive nothing")
	}
	if _, ok := WrapperLiteralPath(`build_url(path)`); ok {
		t.Fatal("a non-literal wrapper argument must derive nothing")
	}
	if !TemplateTailPath("${config.HOST}/mcp") {
		t.Fatal("interpolation-headed template with /-rooted tail must pass")
	}
	if TemplateTailPath("${key}") || TemplateTailPath("literal/${x}") {
		t.Fatal("non-tail shapes must not pass")
	}
}

func TestLitfold_NilStoreIsEmpty(t *testing.T) {
	var a *Assignments
	a.Add("name", "/x")
	if _, ok := a.Resolve("name"); ok {
		t.Fatal("a nil store must resolve nothing")
	}
}
