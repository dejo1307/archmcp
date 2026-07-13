package rustextractor

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// writeRustRepo writes files (rel path -> content) into a temp repo and
// returns the repo dir and the relative file list, mirroring the Kotlin
// extractor's writeKotlinRepo test helper.
func writeRustRepo(t *testing.T, files map[string]string) (string, []string) {
	t.Helper()
	dir := t.TempDir()
	var rel []string
	for name, content := range files {
		full := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		rel = append(rel, name)
	}
	return dir, rel
}

func TestDetect_RootCargoToml(t *testing.T) {
	repo, _ := writeRustRepo(t, map[string]string{
		"Cargo.toml": "[package]\nname = \"demo\"\n",
		"src/lib.rs": "pub fn hi() {}\n",
	})
	ok, err := New().Detect(repo)
	if err != nil || !ok {
		t.Errorf("Detect = %v, %v; want true, nil", ok, err)
	}
}

func TestDetect_SubdirectoryMonorepo(t *testing.T) {
	repo, _ := writeRustRepo(t, map[string]string{
		"backend/Cargo.toml": "[package]\nname = \"demo\"\n",
		"backend/src/lib.rs": "pub fn hi() {}\n",
	})
	ok, err := New().Detect(repo)
	if err != nil || !ok {
		t.Errorf("Detect = %v, %v; want true, nil", ok, err)
	}
}

func TestDetect_NoCargoToml(t *testing.T) {
	repo, _ := writeRustRepo(t, map[string]string{
		"README.md": "hello\n",
	})
	ok, err := New().Detect(repo)
	if err != nil || ok {
		t.Errorf("Detect = %v, %v; want false, nil", ok, err)
	}
}

func TestOwnsFile(t *testing.T) {
	e := New()
	if !e.OwnsFile("src/lib.rs") {
		t.Error("OwnsFile(src/lib.rs) = false, want true")
	}
	if e.OwnsFile("Cargo.toml") {
		t.Error("OwnsFile(Cargo.toml) = true, want false (manifest is shared config, not owned)")
	}
}

func TestParseCargoPackageName(t *testing.T) {
	data := "[package]\nname = \"dbt-common\"\nversion = \"0.1.0\"\n\n[dependencies]\nserde = \"1\"\n"
	if got := parseCargoPackageName(data); got != "dbt-common" {
		t.Errorf("parseCargoPackageName = %q, want dbt-common", got)
	}
}

// TestExtract_WorkspaceCrossCrateDependency is the end-to-end regression guard
// for a Cargo workspace: a `use` of another workspace crate must resolve as an
// internal dependency targeting that crate's own directory, module facts must
// carry their owning crate name, and impl blocks split across files must still
// attach their RelImplements edge to the type.
func TestExtract_WorkspaceCrossCrateDependency(t *testing.T) {
	repo, files := writeRustRepo(t, map[string]string{
		"Cargo.toml": "[workspace]\nmembers = [\"crates/dbt-common\", \"crates/dbt-parser\"]\n",
		"crates/dbt-common/Cargo.toml": "[package]\nname = \"dbt-common\"\n",
		"crates/dbt-common/src/lib.rs": `
pub struct Error;
`,
		"crates/dbt-parser/Cargo.toml": "[package]\nname = \"dbt-parser\"\n",
		"crates/dbt-parser/src/lib.rs": `
use dbt_common::Error;
use serde::Deserialize;

pub fn parse() -> Error {
    Error
}
`,
	})

	ff, err := New().Extract(context.Background(), repo, files)
	if err != nil {
		t.Fatalf("Extract error: %v", err)
	}

	dep, ok := findFact(ff, "crates/dbt-parser/src -> dbt_common::Error")
	if !ok {
		t.Fatalf("expected a dependency fact for dbt_common::Error, got %+v", findFactsByKind(ff, facts.KindDependency))
	}
	if dep.Props["source"] != "internal" {
		t.Errorf("cross-crate workspace dependency source = %v, want internal", dep.Props["source"])
	}
	if !hasRelation(dep, facts.RelImports, "crates/dbt-common/src") {
		t.Errorf("expected RelImports -> crates/dbt-common/src, got %+v", dep.Relations)
	}

	extDep, ok := findFact(ff, "crates/dbt-parser/src -> serde::Deserialize")
	if !ok {
		t.Fatalf("expected a dependency fact for serde::Deserialize")
	}
	if extDep.Props["source"] != "external" {
		t.Errorf("serde dependency source = %v, want external", extDep.Props["source"])
	}

	mod, ok := findFact(ff, "crates/dbt-parser/src")
	if !ok {
		t.Fatalf("expected a module fact for crates/dbt-parser/src")
	}
	if mod.Props["crate"] != "dbt_parser" {
		t.Errorf("module crate prop = %v, want dbt_parser", mod.Props["crate"])
	}
}

func TestExtract_ImplSplitAcrossFilesStillAttaches(t *testing.T) {
	repo, files := writeRustRepo(t, map[string]string{
		"Cargo.toml":     "[package]\nname = \"demo\"\n",
		"src/types.rs":   "pub struct Wrapper;\n",
		"src/display.rs": "impl std::fmt::Display for Wrapper {\n    fn fmt(&self) {}\n}\n",
	})
	ff, err := New().Extract(context.Background(), repo, files)
	if err != nil {
		t.Fatalf("Extract error: %v", err)
	}
	w, ok := findFact(ff, "src.Wrapper")
	if !ok {
		t.Fatalf("expected fact for src.Wrapper, got %+v", ff)
	}
	if !hasRelation(w, facts.RelImplements, "Display") {
		t.Errorf("expected RelImplements -> Display attached across files, got %+v", w.Relations)
	}
}

func TestExtract_UnprefixedUseOfSiblingFileSubmodule(t *testing.T) {
	repo, files := writeRustRepo(t, map[string]string{
		"Cargo.toml": "[package]\nname = \"demo\"\n",
		"src/mod.rs": `
pub mod writer;
pub use writer::spawn_writer;

pub fn start() {
    spawn_writer();
}
`,
		"src/writer.rs": "pub fn spawn_writer() {}\n",
	})
	ff, err := New().Extract(context.Background(), repo, files)
	if err != nil {
		t.Fatalf("Extract error: %v", err)
	}
	start, ok := findFact(ff, "src.start")
	if !ok {
		t.Fatalf("expected fact for src.start, got %+v", ff)
	}
	if !hasRelation(start, facts.RelCalls, "src.spawn_writer") {
		t.Errorf("expected RelCalls -> src.spawn_writer, got %+v", start.Relations)
	}
	dep, ok := findFact(ff, "src -> writer::spawn_writer")
	if !ok {
		t.Fatalf("expected a dependency fact for writer::spawn_writer, got %+v", findFactsByKind(ff, facts.KindDependency))
	}
	if dep.Props["source"] != "internal" {
		t.Errorf("sibling-file submodule source = %v, want internal", dep.Props["source"])
	}
}
