package rubydex

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Version is the Rubydex gem release the binary is written against. Bumping
// it means re-reading the C API the bindings depend on and refreshing the
// pins below from rubygems.org.
const Version = "0.4.0"

// pins are the sha256 digests rubygems.org publishes for each platform gem of
// Version, keyed by GOOS/GOARCH. A download whose digest differs is refused:
// the library runs inside the enola process.
var pins = map[string]struct {
	platform string
	sha256   string
}{
	"linux/amd64":  {"x86_64-linux", "75a4e1c6c4691ff815aee820dd26fb080f8053430a0ad248dd9b3125d621df86"},
	"linux/arm64":  {"aarch64-linux", "fab1f9cbb3301d2f3682f29165d9714aca5456fb3d70616b3676e3302af29709"},
	"darwin/amd64": {"x86_64-darwin", "173bfdad227f4f53aaaab5cdf75d4ebd39c4532a8e7a568b3d8530317a273c3a"},
	"darwin/arm64": {"arm64-darwin", "badbb7d1a7cfc03f1f7ce9fdc5ee414c729812c4b2eaa2bfe7da80d9a3a8b4b6"},
}

const downloadBase = "https://rubygems.org/downloads/"

// ErrUnsupportedPlatform says no Rubydex gem is published for this GOOS/GOARCH.
var ErrUnsupportedPlatform = errors.New("no Rubydex library is published for this platform")

func platformKey() string {
	return runtime.GOOS + "/" + runtime.GOARCH
}

func libraryFileName() string {
	if runtime.GOOS == "darwin" {
		return "librubydex_sys.dylib"
	}
	return "librubydex_sys.so"
}

// CacheDir is where the fetched library lives: the user's cache directory,
// under enola, by gem version, so a bump never reads a stale build.
func CacheDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "enola", "rubydex", Version), nil
}

// LibraryPath is where Open expects the library. It exists only after Fetch.
func LibraryPath() (string, error) {
	dir, err := CacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, libraryFileName()), nil
}

// Installed reports whether the pinned library is in the cache.
func Installed() (string, bool) {
	path, err := LibraryPath()
	if err != nil {
		return "", false
	}
	if _, err := os.Stat(path); err != nil {
		return path, false
	}
	return path, true
}

// FetchHint is the command that puts the library in place, for skips and
// doctor to name.
const FetchHint = "enola providers fetch rubydex"

// Fetch downloads the platform gem for Version, verifies its published
// digest, and extracts the engine library into the cache. It is the only
// network access the provider ever makes, and it is never made at snapshot
// time.
func Fetch(ctx context.Context, client *http.Client) (string, error) {
	pin, ok := pins[platformKey()]
	if !ok {
		return "", fmt.Errorf("%w (%s)", ErrUnsupportedPlatform, platformKey())
	}
	if client == nil {
		client = http.DefaultClient
	}
	url := downloadBase + "rubydex-" + Version + "-" + pin.platform + ".gem"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("downloading %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("downloading %s: HTTP %d", url, resp.StatusCode)
	}
	gem, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("downloading %s: %w", url, err)
	}
	sum := sha256.Sum256(gem)
	if got := hex.EncodeToString(sum[:]); got != pin.sha256 {
		return "", fmt.Errorf("%s does not match its pinned digest (got %s, pinned %s); refusing to install it", url, got, pin.sha256)
	}
	library, err := extractLibrary(gem)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", url, err)
	}
	dir, err := CacheDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, libraryFileName())
	tmp := path + ".partial"
	if err := os.WriteFile(tmp, library, 0o755); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		return "", err
	}
	return path, nil
}

// extractLibrary reads the gem, a tar whose data.tar.gz holds the gem's
// files, and returns the engine library's bytes.
func extractLibrary(gem []byte) ([]byte, error) {
	outer := tar.NewReader(strings.NewReader(string(gem)))
	for {
		header, err := outer.Next()
		if err == io.EOF {
			return nil, errors.New("the gem carries no data.tar.gz")
		}
		if err != nil {
			return nil, err
		}
		if header.Name != "data.tar.gz" {
			continue
		}
		gz, err := gzip.NewReader(outer)
		if err != nil {
			return nil, err
		}
		inner := tar.NewReader(gz)
		want := "lib/rubydex/" + libraryFileName()
		for {
			entry, err := inner.Next()
			if err == io.EOF {
				return nil, fmt.Errorf("the gem carries no %s", want)
			}
			if err != nil {
				return nil, err
			}
			if entry.Name == want {
				return io.ReadAll(inner)
			}
		}
	}
}
