package rubydex

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"strings"
	"testing"
)

func TestPins_CoverEveryPublishedPlatform(t *testing.T) {
	for _, key := range []string{"linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64"} {
		pin, ok := pins[key]
		if !ok || len(pin.sha256) != 64 || pin.platform == "" {
			t.Errorf("%s: the pin must name the gem platform and a full sha256, got %+v", key, pin)
		}
	}
}

func gemWith(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var data bytes.Buffer
	gz := gzip.NewWriter(&data)
	inner := tar.NewWriter(gz)
	for name, body := range files {
		if err := inner.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := inner.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := inner.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	var gem bytes.Buffer
	outer := tar.NewWriter(&gem)
	for name, body := range map[string][]byte{"metadata.gz": []byte("x"), "data.tar.gz": data.Bytes()} {
		if err := outer.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := outer.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := outer.Close(); err != nil {
		t.Fatal(err)
	}
	return gem.Bytes()
}

// A gem is a tar around a gzipped tar; the library sits at lib/rubydex/ under
// the platform's file name, and nothing else in the gem is read.
func TestExtractLibrary_ReadsTheEngineOutOfTheGem(t *testing.T) {
	gem := gemWith(t, map[string]string{
		"lib/rubydex.rb":                   "ruby",
		"lib/rubydex/" + libraryFileName(): "ELF-or-Mach-O",
	})
	got, err := extractLibrary(gem)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "ELF-or-Mach-O" {
		t.Fatalf("extracted %q", got)
	}
	if _, err := extractLibrary(gemWith(t, map[string]string{"lib/rubydex.rb": "ruby"})); err == nil || !strings.Contains(err.Error(), "carries no lib/rubydex/") {
		t.Fatalf("a gem without the library must be refused by name, got %v", err)
	}
}
