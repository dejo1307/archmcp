---
title: Cardholder data environment (PCI DSS 3.2.1)
enola_intent:
  page:
    type: policy
    status: superseded
    scope: [policy-as-code]
    origin: [repo]
    relations:
      - {rel: superseded-by, to: policy/pci-dss-cardholder-data.md}
    anchors:
      - {repo: policy-as-code, path: legacy/cardstore.go}
---

# Cardholder data environment (PCI DSS 3.2.1)

Retired. It permitted card rows in `legacy/`, which the current page does not.

The page stays in the tree on purpose. A retired decision stops contributing
current intent, but its anchors still say which code was written under it, and
that is exactly the code the migration has to remove.
