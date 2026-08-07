namespace Acme.Domain;

// State and no behaviour: the C# DTO idiom. Carries data_holder so the
// package-metrics explainer does not tell this package to extract interfaces —
// advice that means nothing for a bag of values. C# writes these as plain classes
// with auto-properties rather than as a `record`, which is why the construct-based
// signal cannot see them.
public class OrderSummaryDto
{
    public int OrderId { get; set; }

    public string Currency { get; set; }
}
