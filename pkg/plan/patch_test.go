package plan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func mustParse(t *testing.T, diff string) []PatchFile {
	t.Helper()
	files, err := ParsePatch([]byte(diff))
	if err != nil {
		t.Fatalf("ParsePatch: %v", err)
	}
	return files
}

func TestParsePatch_ModifyCreateDelete(t *testing.T) {
	diff := "--- a/pkg/a.go\n" +
		"+++ b/pkg/a.go\n" +
		"@@ -1,3 +1,4 @@\n" +
		" package a\n" +
		"+\n" +
		" func A() {}\n" +
		" func B() {}\n" +
		"--- /dev/null\n" +
		"+++ b/pkg/new.go\n" +
		"@@ -0,0 +1,1 @@\n" +
		"+package a\n" +
		"--- a/pkg/gone.go\n" +
		"+++ /dev/null\n" +
		"@@ -1,1 +0,0 @@\n" +
		"-package a\n"
	files := mustParse(t, diff)
	if len(files) != 3 {
		t.Fatalf("parsed %d files, want 3", len(files))
	}
	wants := []struct{ path, op string }{
		{"pkg/a.go", OpModify},
		{"pkg/new.go", OpCreate},
		{"pkg/gone.go", OpDelete},
	}
	for i, want := range wants {
		if files[i].Path != want.path || files[i].Op != want.op {
			t.Errorf("files[%d] = %s (%s), want %s (%s)", i, files[i].Path, files[i].Op, want.path, want.op)
		}
	}
}

func TestParsePatch_NamedErrors(t *testing.T) {
	cases := []struct {
		name, diff, wantErr string
	}{
		{"empty", "not a diff at all\n", "no file changes"},
		{"rename", "diff --git a/x b/y\nrename from x\nrename to y\n", "rename/copy is not supported"},
		{"binary", "Binary files a/x and b/x differ\n", "binary patches are not supported"},
		{"missing plus header", "--- a/x\n@@ -1 +1 @@\n", "without a +++ header"},
		{"short hunk", "--- a/x\n+++ b/x\n@@ -1,2 +1,2 @@\n x\n", "ends before its declared counts"},
		{"rename via headers", "--- a/x\n+++ b/y\n@@ -1 +1 @@\n-a\n+b\n", "renames x to y"},
		{"twice", "--- a/x\n+++ b/x\n@@ -1 +1 @@\n-a\n+b\n--- a/x\n+++ b/x\n@@ -1 +1 @@\n-b\n+c\n", "names x twice"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParsePatch([]byte(tc.diff))
			if err == nil {
				t.Fatalf("ParsePatch accepted %q", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not name %q", err, tc.wantErr)
			}
		})
	}
}

func TestValidatePatchScope(t *testing.T) {
	cases := []struct {
		path, wantErr string
	}{
		{"../outside.go", "escapes the repository root"},
		{"/etc/passwd", "absolute path"},
		{"a/../../evil.go", "escapes the repository root"},
		{".enola/facts.jsonl", "snapshot output directory"},
		{".enola", "snapshot output directory"},
	}
	for _, tc := range cases {
		err := ValidatePatchScope([]PatchFile{{Path: tc.path, Op: OpModify}}, ".enola")
		if err == nil {
			t.Errorf("ValidatePatchScope accepted %q", tc.path)
			continue
		}
		if !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("error for %q is %q, does not name %q", tc.path, err, tc.wantErr)
		}
	}
	if err := ValidatePatchScope([]PatchFile{{Path: "pkg/fine.go", Op: OpModify}}, ".enola"); err != nil {
		t.Errorf("ValidatePatchScope rejected a clean path: %v", err)
	}
}

func TestApplyPatch_Modify(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "pkg", "a.go"), "package a\n\nfunc A() {\n\treturn\n}\n")
	diff := "--- a/pkg/a.go\n" +
		"+++ b/pkg/a.go\n" +
		"@@ -1,5 +1,7 @@\n" +
		" package a\n" +
		" \n" +
		"+import \"fmt\"\n" +
		" func A() {\n" +
		"+\tfmt.Println(\"hi\")\n" +
		" \treturn\n" +
		" }\n"
	if err := applyPatch(root, mustParse(t, diff)); err != nil {
		t.Fatalf("applyPatch: %v", err)
	}
	got := readFile(t, filepath.Join(root, "pkg", "a.go"))
	want := "package a\n\nimport \"fmt\"\nfunc A() {\n\tfmt.Println(\"hi\")\n\treturn\n}\n"
	if got != want {
		t.Errorf("modified content = %q, want %q", got, want)
	}
}

func TestApplyPatch_CreateAndDelete(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "old.txt"), "gone\n")
	diff := "--- /dev/null\n" +
		"+++ b/fresh/new.txt\n" +
		"@@ -0,0 +1,2 @@\n" +
		"+hello\n" +
		"+world\n" +
		"--- a/old.txt\n" +
		"+++ /dev/null\n" +
		"@@ -1,1 +0,0 @@\n" +
		"-gone\n"
	if err := applyPatch(root, mustParse(t, diff)); err != nil {
		t.Fatalf("applyPatch: %v", err)
	}
	if got := readFile(t, filepath.Join(root, "fresh", "new.txt")); got != "hello\nworld\n" {
		t.Errorf("created content = %q", got)
	}
	if _, err := os.Stat(filepath.Join(root, "old.txt")); !os.IsNotExist(err) {
		t.Errorf("old.txt still exists")
	}
}

func TestApplyPatch_NoNewlineAtEOF(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "x.txt"), "one\ntwo")
	diff := "--- a/x.txt\n" +
		"+++ b/x.txt\n" +
		"@@ -1,2 +1,2 @@\n" +
		" one\n" +
		"-two\n" +
		"\\ No newline at end of file\n" +
		"+three\n" +
		"\\ No newline at end of file\n"
	if err := applyPatch(root, mustParse(t, diff)); err != nil {
		t.Fatalf("applyPatch: %v", err)
	}
	if got := readFile(t, filepath.Join(root, "x.txt")); got != "one\nthree" {
		t.Errorf("content = %q, want %q", got, "one\nthree")
	}
}

func TestApplyPatch_FailsClosed(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.txt"), "actual line\n")
	writeFile(t, filepath.Join(root, "exists.txt"), "here\n")

	cases := []struct {
		name, diff, wantErr string
	}{
		{
			"context mismatch",
			"--- a/a.txt\n+++ b/a.txt\n@@ -1,1 +1,1 @@\n-expected line\n+new line\n",
			`expected "expected line", found "actual line"`,
		},
		{
			"missing file",
			"--- a/absent.txt\n+++ b/absent.txt\n@@ -1,1 +1,1 @@\n-x\n+y\n",
			"cannot be read",
		},
		{
			"create over existing",
			"--- /dev/null\n+++ b/exists.txt\n@@ -0,0 +1,1 @@\n+here\n",
			"already exists",
		},
		{
			"delete mismatch",
			"--- a/a.txt\n+++ /dev/null\n@@ -1,1 +0,0 @@\n-different\n",
			"does not apply",
		},
		{
			"beyond eof",
			"--- a/a.txt\n+++ b/a.txt\n@@ -5,1 +5,1 @@\n-x\n+y\n",
			"does not apply",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := applyPatch(root, mustParse(t, tc.diff))
			if err == nil {
				t.Fatalf("applyPatch accepted %q", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not name %q", err, tc.wantErr)
			}
		})
	}
	if got := readFile(t, filepath.Join(root, "a.txt")); got != "actual line\n" {
		t.Errorf("a.txt was mutated by failed applies: %q", got)
	}
}

func TestCopyTreeSkips(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "keep.txt"), "kept\n")
	writeFile(t, filepath.Join(src, ".git", "HEAD"), "ref\n")
	writeFile(t, filepath.Join(src, ".enola", "facts.jsonl"), "{}\n")
	writeFile(t, filepath.Join(src, "nested", "deep.txt"), "deep\n")

	dst := filepath.Join(t.TempDir(), "copy")
	if err := copyTree(src, dst, map[string]bool{".enola": true}); err != nil {
		t.Fatalf("copyTree: %v", err)
	}
	if got := readFile(t, filepath.Join(dst, "keep.txt")); got != "kept\n" {
		t.Errorf("keep.txt = %q", got)
	}
	if got := readFile(t, filepath.Join(dst, "nested", "deep.txt")); got != "deep\n" {
		t.Errorf("nested/deep.txt = %q", got)
	}
	for _, skipped := range []string{".git", ".enola"} {
		if _, err := os.Stat(filepath.Join(dst, skipped)); !os.IsNotExist(err) {
			t.Errorf("%s was copied into the scratch tree", skipped)
		}
	}
}
