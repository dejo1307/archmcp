---
title: Cardholder data environment
enola_intent:
  page:
    type: policy
    status: living
    scope: [policy-as-code]
    origin: [repo]
    anchors:
      - {repo: policy-as-code, path: cardholder}
      - {repo: policy-as-code, path: cardholder/vault.go}
      - {repo: policy-as-code, path: gateway/gateway.go}
---

# Cardholder data environment

The primary account number lives in `cardholder/` and is read in exactly one
place: `gateway.Charge`. Everything else in this module works with a token.

The two files this page anchors are the cardholder data environment. Adding a
third file to it is a change to the audited scope, not a refactor, and the laws
in `enola/constraints/pci-dss.yaml` are written against that boundary.

## What the anchors are for

An anchor is a citation from this decision to the code it decides. It is what
lets `require_governed` ask the opposite question: is there a file inside the
environment that no decision covers?
