using Xunit;
using Acme.Api;
using Acme.Domain;

namespace Acme.Api.Tests;

// This file is excluded from production indexing by the `**/tests/**/*.cs` glob, so
// it contributes NO symbols — the absence of Acme.Api.Tests.OrderServiceTests from
// the golden is the assertion that test classes never become dead-code candidates.
//
// What it does contribute is one test_ref fact carrying the production names it
// touches, so a symbol whose only caller is a test is not mis-reported as dead.
// Framework noise (Assert.*) is dropped; the subject's own names are kept.
public class OrderServiceTests
{
    [Fact]
    public void SummarisesOrders()
    {
        var svc = new OrderService(null, null);
        var money = svc.Summarise(new Order[0]);

        Assert.Equal("EUR", money.Currency);
        Assert.NotNull(money);
    }

    [Fact]
    public void WidgetDescribes()
    {
        var w = new Widget();
        w.Reset();
    }
}
