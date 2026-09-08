# Policy as code, with nothing added to enola

Two compliance regimes declared as law over one small Go module, and one change
that breaches three of their controls. Every construct on this page ships in
the binary today: components, the rule forms, modes, exemptions, knowledge
pages and the gate. Nothing here needed a new keyword.

```bash
./run.sh
```

It needs `enola` on your PATH and nothing else: no Go toolchain, no build. The binary you
already have runs against this directory exactly the way it runs against your own code.

## The module

```
cardholder/   the cardholder data environment: the only code that holds a PAN
gateway/      the one sanctioned way in
checkout/     ordinary application code, works with tokens
analytics/    declared out of scope
customers/    personal data
legacy/       card storage the previous standard allowed
```

Three knowledge pages under `policy/` carry the decisions, and two files under
`enola/constraints/` carry the law. One of the pages is `status: superseded`,
which is how a retired version of a standard stays in the tree without still
governing anything.

## The controls

| Control | Form | Mode | What it actually verdicts |
|---|---|---|---|
| PCI DSS 3.4 | `protect` / `owners` | strict | only `gateway/` may call into the vault |
| PCI DSS 1.2 | `forbid_reach` | strict | `analytics/` cannot reach the vault by **any** measured path |
| PCI DSS 12.5 | `require_governed` | strict | every file in the environment is anchored by a policy page |
| PCI DSS 12.5.2 | `cap` | advisory | the audited boundary does not grow without a decision |
| PCI DSS 6.5 | `forbid_fact` + `governed_by … status:superseded` | strict | code written under the retired policy is gone |
| GDPR Art. 5(1)(f) | `forbid` / `to_name` | strict | personal data never reaches `log.*` |
| GDPR Art. 30 | `require_governed` | strict | every file that processes personal data has a record |

Two of those are worth reading twice.

**`forbid_reach` is the control a compliance boundary actually needs.** Not
"analytics does not read a PAN today" but "no measured path from analytics
arrives at one". The change in `run.sh` adds a single call from reporting to
`gateway.Charge`, which looks like reconciliation and is two hops from the
vault. That is the finding:

```
Strict constraint pci-dss-1-2-analytics-stays-out-of-scope violated:
    analytics.Reconcile reaches cardholder.ReadPAN
      reachable in 2 hop(s)
```

**`governed_by … status:superseded` names code by the decision behind it**
rather than by where it sits. The component is "the files the retired page
anchors", so the migration away from the old standard is a law with a witness
per file, not a ticket.

## Scope is declared by the page, and checked from both ends

`policy/pci-dss-cardholder-data.md` anchors the two files that are the
cardholder data environment. Those anchors do two jobs at once:

- `require_governed` asks the reverse question. A file **inside** the boundary
  that no page anchors is scope nobody documented, and the run's second breach
  is exactly that: `cardholder/rotate.go` has no governing page.
- `governed_by` turns a page into a component, so a rule can be written about
  "the code this decision covers" without repeating its paths.

An anchor joins a path **exactly**. A directory anchor covers the package fact,
not the files under it, which is why the pages here anchor both.

## The seven runs

| | What it shows |
|---|---|
| 1. `constraints lint` | what each selector actually selects, before any verdict rests on it |
| 2. `constraints explain <file>` | why this file is under this policy, and which selector admitted it |
| 3. `check --fail-on=constraints` | the clean state: every control obeyed, two breaches signed off |
| 4. `plan --paths …` | which controls govern the files about to be edited, **before** the edit |
| 5. `go build && go vet` | the toolchain has no opinion about any of the three edits |
| 6. `check --fail-on=constraints` | the same gate after the change: 3 strict breaches, exit 1 |
| 7. `constraints ledger` | how much of the law is obeyed and how much is excused |

Run 4 is the one worth adopting first. It answers for a file that does not
exist yet, so an agent asks what the compliance boundary requires while the
tree is still clean rather than finding out in CI.

Run 7 is the compliance question the gate cannot answer one verdict at a time:

```
law: 7 rules (6 strict, 1 advisory) · 6 breaches · 2 excused (33%) · oldest excuse 38 days
```

Every excuse is signed. An exemption names the witness it covers, an owner, a
reason and a date, and all four are mandatory, so a control that is being
excused rather than obeyed shows up as a rate and an age instead of as silence.

## What this cannot say, and why that is the point

A compliance regime is not a set of structural properties, and most of one is
not expressible here. Encryption at rest, key rotation, retention windows,
lawful basis, access logging and cross-border transfer are properties of data
and of running systems. Nothing in a snapshot measures them, and a control that
reads as enforced while nothing checks it is worse than no control at all.

What a fact graph verdicts exactly is **reachability and coverage**: which code
can arrive at which other code, which files a decision covers, which packages
are declared. That is a real and useful slice of a regime, and it is the slice
that is invisible to every linter, because a violation here is a path across
four files rather than a line.

Two more limits, both visible in this example:

- **The scope is asserted, not discovered.** enola does not find personal data.
  A human declares that `customers/` holds it, and enola grades the code
  against that declaration.
- **The control identifier lives in prose.** `because:` carries "PCI DSS 4.0
  req 3.4" as text, because a rule has no field for the obligation it
  implements. Everything else on this page is data the graph can be queried
  for; the citation is the one part that a report would have to parse out of a
  sentence.

## Three sharp edges you will meet

- **`repos: ["."]`, not `repo: "."`.** Anchors join through a repo label, and a
  plain single-repo snapshot carries none. With `repo:` the two `require_governed`
  laws and the `governed_by` component resolve to nothing and hold vacuously.
  `mcp-arch.yaml` says the same thing in a comment.
- **`constraints lint` under-reports a `governed_by` component.** Run 1 prints
  `retired-policy-code  0 member(s)` while the explainer resolves two members
  and reports two breaches on them. Lint resolves components against the
  declaration plus the snapshot's non-intent facts, so the compiled anchors are
  not in the store it looks at. Trust the verdict, not that line.
- **`require_defines` is Go-blind.** It verdicts members whose measured
  `symbol_kind` is `class`, and Go structs are `struct`, so "every store defines
  `Erase`" is not stated here: it would have compiled, linted clean and
  verdicted nothing. In a Ruby, Python or TypeScript codebase it is one of the
  strongest forms available.

---

The vocabulary in full: **[docs/CONSTRAINTS.md](../../docs/CONSTRAINTS.md)**.
Where declarations live and how findings are graded:
**[docs/INTENT.md](../../docs/INTENT.md)**.
