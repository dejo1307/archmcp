package facts

import (
	"runtime"
	"testing"
)

// Store.Add's path normalisation is the last line of defence for the fact-path
// invariant. Two things about it need pinning, and only one of them is host-neutral.
//
// The conversion itself is host-CONDITIONAL, deliberately: it uses factpath.Slash,
// which is the identity wherever the separator is already "/". That is not a gap. On
// Unix a backslash in a file name is a legal filename character, and rewriting it
// would move a real file into a directory it is not in — corrupting correct data to
// paper over a bug that only Windows can produce. So the conversion is asserted on
// Windows, where CI runs it (see .github/workflows/ci.yml), and the invariant it
// exists to serve is asserted everywhere.
//
// The carve-out is host-neutral and must hold on every host: PHP namespaces are
// backslash-separated, so a symbol name and a relation target may legitimately carry
// backslashes and must survive untouched.

// Whatever Add stores, the indexes agree with the fields. This is the property that
// actually matters — a File normalised in the struct but indexed under its original
// spelling would make ByFile return nothing, which is the same silent miss the
// normalisation exists to prevent.
func TestStoreAdd_IndexesAgreeWithNormalisedFields(t *testing.T) {
	s := NewStore()
	s.Add(
		Fact{Kind: KindSymbol, Name: "blockId", File: `src\lib\site-blocks.ts`},
		Fact{Kind: KindModule, Name: `src\lib`},
	)
	for _, f := range s.All() {
		if len(s.ByFile(f.File)) == 0 && f.File != "" {
			t.Errorf("fact %q is stored with File %q but is not indexed under it", f.Name, f.File)
		}
		if len(s.ByName(f.Name)) == 0 {
			t.Errorf("fact is stored with Name %q but is not indexed under it", f.Name)
		}
	}
}

// On Windows — the host that produces them — a backslash path becomes a fact path.
func TestStoreAdd_NormalisesHostPathsOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("filepath.ToSlash is the identity here; this is the assertion the Windows CI job carries")
	}
	s := NewStore()
	s.Add(
		Fact{Kind: KindSymbol, Name: "blockId", File: `src\lib\site-blocks.ts`},
		Fact{Kind: KindModule, Name: `src\lib`},
		Fact{Kind: KindFileRef, Name: `src\lib\site-blocks.ts`},
		Fact{Kind: KindTestRef, Name: `spec\services\report_spec.rb`},
	)
	want := map[string]string{
		KindSymbol:  "src/lib/site-blocks.ts", // checked as File below
		KindModule:  "src/lib",
		KindFileRef: "src/lib/site-blocks.ts",
		KindTestRef: "spec/services/report_spec.rb",
	}
	if got := s.ByKind(KindSymbol)[0].File; got != want[KindSymbol] {
		t.Errorf("symbol File = %q, want %q", got, want[KindSymbol])
	}
	for _, kind := range []string{KindModule, KindFileRef, KindTestRef} {
		if got := s.ByKind(kind)[0].Name; got != want[kind] {
			t.Errorf("%s name = %q, want %q", kind, got, want[kind])
		}
	}
}

// The carve-out, and the reason normalisation cannot simply be applied to every
// string that looks like a path. PHP separates namespace segments with a backslash,
// so these are correct values — rewriting them would break PHP resolution in order
// to fix Windows, trading one silent mismatch for another.
func TestStoreAdd_PreservesPHPNamespaceSeparators(t *testing.T) {
	const (
		class   = `App\Http\Controllers\UserController`
		facade  = `Illuminate\Support\Facades\Route`
		depName = `routes -> Illuminate\Support\Facades\Route`
	)
	s := NewStore()
	s.Add(
		Fact{Kind: KindSymbol, Name: class, File: "app/Http/Controllers/UserController.php"},
		Fact{Kind: KindDependency, Name: depName, File: "routes/web.php",
			Relations: []Relation{{Kind: RelImports, Target: facade}}},
	)

	if got := s.ByKind(KindSymbol)[0].Name; got != class {
		t.Errorf("symbol name = %q, want the PHP FQN unchanged", got)
	}
	dep := s.ByKind(KindDependency)
	if len(dep) != 1 {
		t.Fatalf("want 1 dependency, got %d", len(dep))
	}
	if dep[0].Name != depName {
		t.Errorf("dependency name = %q, want it unchanged", dep[0].Name)
	}
	if dep[0].Relations[0].Target != facade {
		t.Errorf("relation target = %q, want the PHP FQN unchanged — a target is not a path", dep[0].Relations[0].Target)
	}
	if dep[0].File != "routes/web.php" {
		t.Errorf("File = %q, want the path beside them untouched", dep[0].File)
	}
}
