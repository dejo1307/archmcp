package providers

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/enola-labs/enola/internal/providers/rubydex"
)

func writeRubyWorkspace(t *testing.T) (string, []string, map[string]string) {
	t.Helper()
	root := t.TempDir()
	sources := map[string]string{
		"Gemfile":        "source \"https://rubygems.org\"\n",
		"Gemfile.lock":   "GEM\n  specs:\n\nPLATFORMS\n  ruby\n\nDEPENDENCIES\n",
		"app/ledger.rb":  "class Ledger\n  def self.record(x)\n    x\n  end\nend\n",
		"app/billing.rb": "class Billing\n  def charge(invoice)\n    Ledger.record(invoice)\n  end\nend\n",
		"app/helpers.js": "export const x = 1;\n",
	}
	var files []string
	hashes := map[string]string{}
	for rel, body := range sources {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		files = append(files, rel)
		hashes[rel] = digestOf(body)
	}
	return root, files, hashes
}

func rubydexInput(root string, files []string, hashes map[string]string, cache Cache) Input {
	return Input{Providers: []Provider{{Name: "rubydex"}}, RepoPath: root, Files: files, Hashes: hashes, Cache: cache}
}

func TestRubydexIndex_HitAndMissCauses(t *testing.T) {
	if _, installed := rubydex.Installed(); !installed {
		t.Skip("the Rubydex library is not installed here; the built-in provider cannot run")
	}
	root, files, hashes := writeRubyWorkspace(t)
	cache := newMemoryCache()

	cold, records := RunWith(context.Background(), rubydexInput(root, files, hashes, cache))
	if records[0].Skipped {
		t.Fatalf("cold run skipped: %s", records[0].Reason)
	}
	if r := records[0].Reuse; r == nil || r.Cache != "miss" || r.Miss != "cold" || r.Computed != len(cold) || r.Reused != 0 {
		t.Fatalf("cold run reuse = %+v, want a cold miss computing every fact", records[0].Reuse)
	}
	coldCensus := records[0].Census

	warm, records := RunWith(context.Background(), rubydexInput(root, files, hashes, cache))
	if r := records[0].Reuse; r == nil || r.Cache != "hit" || r.Reused != len(cold) || r.Computed != 0 {
		t.Fatalf("warm run reuse = %+v, want a hit reusing every fact", records[0].Reuse)
	}
	if len(warm) != len(cold) {
		t.Fatalf("warm run returned %d facts, cold %d", len(warm), len(cold))
	}
	for i := range cold {
		if cold[i].Name != warm[i].Name || cold[i].File != warm[i].File {
			t.Fatalf("fact %d differs between cold and warm: %+v vs %+v", i, cold[i], warm[i])
		}
	}
	if records[0].Census == nil || coldCensus == nil || records[0].Census.FilesSeen != coldCensus.FilesSeen || records[0].Census.ConstructsSkipped != coldCensus.ConstructsSkipped {
		t.Fatalf("a hit must report the census as recorded: %+v vs %+v", records[0].Census, coldCensus)
	}

	hashes["app/helpers.js"] = "changed"
	_, records = RunWith(context.Background(), rubydexInput(root, files, hashes, cache))
	if r := records[0].Reuse; r == nil || r.Cache != "hit" {
		t.Fatalf("a file the engine does not index must not move the key, got %+v", records[0].Reuse)
	}

	hashes["app/billing.rb"] = "changed"
	_, records = RunWith(context.Background(), rubydexInput(root, files, hashes, cache))
	if r := records[0].Reuse; r == nil || r.Cache != "miss" || r.Miss != "files" {
		t.Fatalf("a changed Ruby file must miss on files, got %+v", records[0].Reuse)
	}

	if err := os.WriteFile(filepath.Join(root, "Gemfile.lock"), []byte("GEM\n  specs:\n    rake (13.0.0)\n\nPLATFORMS\n  ruby\n\nDEPENDENCIES\n  rake\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, records = RunWith(context.Background(), rubydexInput(root, files, hashes, cache))
	if r := records[0].Reuse; r == nil || r.Cache != "miss" || r.Miss != "lockfile" {
		t.Fatalf("a changed lockfile must miss on lockfile, got %+v", records[0].Reuse)
	}

	_, records = RunWith(context.Background(), rubydexInput(root, files, hashes, nil))
	if records[0].Reuse != nil {
		t.Fatalf("without a cache the record carries no reuse state, got %+v", records[0].Reuse)
	}
}
