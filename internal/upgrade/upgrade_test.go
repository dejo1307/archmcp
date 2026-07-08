package upgrade

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestVerifyChecksum(t *testing.T) {
	tarball := []byte("fake tarball contents")
	sum := sha256.Sum256(tarball)
	good := fmt.Sprintf("%s  enola-1.2.3-linux-amd64.tar.gz\n", hex.EncodeToString(sum[:]))

	if err := verifyChecksum(tarball, []byte(good)); err != nil {
		t.Fatalf("expected valid checksum to pass, got %v", err)
	}

	// Tampered payload must fail against the same recorded digest.
	if err := verifyChecksum([]byte("tampered"), []byte(good)); err == nil {
		t.Fatal("expected checksum mismatch, got nil")
	}

	if err := verifyChecksum(tarball, []byte("")); err == nil {
		t.Fatal("expected error on empty checksum file, got nil")
	}
}

func TestAssetNames(t *testing.T) {
	cases := []struct {
		goos, goarch string
		wantTarball  string
		wantInner    string
	}{
		{"linux", "amd64", "enola-1.2.3-linux-amd64.tar.gz", "enola-1.2.3-linux-amd64"},
		{"linux", "arm64", "enola-1.2.3-linux-arm64.tar.gz", "enola-1.2.3-linux-arm64"},
		{"darwin", "amd64", "enola-1.2.3-darwin-amd64.tar.gz", "enola-1.2.3-darwin-amd64"},
		{"darwin", "arm64", "enola-1.2.3-darwin-arm64.tar.gz", "enola-1.2.3-darwin-arm64"},
		{"windows", "amd64", "enola-1.2.3-windows-amd64.tar.gz", "enola-1.2.3-windows-amd64.exe"},
	}
	for _, c := range cases {
		got, err := assetNames("1.2.3", c.goos, c.goarch)
		if err != nil {
			t.Fatalf("%s/%s: unexpected error %v", c.goos, c.goarch, err)
		}
		if got.tarball != c.wantTarball {
			t.Errorf("%s/%s tarball = %q, want %q", c.goos, c.goarch, got.tarball, c.wantTarball)
		}
		if got.checksum != "enola-1.2.3-"+c.goos+"-"+c.goarch+".sha256" {
			t.Errorf("%s/%s checksum = %q", c.goos, c.goarch, got.checksum)
		}
		if got.innerBinary != c.wantInner {
			t.Errorf("%s/%s inner = %q, want %q", c.goos, c.goarch, got.innerBinary, c.wantInner)
		}
	}

	if _, err := assetNames("1.2.3", "linux", "riscv64"); err == nil {
		t.Fatal("expected error for unsupported platform, got nil")
	}
}

func TestExtractBinary(t *testing.T) {
	want := []byte("#!/bin/sh\necho hi\n")
	tarball := buildTarGz(t, "enola-1.2.3-linux-amd64", want)

	got, err := extractBinary(tarball, "enola-1.2.3-linux-amd64")
	if err != nil {
		t.Fatalf("extractBinary: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("extracted %q, want %q", got, want)
	}

	if _, err := extractBinary(tarball, "not-present"); err == nil {
		t.Fatal("expected error for missing entry, got nil")
	}
}

func TestLatestVersion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/enola-labs/enola/releases/latest" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `{"tag_name": "v0.1.26", "name": "release"}`)
	}))
	defer srv.Close()

	old := apiBase
	apiBase = srv.URL
	defer func() { apiBase = old }()

	got, err := latestVersion(context.Background())
	if err != nil {
		t.Fatalf("latestVersion: %v", err)
	}
	if got != "0.1.26" {
		t.Errorf("latestVersion = %q, want %q", got, "0.1.26")
	}
}

func TestDownload(t *testing.T) {
	body := []byte("asset bytes")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))
	defer srv.Close()

	got, err := download(context.Background(), srv.URL+"/enola.tar.gz")
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Errorf("download = %q, want %q", got, body)
	}

	// Non-2xx must surface as an error.
	fail := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer fail.Close()
	if _, err := download(context.Background(), fail.URL); err == nil {
		t.Fatal("expected error on 404, got nil")
	}
}

// buildTarGz returns a gzipped tar archive containing a single file.
func buildTarGz(t *testing.T, name string, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	hdr := &tar.Header{Name: name, Mode: 0o755, Size: int64(len(data))}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
