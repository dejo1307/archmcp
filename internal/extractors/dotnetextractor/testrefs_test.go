package dotnetextractor

import (
	"sort"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

func refTargets(t *testing.T, src, relFile string) []string {
	t.Helper()
	ff := testRefsFromFile([]byte(src), relFile)
	if len(ff) == 0 {
		return nil
	}
	if ff[0].Kind != facts.KindTestRef {
		t.Fatalf("kind = %q, want test_ref", ff[0].Kind)
	}
	if ff[0].Name != relFile {
		t.Errorf("name = %q, want the file path", ff[0].Name)
	}
	var out []string
	for _, r := range ff[0].Relations {
		if r.Kind != facts.RelCalls {
			t.Errorf("test_ref carried a %q relation; only calls are allowed", r.Kind)
		}
		out = append(out, r.Target)
	}
	sort.Strings(out)
	return out
}

func has(targets []string, want string) bool {
	for _, t := range targets {
		if t == want {
			return true
		}
	}
	return false
}

const testSrc = `
using Xunit;
using Acme.Orders;

namespace Acme.Tests;

public class OrderServiceTests
{
    [Fact]
    public void FindsAnOrder()
    {
        var repo = new InMemoryOrderRepository();
        var sut = new OrderService(repo);

        var order = sut.Find(42);

        Assert.Equal(OrderStatus.Draft, order.Status);
        Assert.NotNull(order);
    }
}
`

func TestExtractTestRefs_CapturesProductionReferences(t *testing.T) {
	got := refTargets(t, testSrc, "tests/OrderServiceTests.cs")

	for _, want := range []string{
		"OrderService",            // construction of the subject
		"InMemoryOrderRepository", // construction of a collaborator
		"Find",                    // the method under test, bare
		"sut.Find",                // and qualified by its receiver
		"OrderStatus.Draft",       // an enum member read
	} {
		if !has(got, want) {
			t.Errorf("missing reference %q; got %v", want, got)
		}
	}
}

// TestExtractTestRefs_FrameworkNoiseDropped keeps the reference set about the
// subject rather than the harness. A test file is mostly assertions, and across
// 18,000 test files those are a great many edges that can match no production
// symbol.
func TestExtractTestRefs_FrameworkNoiseDropped(t *testing.T) {
	got := refTargets(t, testSrc, "tests/OrderServiceTests.cs")
	for _, unwanted := range []string{"Assert", "Assert.Equal", "Assert.NotNull"} {
		if has(got, unwanted) {
			t.Errorf("framework noise %q should be dropped; got %v", unwanted, got)
		}
	}
}

// TestExtractTestRefs_EmitsOnlyReferenceFacts pins the contract: a test file must
// contribute no symbols, so test classes never become dead-code candidates and no
// symbol/module/route explainer is affected.
func TestExtractTestRefs_EmitsOnlyReferenceFacts(t *testing.T) {
	ff := testRefsFromFile([]byte(testSrc), "tests/OrderServiceTests.cs")
	if len(ff) != 1 {
		t.Fatalf("expected exactly one fact per file, got %d", len(ff))
	}
	if ff[0].Kind != facts.KindTestRef {
		t.Fatalf("kind = %q", ff[0].Kind)
	}
	// The subject's own test class must not appear as a reference target either.
	for _, r := range ff[0].Relations {
		if strings.Contains(r.Target, "OrderServiceTests") {
			t.Errorf("test class leaked into its own references: %v", r)
		}
	}
}

func TestExtractTestRefs_Deterministic(t *testing.T) {
	a := refTargets(t, testSrc, "tests/OrderServiceTests.cs")
	b := refTargets(t, testSrc, "tests/OrderServiceTests.cs")
	if strings.Join(a, ",") != strings.Join(b, ",") {
		t.Errorf("targets are not stable: %v vs %v", a, b)
	}
	if !sort.StringsAreSorted(a) {
		t.Errorf("targets must be sorted, got %v", a)
	}
}

// TestExtractTestRefs_FrameworkReceiverDisqualifiesBareName pins the fix for a
// leak the fixture golden exposed. Filtering only the QUALIFIED form let
// `Assert.Equal` through as the bare `Equal` — and `Equal` is a method name
// production code really has, so the harness would have vouched for a symbol no
// test exercises, suppressing a genuine dead-code finding.
func TestExtractTestRefs_FrameworkReceiverDisqualifiesBareName(t *testing.T) {
	got := refTargets(t, `
using Xunit;
namespace Acme.Tests;
public class T
{
    [Fact]
    public void M()
    {
        var svc = new OrderService();
        Assert.Equal(1, svc.Count());
        Assert.NotNull(svc);
        Mock.Of<IThing>();
    }
}
`, "tests/T.cs")

	for _, unwanted := range []string{"Equal", "NotNull", "Of", "Assert.Equal", "Mock.Of"} {
		if has(got, unwanted) {
			t.Errorf("framework name %q survived; got %v", unwanted, got)
		}
	}
	// The subject's own references must survive the filter.
	for _, want := range []string{"OrderService", "Count", "svc.Count"} {
		if !has(got, want) {
			t.Errorf("missing real reference %q; got %v", want, got)
		}
	}
}
