---
title: Personal data processing record
enola_intent:
  page:
    type: policy
    status: living
    scope: [policy-as-code]
    origin: [repo]
    anchors:
      - {repo: policy-as-code, path: customers}
      - {repo: policy-as-code, path: customers/store.go}
---

# Personal data processing record

Every store in `customers/` holds personal data. Two obligations follow from
that and both are in `enola/constraints/gdpr.yaml`: the data does not reach the
application log, and every file that processes it is covered by a page like
this one.

This page is the record of processing activity for `customers/`. The anchors
below are what make it a record rather than a description: they name the files,
so a file added to the package without a corresponding entry here is a breach
rather than a thing somebody may notice in review.
