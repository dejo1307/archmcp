// Block-scoped namespace, the older of the two spellings. Both must produce the
// same `namespace` prop, since resolution keys on it.
namespace Acme.Domain
{
    public enum OrderStatus
    {
        Draft,
        Submitted,
        Shipped
    }

    // A positional record: its parameters are the compiler-generated public
    // properties, and it is a class rather than a struct.
    public record Money(decimal Amount, string Currency);

    public interface IOrderRepository
    {
        // No access modifier: an interface member is public by default, which is
        // the one place C#'s default is not private.
        Order Find(int id);
    }

    public class Order
    {
        public int Id { get; set; }

        public OrderStatus Status { get; set; }

        // Private state stays out of the symbol set.
        private readonly List<string> _lines = new();

        public Money Total()
        {
            return new Money(0m, "EUR");
        }
    }
}
