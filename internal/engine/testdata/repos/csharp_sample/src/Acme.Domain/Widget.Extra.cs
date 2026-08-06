namespace Acme.Domain;

public partial class Widget : IOrderRepository
{
    public Order Find(int id)
    {
        return new Order();
    }

    public void Describe() { }
}
