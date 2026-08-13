package bootstrap

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// sleepTick is one sampling interval, the unit a test waits in.
func sleepTick() { time.Sleep(memWatchFastInterval) }

// The summary line is parsed by enola-benchmarks/bench-sweep.sh, so its field
// names and key=value shape are a contract with that script, not just log text.
func TestMemWatch_ReportLineIsParseable(t *testing.T) {
	w := StartMemWatch("")
	var out bytes.Buffer
	w.Report(&out, 1234)

	line := out.String()
	if !strings.HasPrefix(line, "[memwatch] ") {
		t.Fatalf("missing prefix, got %q", line)
	}
	for _, field := range []string{
		"peakHeapMiB=", "steadyHeapMiB=", "sysMiB=", "totalAllocMiB=",
		"mallocs=", "facts=1234", "wallMs=",
	} {
		if !strings.Contains(line, field) {
			t.Errorf("summary line missing %q; got %q", field, line)
		}
	}
	// No profile path was given, so nothing should claim a file was written.
	if strings.Contains(line, "wrote") {
		t.Errorf("reported writing a profile without a path: %q", line)
	}
}

// --memstats asks for a summary line and nothing else. It must not leave a file
// behind: the steady-state profile path is built by appending ".final", and with
// no profile requested that concatenation is ".final" — a valid relative filename,
// which is exactly what an earlier version dropped into the working directory.
func TestMemWatch_NoProfilePathWritesNoFile(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	w := StartMemWatch("")
	w.Report(&bytes.Buffer{}, 0)

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		t.Errorf("--memstats left a file behind: %q", e.Name())
	}
}

// With a path, both profiles must exist and be non-empty — the peak one is
// written from inside the sampling loop, which is the part that silently does
// nothing if the path is bad.
func TestMemWatch_WritesBothProfiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "heap.pb.gz")

	w := StartMemWatch(path)
	// Allocate enough to guarantee at least one high-water mark is recorded while
	// the sampler is running, so the peak profile is actually written.
	sink := make([][]byte, 0, 64)
	for i := 0; i < 64; i++ {
		sink = append(sink, make([]byte, 1<<20))
	}
	waitForPeak(t, w)
	var out bytes.Buffer
	w.Report(&out, len(sink))

	for _, p := range []string{path, path + ".final"} {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatalf("expected profile at %s: %v", p, err)
		}
		if fi.Size() == 0 {
			t.Errorf("profile %s is empty", p)
		}
	}
	if !strings.Contains(out.String(), "wrote "+path) {
		t.Errorf("summary did not name the profiles it wrote: %q", out.String())
	}
}

// A nil watch is the "not instrumented" case, and every call site relies on it
// being safe rather than guarding individually.
func TestMemWatch_NilIsSafe(t *testing.T) {
	var w *MemWatch
	var out bytes.Buffer
	w.Report(&out, 0)
	if out.Len() != 0 {
		t.Errorf("nil watch wrote output: %q", out.String())
	}
	if got := w.PeakHeap(); got != 0 {
		t.Errorf("nil watch reported peak %d, want 0", got)
	}
}

// waitForPeak blocks until the sampler has recorded at least one reading, so a
// test does not race the first tick.
func waitForPeak(t *testing.T, w *MemWatch) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if w.PeakHeap() > 0 {
			return
		}
		sleepTick()
	}
	t.Fatal("sampler recorded no heap reading")
}
