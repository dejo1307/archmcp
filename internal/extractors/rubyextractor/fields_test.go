package rubyextractor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fieldsFor(t *testing.T, src string) map[string]map[string]string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "app", "services")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(path, "thing.rb")
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	facts, err := New().Extract(context.Background(), dir, []string{"app/services/thing.rb"})
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]map[string]string{}
	for _, f := range facts {
		reads := propList(f.Props["fields_read"])
		writes := propList(f.Props["fields_written"])
		if len(reads)+len(writes) == 0 {
			continue
		}
		modes := map[string]string{}
		for _, r := range reads {
			modes[r] = "read"
		}
		for _, w := range writes {
			if modes[w] == "read" {
				modes[w] = "both"
			} else {
				modes[w] = "write"
			}
		}
		out[f.Name] = modes
	}
	return out
}

func propList(v any) []string {
	switch value := v.(type) {
	case []string:
		return value
	case []any:
		var out []string
		for _, item := range value {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func TestReadsAndWritesAreDistinguished(t *testing.T) {
	got := fieldsFor(t, `
class Thing
  def call
    @client = build
    @client.post(@payload)
  end
end
`)
	modes := got["Thing#call"]
	if modes["@client"] != "both" {
		t.Fatalf("@client should be both, got %q (all: %v)", modes["@client"], got)
	}
	if modes["@payload"] != "read" {
		t.Fatalf("@payload should be read, got %q", modes["@payload"])
	}
}

// The extract-class question is "which methods actually use @client", so a
// method touching nothing must not appear as touching something.
func TestAMethodTouchingNoFieldsRecordsNone(t *testing.T) {
	got := fieldsFor(t, `
class Thing
  def pure(a, b)
    a + b
  end
end
`)
	if _, ok := got["Thing#pure"]; ok {
		t.Fatalf("a method with no field access reported one: %v", got)
	}
}

// Class variables are a separate namespace and must not merge into instance
// state — the backlog is explicit, and merging them overstates cohesion.
func TestClassVariablesAreNotInstanceFields(t *testing.T) {
	got := fieldsFor(t, `
class Thing
  def call
    @@registry ||= {}
    @local = 1
  end
end
`)
	modes := got["Thing#call"]
	for name := range modes {
		if strings.HasPrefix(name, "@@") {
			t.Fatalf("a class variable was recorded as an instance field: %v", modes)
		}
	}
	if modes["@local"] == "" {
		t.Fatalf("the instance variable was dropped alongside it: %v", modes)
	}
}
