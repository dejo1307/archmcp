package cppextractor

import "testing"

func TestCppIODirect_FilePrimitive(t *testing.T) {
	cases := map[string]string{
		"fopen_fread": `void load(const char *p) { FILE *f = fopen(p, "r"); char b[8]; fread(b, 1, 8, f); }`,
		"fwrite":      `void save(const char *p, const void *b) { FILE *f = fopen(p, "w"); fwrite(b, 1, 8, f); }`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			ff := extractProject(t, map[string]string{"io/reader.cpp": body})
			f, ok := findFact(ff, "io.load")
			if !ok {
				if f, ok = findFact(ff, "io.save"); !ok {
					t.Fatalf("no symbol fact; facts=%d", len(ff))
				}
			}
			if f.Props["io_direct"] != true {
				t.Errorf("io_direct = %v, want true (%s)", f.Props["io_direct"], name)
			}
		})
	}
}

func TestCppIODirect_SocketPrimitive(t *testing.T) {
	ff := extractProject(t, map[string]string{
		"io/net.cpp": `void pull(int fd, void *buf) { recvfrom(fd, buf, 64, 0, 0, 0); }`,
	})
	f, _ := findFact(ff, "io.pull")
	if f.Props["io_direct"] != true {
		t.Errorf("io_direct = %v, want true (recvfrom is a socket read)", f.Props["io_direct"])
	}
}

func TestCppIODirect_AmbiguousExcluded(t *testing.T) {
	// fprintf is console/logging (Msg::-style) and would mark every logger as I/O;
	// a bare read() is too generic. Neither is in the curated set.
	ff := extractProject(t, map[string]string{
		"io/log.cpp": `void note(const char *m) { fprintf(stderr, "%s", m); read_config(); }
void read_config() {}`,
	})
	f, _ := findFact(ff, "io.note")
	if f.Props["io_direct"] == true {
		t.Errorf("io_direct = true, want unset (fprintf logging + generic read must not count)")
	}
}

func TestCppComputePerformsIO_Transitive(t *testing.T) {
	// handler -> load_row -> fopen/fread. io_direct is set on load_row by the walker;
	// performs_io is the transitive fixpoint (package-level), computed inside Extract.
	ff := extractProject(t, map[string]string{
		"io/reader.cpp": `void load_row(const char *p) { FILE *f = fopen(p, "r"); char b[4]; fread(b, 1, 4, f); }
void handler() { load_row("x"); }`,
	})
	lr, _ := findFact(ff, "io.load_row")
	if lr.Props["io_direct"] != true {
		t.Fatalf("precondition: io.load_row io_direct = %v, want true", lr.Props["io_direct"])
	}
	h, _ := findFact(ff, "io.handler")
	if h.Props["performs_io"] != true {
		t.Errorf("io.handler performs_io = %v, want true (transitive through load_row)", h.Props["performs_io"])
	}
}
