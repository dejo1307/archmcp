// Package upgrade implements the `enola upgrade` self-update command. It
// resolves the latest GitHub release for enola, downloads the artifact for the
// current platform, verifies its checksum, and atomically replaces the running
// binary. It mirrors the download/verify/install contract of install.sh.
package upgrade

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Overridable base URLs (production defaults). Tests point these at an
// httptest server to exercise Run without touching the network.
var (
	// apiBase is the GitHub REST API host for the releases/latest lookup.
	apiBase = "https://api.github.com"
	// downloadBase is the host serving release assets.
	downloadBase = "https://github.com"
)

const (
	repoSlug = "enola-labs/enola"
	// maxDownload bounds the artifact download to guard against absurd sizes.
	maxDownload = 512 << 20 // 512 MiB
)

// supportedPlatforms lists the GOOS/GOARCH combinations the release workflow
// builds. Anything else has no downloadable artifact.
var supportedPlatforms = map[string]bool{
	"linux/amd64":   true,
	"linux/arm64":   true,
	"darwin/amd64":  true,
	"darwin/arm64":  true,
	"windows/amd64": true,
}

// Run performs a self-update to the latest release. current is the installed
// version (version.Version); a value of "dev" is always treated as out of date.
func Run(ctx context.Context, current string) error {
	current = strings.TrimPrefix(current, "v")

	latest, err := latestVersion(ctx)
	if err != nil {
		return fmt.Errorf("resolving latest version: %w", err)
	}

	if current != "dev" && current == latest {
		fmt.Fprintf(os.Stderr, "enola is already up to date (v%s)\n", current)
		return nil
	}

	names, err := assetNames(latest, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "==> Downloading enola v%s for %s/%s ...\n", latest, runtime.GOOS, runtime.GOARCH)

	base := fmt.Sprintf("%s/%s/releases/download/v%s", downloadBase, repoSlug, latest)
	tarball, err := download(ctx, base+"/"+names.tarball)
	if err != nil {
		return fmt.Errorf("downloading %s: %w", names.tarball, err)
	}
	sumFile, err := download(ctx, base+"/"+names.checksum)
	if err != nil {
		return fmt.Errorf("downloading %s: %w", names.checksum, err)
	}

	fmt.Fprintln(os.Stderr, "==> Verifying checksum ...")
	if err := verifyChecksum(tarball, sumFile); err != nil {
		return err
	}

	fmt.Fprintln(os.Stderr, "==> Extracting ...")
	binary, err := extractBinary(tarball, names.innerBinary)
	if err != nil {
		return err
	}

	fmt.Fprintln(os.Stderr, "==> Installing ...")
	if err := replaceExecutable(binary); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "Upgraded enola v%s -> v%s\n", current, latest)
	return nil
}

// latestVersion queries the GitHub API for the latest release tag and returns
// it without a leading "v".
func latestVersion(ctx context.Context) (string, error) {
	url := fmt.Sprintf("%s/repos/%s/releases/latest", apiBase, repoSlug)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if tok := os.Getenv("GITHUB_TOKEN"); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("GitHub API returned %s", resp.Status)
	}

	var payload struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&payload); err != nil {
		return "", err
	}
	if payload.TagName == "" {
		return "", errors.New("no tag_name in latest release")
	}
	return strings.TrimPrefix(payload.TagName, "v"), nil
}

type assets struct {
	tarball     string
	checksum    string
	innerBinary string
}

// assetNames derives the release artifact names for a version and platform,
// matching the naming produced by .github/workflows/release.yml.
func assetNames(version, goos, goarch string) (assets, error) {
	if !supportedPlatforms[goos+"/"+goarch] {
		return assets{}, fmt.Errorf("no prebuilt release for %s/%s; install manually from https://github.com/%s/releases", goos, goarch, repoSlug)
	}
	base := fmt.Sprintf("enola-%s-%s-%s", version, goos, goarch)
	inner := base
	if goos == "windows" {
		inner += ".exe"
	}
	return assets{
		tarball:     base + ".tar.gz",
		checksum:    base + ".sha256",
		innerBinary: inner,
	}, nil
}

// download fetches url and returns the full response body.
func download(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxDownload))
}

// verifyChecksum compares the sha256 of tarball against the digest recorded in
// a sha256sum-format file (`<hex>  <filename>`).
func verifyChecksum(tarball, sumFile []byte) error {
	fields := strings.Fields(string(sumFile))
	if len(fields) == 0 {
		return errors.New("empty checksum file")
	}
	want := strings.ToLower(fields[0])

	sum := sha256.Sum256(tarball)
	got := hex.EncodeToString(sum[:])
	if got != want {
		return fmt.Errorf("checksum mismatch: expected %s, got %s", want, got)
	}
	return nil
}

// extractBinary reads a gzipped tarball and returns the contents of the entry
// named want.
func extractBinary(tarball []byte, want string) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(tarball))
	if err != nil {
		return nil, fmt.Errorf("opening gzip: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading tar: %w", err)
		}
		if filepath.Base(hdr.Name) == want {
			data, err := io.ReadAll(io.LimitReader(tr, maxDownload))
			if err != nil {
				return nil, fmt.Errorf("extracting %s: %w", want, err)
			}
			return data, nil
		}
	}
	return nil, fmt.Errorf("binary %q not found in archive", want)
}

// replaceExecutable writes the new binary next to the current executable and
// atomically swaps it into place.
func replaceExecutable(binary []byte) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locating current executable: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}

	dir := filepath.Dir(exe)
	tmp, err := os.CreateTemp(dir, ".enola-upgrade-*")
	if err != nil {
		return installPermError(dir, err)
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(binary); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, 0o755); err != nil {
		return err
	}

	if runtime.GOOS == "windows" {
		// A running .exe cannot be overwritten; move it aside first.
		old := exe + ".old"
		os.Remove(old) // best-effort cleanup of a prior upgrade
		if err := os.Rename(exe, old); err != nil {
			return installPermError(dir, err)
		}
		if err := os.Rename(tmpPath, exe); err != nil {
			os.Rename(old, exe) // roll back
			return installPermError(dir, err)
		}
		cleanup = false
		os.Remove(old) // best-effort; may fail while the old exe is still running
		return nil
	}

	if err := os.Rename(tmpPath, exe); err != nil {
		return installPermError(dir, err)
	}
	cleanup = false
	return nil
}

// installPermError wraps errors that are typically permission-related with an
// actionable hint.
func installPermError(dir string, err error) error {
	if errors.Is(err, os.ErrPermission) {
		return fmt.Errorf("cannot write to %s: %w\nTry re-running with elevated permissions, or re-run the installer: curl -fsSL https://raw.githubusercontent.com/%s/main/install.sh | sh", dir, err, repoSlug)
	}
	return err
}
