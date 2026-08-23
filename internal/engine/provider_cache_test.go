package engine_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/enola-labs/enola/pkg/bootstrap"
)

// A per-file provider that emits one fact per listed Ruby file and records
// every invocation, so the warm snapshot can prove the provider did not run.
func writeCountingProvider(t *testing.T, marker string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "counting-provider")
	script := "#!/bin/sh\n" +
		"for a in \"$@\"; do if [ \"$a\" = \"--version\" ]; then echo 1.0.0; exit 0; fi; done\n" +
		"echo ran >> " + marker + "\n" +
		"listing=\"\"\n" +
		"while [ $# -gt 0 ]; do if [ \"$1\" = \"--files\" ]; then listing=\"$2\"; fi; shift; done\n" +
		"[ -n \"$listing\" ] || exit 1\n" +
		"while read -r f; do\n" +
		"  printf '{\"kind\":\"dependency\",\"name\":\"counted: %s\",\"file\":\"%s\",\"props\":{\"resolution_level\":\"name-only\"},\"relations\":[{\"kind\":\"calls\",\"target\":\"Ledger\"}]}\\n' \"$f\" \"$f\"\n" +
		"done < \"$listing\"\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestProviderCache_ColdAndWarmSnapshotsAreIdentical(t *testing.T) {
	root := copyTree(t, filepath.Join("testdata", "repos", "ruby_sample"), t.TempDir())
	marker := filepath.Join(t.TempDir(), "runs")
	script := writeCountingProvider(t, marker)
	config := filepath.Join(t.TempDir(), "mcp-arch.yaml")
	body := "providers:\n  - name: counting\n    command: [\"" + script + "\"]\n    files: per-file\n    extensions: [\".rb\"]\n"
	if err := os.WriteFile(config, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	snapshot := func() ([]byte, int, int) {
		eng, _, err := bootstrap.NewEngine(bootstrap.Options{ConfigPath: config})
		if err != nil {
			t.Fatalf("bootstrap.NewEngine: %v", err)
		}
		snap, err := eng.GenerateSnapshot(context.Background(), root, false)
		if err != nil {
			t.Fatalf("GenerateSnapshot: %v", err)
		}
		var buf bytes.Buffer
		if err := eng.Store().WriteJSONL(&buf); err != nil {
			t.Fatal(err)
		}
		if len(snap.Meta.Providers) != 1 || snap.Meta.Providers[0].Reuse == nil {
			t.Fatalf("receipt carries no reuse block: %+v", snap.Meta.Providers)
		}
		return normalize(buf.Bytes(), root), snap.Meta.Providers[0].Reuse.Reused, snap.Meta.Providers[0].Reuse.Computed
	}

	cold, reused, computed := snapshot()
	if reused != 0 || computed == 0 {
		t.Fatalf("cold snapshot reuse = %d/%d, want everything computed", reused, computed)
	}
	warm, reused, computed := snapshot()
	if computed != 0 || reused == 0 {
		t.Fatalf("warm snapshot reuse = %d/%d, want everything reused", reused, computed)
	}
	if data, _ := os.ReadFile(marker); bytes.Count(data, []byte("ran")) != 1 {
		t.Fatalf("the provider ran %d time(s); a warm snapshot must not run it", bytes.Count(data, []byte("ran")))
	}
	if !bytes.Equal(cold, warm) {
		t.Fatalf("cold and warm snapshots differ:\n%s", firstDiff(cold, warm))
	}
}
