namespace Acme.Domain;

// One half of a partial type. The other half (Widget.Extra.cs) declares the base
// list and a second method; both must fold into ONE Acme.Domain.Widget symbol
// carrying the union of their edges.
public partial class Widget
{
    public string Name { get; set; }

    public void Reset()
    {
        Describe();
    }
}
