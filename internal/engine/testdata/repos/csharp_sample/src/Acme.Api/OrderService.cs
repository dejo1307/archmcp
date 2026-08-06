using System;
using System.Net.Http;
using Acme.Domain;
using Repo = Acme.Domain.IOrderRepository;

// File-scoped namespace: everything below is in Acme.Api, and the declarations
// are siblings of this node rather than its children.
namespace Acme.Api;

public sealed class OrderService : IDisposable
{
    private readonly Repo _repo;
    private readonly HttpClient _http;

    public const string CacheKey = "orders";

    // A sole constructor: its parameters are injected dependencies. The `Repo`
    // alias must be substituted for the type it actually names.
    public OrderService(Repo repo, HttpClient http)
    {
        _repo = repo;
        _http = http;
    }

    public Money Summarise(Order[] orders)
    {
        var total = 0m;
        // A literal-bounded loop raises loop_depth but contributes no factor of n.
        for (var i = 0; i < 3; i++)
        {
            total += i;
        }
        // A loop over a parameter does scale, and the call inside it is an N+1
        // candidate because Fetch reaches the network transitively.
        foreach (var order in orders)
        {
            if (order.Id > 0 && order.Status == OrderStatus.Draft)
            {
                Fetch(order.Id);
            }
        }
        return new Money(total, "EUR");
    }

    private string Fetch(int id)
    {
        return _http.GetStringAsync("/orders/" + id).Result;
    }

    public void Dispose() { }
}

public static class OrderExtensions
{
    // An extension method: the receiver type is recorded, but no edge is drawn
    // from a `order.IsOpen()` call site, whose static type is not tracked.
    public static bool IsOpen(this Order order) => order.Status == OrderStatus.Draft;
}
