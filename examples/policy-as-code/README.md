# Policy as code

This example applies PCI DSS and GDPR-inspired constraints to a small Go
module. The initial code passes every strict constraint, with two documented
exemptions. The demo then makes three changes that violate those constraints.

All of the features used here are already available in Enola: components,
constraints, enforcement modes, exemptions, knowledge pages, and CI failure
thresholds.

```bash
./run.sh
```

You need `enola` on your `PATH`. Go is optional; it is only used in step 5 to
show that `go build` and `go vet` do not catch these policy violations.

## Example layout

```text
cardholder/   stores card numbers
gateway/      provides the approved path into cardholder/
checkout/     handles tokens rather than card numbers
analytics/    is outside the cardholder-data scope
customers/    stores personal data
legacy/       contains card storage allowed by an older policy
```

The files in `policy/` document scope and ownership decisions. The files in
`enola/constraints/` define the checks. One policy page has
`status: superseded`, so it remains in the repository for reference without
being treated as current policy.

## Constraints

| Control | Constraint | Mode | Check |
|---|---|---|---|
| PCI DSS 3.4 | `protect` / `owners` | strict | Only `gateway/` may call the card vault |
| PCI DSS 1.2 | `forbid_reach` | strict | `analytics/` cannot reach the card vault through any measured call path |
| PCI DSS 12.5 | `require_governed` | strict | Every file in the cardholder-data environment must be linked to a policy page |
| PCI DSS 12.5.2 | `cap` | advisory | Expanding the audited boundary produces a warning |
| PCI DSS 6.5 | `forbid_fact` + `governed_by ... status:superseded` | strict | Code covered only by the retired policy must be removed |
| GDPR Art. 5(1)(f) | `forbid` / `to_name` | strict | Code that handles personal data cannot call `log.*` |
| GDPR Art. 30 | `require_governed` | strict | Every file that handles personal data must be linked to a policy record |

### Checking indirect access

`forbid_reach` checks complete measured paths, not only direct calls. The demo
adds a call from `analytics.Reconcile` to `gateway.Charge`. Although that looks
like a normal reconciliation call, it reaches `cardholder.ReadPAN` two calls
later:

```text
Strict constraint pci-dss-1-2-analytics-stays-out-of-scope violated:
    analytics.Reconcile reaches cardholder.ReadPAN
      reachable in 2 hop(s)
```

### Selecting code through policy pages

`governed_by ... status:superseded` selects files linked to a superseded policy
page. This lets the constraint identify legacy code by the decision that
allowed it, without duplicating file paths in the constraint definition.

## How policy pages define scope

`policy/pci-dss-cardholder-data.md` links to the two files in the
cardholder-data environment. Those links are used in both directions:

- `require_governed` finds files inside the component that are not linked to a
  policy page. The demo adds `cardholder/rotate.go` without adding it to the
  policy page, which triggers this constraint.
- `governed_by` turns the files linked from a policy page into a component that
  other constraints can select.

File links match exact paths. Linking a directory covers its package fact, not
every file below it, so the policy pages link both the directory and its files.

## What the script does

| Step | Command | Purpose |
|---|---|---|
| 1 | `constraints lint` | Shows which members each selector matches |
| 2 | `constraints explain <file>` | Explains which policies and selectors apply to a file |
| 3 | `check --fail-on=constraints` | Checks the initial state |
| 4 | `plan --paths ...` | Shows which constraints apply before the files are edited |
| 5 | `go build && go vet` | Shows that the Go toolchain accepts the changes, when Go is installed |
| 6 | `check --fail-on=constraints` | Finds three strict violations after the changes |
| 7 | `constraints ledger` | Summarizes compliance and exemptions |

Step 4 also works for paths that do not exist yet. You can use it before
creating a file to see which constraints will apply.

The ledger reports how many violations have exemptions and how old the oldest
exemption is:

```text
law: 7 rules (6 strict, 1 advisory) · 6 breaches · 2 excused (33%) · oldest excuse 38 days
```

Each exemption must identify the affected finding, owner, reason, and date.

## Limits of this example

This example checks repository structure. It cannot verify runtime or data
properties such as encryption at rest, key rotation, retention periods, lawful
basis, access logging, or cross-border transfers.

The fact graph can check reachability and coverage: which code can reach other
code, which files are linked to a decision, and which packages are in scope.
These relationships often span several files and are not visible to a
line-oriented linter.

Two other limitations are important:

- **Scope is declared, not discovered.** Enola does not detect personal data.
  Someone must declare that `customers/` handles it, after which Enola checks
  the code against that declaration.
- **Control identifiers are plain text.** The `because:` field contains text
  such as "PCI DSS 4.0 req 3.4" because constraints do not have a dedicated
  control-ID field. A report would need to extract the identifier from that
  text.

## Current caveats

- **The stale-anchor check depends on the directory name.** A snapshot labels
  its facts with the repository directory's own name, and this example's pages
  anchor `repo: policy-as-code`. The `governed_by` component and the two
  `require_governed` constraints do not care: with one repository loaded they
  join an anchor by dropping its first path segment, whatever the label is. The
  intent explainer's dangling-anchor check does care, and it compares the two
  names. Run this tree from a directory called `policy-as-code` and an anchor
  pointing at a file that no longer exists reports `Dangling code anchor` at
  0.80. Run the same tree from a directory called anything else and it reports
  `Anchors not checked` at 0.40 for each page instead, because an anchor whose
  repository is absent from the graph is unasked rather than failed. The
  constraint verdicts are unaffected either way.

Two caveats this example carried have been fixed and are recorded here because
the recording of it predates them. `constraints lint` used to print
`retired-policy-code  0 member(s)` while the explainer resolved two members and
reported breaches against them; it now resolves the component through the
compiled policy pages, like the explainer does. And `require_defines` used to
verdict only members whose measured `symbol_kind` was `class`, so a rule over Go
structs matched nothing and said nothing; it now covers `struct` as well, which
means a law such as "every store of personal data defines `Erase`" is
declarable here. This example does not declare one.

For the full constraint vocabulary, see
[docs/CONSTRAINTS.md](../../docs/CONSTRAINTS.md). For declaration locations and
finding severity, see [docs/INTENT.md](../../docs/INTENT.md).
