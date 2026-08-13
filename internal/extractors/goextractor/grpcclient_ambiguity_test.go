package goextractor

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

func parseFiles(t *testing.T, sources ...string) []*ast.File {
	t.Helper()
	fset := token.NewFileSet()
	var files []*ast.File
	for i, src := range sources {
		f, err := parser.ParseFile(fset, "stub"+string(rune('a'+i))+".go", src, 0)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}
	return files
}

const v1Stub = `package pb
import "google.golang.org/grpc"
var _ = grpc.SupportPackageIsVersion7
const Plugin_Prepare_FullMethodName = "/dra.v1.DRAPlugin/Prepare"
`

const v1beta1Stub = `package pb
import "google.golang.org/grpc"
var _ = grpc.SupportPackageIsVersion7
const Plugin_Prepare_FullMethodName = "/dra.v1beta1.DRAPlugin/Prepare"
`

// A service name declared in two versioned proto packages cannot be resolved
// from the short name alone. Emitting either one is a confident answer from a
// failed derivation, and picking whichever was scanned last makes the output
// depend on file order.
func TestAmbiguousServiceNameResolvesToNothing(t *testing.T) {
	idx := buildGoGRPCStubIndex(parseFiles(t, v1Stub, v1beta1Stub))
	if idx == nil {
		t.Fatal("the index must still be built")
	}
	stub := idx.byClient["DRAPluginClient"]
	if stub == nil {
		t.Fatal("the client interface must still be indexed")
	}
	if path, present := stub.methods["Prepare"]; present {
		t.Fatalf("an ambiguous method must resolve to nothing, got %q", path)
	}
	if !stub.ambiguous["Prepare"] {
		t.Fatal("the ambiguity must be recorded so a later file cannot revive it")
	}
}

// Order must not change the answer — that is the whole defect.
func TestAmbiguityIsOrderIndependent(t *testing.T) {
	forward := buildGoGRPCStubIndex(parseFiles(t, v1Stub, v1beta1Stub))
	reverse := buildGoGRPCStubIndex(parseFiles(t, v1beta1Stub, v1Stub))

	_, inForward := forward.byClient["DRAPluginClient"].methods["Prepare"]
	_, inReverse := reverse.byClient["DRAPluginClient"].methods["Prepare"]
	if inForward || inReverse {
		t.Fatal("neither scan order may produce a resolution")
	}
}

// A third declaration arriving after the ambiguity was recorded must not
// reinstate a resolution.
func TestALaterDuplicateCannotReviveAnAmbiguousMethod(t *testing.T) {
	idx := buildGoGRPCStubIndex(parseFiles(t, v1Stub, v1beta1Stub, v1Stub))
	if _, present := idx.byClient["DRAPluginClient"].methods["Prepare"]; present {
		t.Fatal("once ambiguous, a method stays unresolved")
	}
}

// The fix must not cost anything where the name is unambiguous, which is every
// repository but the largest.
func TestUnambiguousServicesStillResolve(t *testing.T) {
	idx := buildGoGRPCStubIndex(parseFiles(t, v1Stub))
	if got := idx.byClient["DRAPluginClient"].methods["Prepare"]; got != "/dra.v1.DRAPlugin/Prepare" {
		t.Fatalf("a single declaration must resolve normally, got %q", got)
	}
}

// Two methods on the same colliding service are judged independently: only the
// one actually declared twice is lost.
func TestOnlyTheCollidingMethodIsDropped(t *testing.T) {
	extra := `package pb
import "google.golang.org/grpc"
var _ = grpc.SupportPackageIsVersion7
const Plugin_Unprepare_FullMethodName = "/dra.v1.DRAPlugin/Unprepare"
`
	idx := buildGoGRPCStubIndex(parseFiles(t, v1Stub, v1beta1Stub, extra))
	stub := idx.byClient["DRAPluginClient"]
	if _, present := stub.methods["Prepare"]; present {
		t.Fatal("the colliding method must be dropped")
	}
	if got := stub.methods["Unprepare"]; got != "/dra.v1.DRAPlugin/Unprepare" {
		t.Fatalf("a method declared once must survive, got %q", got)
	}
}
