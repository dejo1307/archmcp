package mdintent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
	"github.com/enola-labs/enola/pkg/plugin"
)

const page = `---
title: Estate seams
kind: initiative
enola_intent:
  consumes:
    - {repo: mobile, target: backend, via: graphql}
  layers:
    - repo: backend
      order:
        - {name: handlers, paths: ["app/handlers/**"]}
        - {name: domain, paths: ["app/domain/**"]}
  claims:
    - {metric: fact-count, repo: backend, kind: route, name_prefix: "/api", value: 12}
---

# Estate seams

Prose body — never parsed, may even say enola_intent: without effect.
`

func TestMDIntent_PageCompilesToOwnedFacts(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "wiki", "enola", "prds")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "seams.md"), []byte(page), 0o644); err != nil {
		t.Fatal(err)
	}
	e := New()
	if ok, _ := e.Detect(dir); !ok {
		t.Fatal("a wiki tree carrying enola_intent must detect")
	}
	ff, err := e.Extract(context.Background(), dir, []string{"wiki/enola/prds/seams.md"})
	if err != nil {
		t.Fatal(err)
	}
	byKind := map[string]int{}
	for _, f := range ff {
		if f.Kind != facts.KindIntent {
			t.Fatalf("unexpected kind %s", f.Kind)
		}
		byKind[f.Props["intent_kind"].(string)]++
		if f.File != "wiki/enola/prds/seams.md" {
			t.Fatalf("provenance must be the page, got %s", f.File)
		}
	}
	if byKind["consumes"] != 1 || byKind["layer"] != 2 || byKind["claim"] != 1 {
		t.Fatalf("compiled %v, want 1 consumes + 2 layers + 1 claim", byKind)
	}
	for _, f := range ff {
		if f.Props["intent_kind"] == "consumes" && f.Props["intent_owner"] != "mobile" {
			t.Fatalf("owner must be the declared repo, got %v", f.Props["intent_owner"])
		}
	}
}

func TestMDIntent_InvalidBlockFailsExtraction(t *testing.T) {
	dir := t.TempDir()
	bad := "---\nenola_intent:\n  consumes:\n    - {repo: a, target: b, via: rest}\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(dir, "page.md"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := New().Extract(context.Background(), dir, []string{"page.md"})
	if err == nil || !strings.Contains(err.Error(), "graphql") {
		t.Fatalf("an invalid via must fail with the allowed set named, got %v", err)
	}
	// Fatal, so the engine fails the snapshot instead of recording a parse error
	// and publishing a run that quietly lost every verdict the block carried.
	var fatal *plugin.FatalError
	if !errors.As(err, &fatal) {
		t.Fatalf("an invalid declaration must be fatal, got %T: %v", err, err)
	}
}

// A page without the key is a document: a symbol named by its path, a symbol
// per heading named the way a fragment names it, and a names relation per
// link that resolves on disk from the section that carries it. Fenced code
// is not prose, a URL is external, a fragment is nothing, and a link to a
// file that is not there is counted rather than followed.
func TestMDIntent_PlainPagesAreDocuments(t *testing.T) {
	dir := t.TempDir()
	for _, p := range []string{"app/models/order.rb", "docs/guide.md", "lib/util.rb"} {
		if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, p), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	notes := "---\ntitle: x\n---\n# Orders\n\nSee [the model](../app/models/order.rb) and `lib/util.rb`.\n\n## How it works\n\nRead [the guide](guide.md#intro), [upstream](https://example.com/x.md), [top](#orders) and [gone](missing.md).\n\n```ruby\n# not a heading\n[link](../app/models/order.rb)\n```\n\n## How it works\n\nA path that escapes the tree: [out](../../etc/passwd).\n"
	if err := os.WriteFile(filepath.Join(dir, "docs", "notes.md"), []byte(notes), 0o644); err != nil {
		t.Fatal(err)
	}
	if ok, _ := New().Detect(dir); !ok {
		t.Fatal("a tree with markdown must detect")
	}
	ff, err := New().Extract(context.Background(), dir, []string{"docs/notes.md"})
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]facts.Fact{}
	for _, f := range ff {
		byName[f.Name] = f
	}
	doc := byName["docs/notes.md"]
	if doc.Kind != facts.KindSymbol || doc.Props["symbol_kind"] != SymbolDocument || doc.Props["title"] != "Orders" {
		t.Fatalf("document fact: %+v", doc)
	}
	names := func(f facts.Fact, kind string) []string {
		var out []string
		for _, r := range f.Relations {
			if r.Kind == kind {
				out = append(out, r.Target)
			}
		}
		return out
	}
	if got := names(doc, facts.RelDeclares); strings.Join(got, ",") != "docs,docs/notes.md#orders,docs/notes.md#how-it-works,docs/notes.md#how-it-works-1" {
		t.Fatalf("document declares %v", got)
	}
	if got := names(byName["docs/notes.md#orders"], facts.RelNames); strings.Join(got, ",") != "app/models/order.rb,lib/util.rb" {
		t.Fatalf("section links %v", got)
	}
	if got := names(byName["docs/notes.md#how-it-works"], facts.RelNames); strings.Join(got, ",") != "docs/guide.md" {
		t.Fatalf("second section links %v (fenced, external, fragment and missing must not resolve)", got)
	}
	if sec := byName["docs/notes.md#how-it-works-1"]; sec.Props["level"] != 2 || sec.Line != 17 {
		t.Fatalf("section fact: %+v", sec)
	}
	coverage := byName["mdintent:links"]
	if coverage.Kind != facts.KindExtraction || coverage.Props["unresolved_macros"] != "external=1,missing=1,outside-repo=1" {
		t.Fatalf("link coverage: %+v", coverage)
	}
	if entries := coverage.Props["edge_coverage"].([]map[string]any); entries[0]["resolved"] != 3 {
		t.Fatalf("resolved links: %v", entries)
	}
}

const knowledgePage = `---
title: Choose the queue
kind: decision
enola_intent:
  page:
    type: decision
    status: living
    scope: [backend]
    affects: [mobile]
    relations:
      - {rel: part-of, to: wiki/backend/epics/messaging.md}
      - {rel: supersedes, to: wiki/backend/adrs/old-queue.md}
---

Prose body.
`

func TestMDIntent_PageDeclCompilesKnowledgeNode(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "wiki", "backend", "adrs")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "queue.md"), []byte(knowledgePage), 0o644); err != nil {
		t.Fatal(err)
	}
	ff, err := New().Extract(context.Background(), dir, []string{"wiki/backend/adrs/queue.md"})
	if err != nil {
		t.Fatal(err)
	}
	byKind := map[string]int{}
	for _, f := range ff {
		byKind[f.Props["intent_kind"].(string)]++
		if f.File != "wiki/backend/adrs/queue.md" {
			t.Fatalf("provenance must be the page, got %s", f.File)
		}
	}
	if byKind["page"] != 1 || byKind["relation"] != 2 {
		t.Fatalf("compiled %v, want 1 page node + 2 relation edges", byKind)
	}
	for _, f := range ff {
		if f.Props["intent_kind"] == "page" {
			if f.Props["page_type"] != "decision" || f.Props["status"] != "living" {
				t.Fatalf("page node must carry type and status, got %v", f.Props)
			}
		}
	}
}

func TestMDIntent_InvalidRelFailsWithVocabulary(t *testing.T) {
	dir := t.TempDir()
	bad := "---\nenola_intent:\n  page:\n    type: decision\n    relations:\n      - {rel: replaces, to: wiki/a.md}\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(dir, "page.md"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := New().Extract(context.Background(), dir, []string{"page.md"})
	if err == nil || !strings.Contains(err.Error(), "supersedes") {
		t.Fatalf("an unknown rel must fail with the vocabulary named, got %v", err)
	}
}

// TestMDIntent_OriginChannelsCompileAndValidate pins the provenance channel:
// a page declaring origin compiles it onto the page node's props, and a name
// outside the closed vocabulary fails with the allowed set named.
func TestMDIntent_OriginChannelsCompileAndValidate(t *testing.T) {
	dir := t.TempDir()
	good := "---\nenola_intent:\n  page:\n    type: decision\n    origin: [slack, langfuse]\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(dir, "d.md"), []byte(good), 0o644); err != nil {
		t.Fatal(err)
	}
	ff, err := New().Extract(context.Background(), dir, []string{"d.md"})
	if err != nil || len(ff) != 1 {
		t.Fatalf("extract = %v, %v", ff, err)
	}
	origin, _ := ff[0].Props["origin"].([]string)
	if len(origin) != 2 || origin[0] != "slack" || origin[1] != "langfuse" {
		t.Fatalf("origin = %v, want [slack langfuse]", ff[0].Props["origin"])
	}
	bad := "---\nenola_intent:\n  page:\n    type: decision\n    origin: [jira]\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(dir, "b.md"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = New().Extract(context.Background(), dir, []string{"b.md"})
	if err == nil || !strings.Contains(err.Error(), "langfuse") {
		t.Fatalf("an unknown channel must fail with the vocabulary named, got %v", err)
	}
}

// TestMDIntent_AnchorsCompileToOwnedFacts pins the page-to-code join: a page
// declaring anchors compiles one anchor fact per entry, owned by the anchored
// repo with the page as provenance, and an invalid anchor fails with the
// shape named.
func TestMDIntent_AnchorsCompileToOwnedFacts(t *testing.T) {
	dir := t.TempDir()
	good := "---\nenola_intent:\n  page:\n    type: decision\n    anchors:\n      - {repo: backend, path: app/services/formatter.rb}\n      - {repo: backend, path: app/jobs}\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(dir, "d.md"), []byte(good), 0o644); err != nil {
		t.Fatal(err)
	}
	ff, err := New().Extract(context.Background(), dir, []string{"d.md"})
	if err != nil {
		t.Fatal(err)
	}
	var anchors []facts.Fact
	for _, f := range ff {
		if f.Props["intent_kind"] == "anchor" {
			anchors = append(anchors, f)
		}
	}
	if len(anchors) != 2 {
		t.Fatalf("compiled %d anchor facts, want 2", len(anchors))
	}
	for _, f := range anchors {
		if f.Props["intent_owner"] != "backend" || f.File != "d.md" {
			t.Fatalf("anchor must be owned by the anchored repo with the page as provenance, got %v @ %s", f.Props, f.File)
		}
	}
	bad := "---\nenola_intent:\n  page:\n    type: decision\n    anchors:\n      - {repo: backend, path: /etc/passwd}\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(dir, "b.md"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = New().Extract(context.Background(), dir, []string{"b.md"})
	if err == nil || !strings.Contains(err.Error(), "repo-relative") {
		t.Fatalf("an absolute anchor path must fail as not repo-relative, got %v", err)
	}
}
