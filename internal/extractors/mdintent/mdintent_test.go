package mdintent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
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
}

func TestMDIntent_PlainPagesAreIgnored(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.md"), []byte("---\ntitle: x\n---\nprose enola_intent: in body only\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ff, err := New().Extract(context.Background(), dir, []string{"notes.md"})
	if err != nil || len(ff) != 0 {
		t.Fatalf("a page without the frontmatter key must compile nothing: %v %v", ff, err)
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
