# Declared intent — the enola standard

Enola measures what your architecture *is*. Intent declarations state
what it was *meant* to be — and every snapshot diffs the two, so
disagreement between decision and code surfaces as a finding with
evidence instead of waiting for someone to notice.

This page is the contract: where intent lives, exactly what enola
reads, the full vocabulary, and how verdicts behave. Everything here
is deterministic — declarations parse, compile, and verdict the same
way on every machine, and an invalid declaration fails the snapshot
rather than degrading silently.

## The one rule: enola reads only what enola defines

Intent reaches enola through exactly three carriers, each an
enola-defined schema:

| Carrier | Where | Scope |
|---|---|---|
| `enola-intent.yaml` | a repo's root | that repo's own declaration |
| `intent:` block | a cluster config (`mcp-arch.yaml`) | per-repo declarations for an estate, overriding repo files wholesale |
| `enola_intent:` key | a markdown page's YAML frontmatter | intent declared where the *decision* lives — a wiki, a docs tree |

For markdown, the key is deliberately namespaced: a wiki's own
toolchain, linters and renderers ignore `enola_intent:`, and enola
ignores everything else — it never reads your `title:`, your tags,
your custom frontmatter, and it **never parses the page body**. Prose
stays prose. If your wiki has its own conventions (statuses, source
citations, ownership fields), your tooling can *derive* the
`enola_intent:` block from them — but that derivation is your side of
the boundary; enola's contract is the block itself.

## The page schema

A page opts in by carrying `enola_intent:` in its frontmatter. Four
sections, all optional, all validated:

```yaml
enola_intent:
  page:                      # this page as a knowledge node
    type: decision           # lowercase token — your taxonomy
    status: living           # optional lowercase token
    scope: [backend]         # repos this knowledge is about
    affects: [mobile]        # repos this knowledge has consequences for
    origin: [slack, langfuse]  # channels the knowledge came from (closed set)
    relations:               # typed edges to other pages
      - {rel: part-of,       to: wiki/backend/epics/messaging.md}
      - {rel: depends-on,    to: wiki/backend/adrs/queue-choice.md}
      - {rel: supersedes,    to: wiki/backend/adrs/old-queue.md}
    anchors:                 # code locations this knowledge is about
      - {repo: backend, path: app/services/payment_processor.rb}
      - {repo: backend, path: app/handlers}
  consumes:                  # seams: who intends to call whom, and how
    - {repo: mobile, target: backend, via: graphql}
  layers:                    # a repo's declared layer order, outermost first
    - repo: backend
      order:
        - {name: handlers, paths: ["app/handlers/**"]}
        - {name: domain,   paths: ["app/domain/**"]}
  claims:                    # measurable statements, re-verdicted every snapshot
    - {metric: fact-count, repo: backend, kind: route, name_prefix: "/api", value: 214}
    - {metric: seam, consumer: mobile, provider: backend, via: graphql}
```

Every fact compiled from a page carries the **page as its
provenance** — a verdict's evidence cites the decision that declared
the intent, not a config artifact. Seam and layer entries name their
owner repo explicitly (`repo:`), because a page lives where the
decision lives, not inside the repo it governs.

## The vocabularies (closed, validated, named in errors)

- **`via`** — how a seam is mechanized: `http`, `http-client`,
  `grpc`, `graphql`, `kafka`, `import`, `shared_symbols`,
  `object-storage`. The last two name coupling a call graph cannot
  see: shared/vendored code, and bucket-mediated export/import
  handoffs. A via outside this set is a parse error naming the set.
- **`rel`** — how pages relate: `depends-on`, `supersedes`,
  `superseded-by`, `part-of`, `relates-to`. Targets are
  repo-relative markdown paths.
- **`anchors`** — where a relation joins page to page, an anchor
  joins page to code: a repo label plus a repo-relative path,
  either a file or a directory prefix. A validated shape rather
  than a closed vocabulary: both fields required, the path
  repo-relative. With anchors the reverse query — *which
  decisions govern this file?* — becomes a graph traversal
  instead of a grep through prose.
- **`origin`** — where knowledge came from: `slack`, `langfuse`,
  `notion`, `github`, `web`, `repo`, `other`. Channels, not source
  files: the entry names the class of system the page's evidence was
  ingested from, and your wiki keeps the mapping from its own source
  layout to these names. A new channel is a vocabulary addition
  here, never a stringly-typed leak.
- **`page.type` / `page.status`** — lowercase tokens; the taxonomy
  is yours.
- **`claims.metric`** — `fact-count` (kind + owner + optional
  file/name prefix + expected value) or `seam` (a measured
  cross-repo edge must exist).

## What compiles

Declarations become ordinary facts (`kind: intent`) at snapshot
time — snapshots carry them, diffs track them, receipts fingerprint
them. Nothing about intent lives in a side channel:

- a `page:` block → one **knowledge node** plus one **relation
  edge** per relation plus one **anchor fact** per anchor
- each `consumes:` entry → a seam-intent fact with `intent_owner`
- each `layers:` entry → per-layer facts feeding the layers explainer
- each `claims:` entry → a claim fact the explainer re-evaluates

## How verdicts behave

The `intent` explainer diffs declared against measured, with honest
confidences:

| Finding | Confidence | Why |
|---|---|---|
| Unexpected seam (measured, not declared) | 1.0 | set difference between stated and measured — exact |
| Mis-via (right target, wrong mechanism) | 1.0 | exact |
| Failed claim (count or seam doesn't hold) | 1.0 | the claim is stated, the count is counted |
| Missing intended seam (declared, not measured) | 0.8 | could be drift *or* an extraction miss — an estimate never presents as certainty |
| Dangling relation (edge to an uncompiled page) | 0.8 | the target may be deleted or merely not opted in |
| Dangling code anchor (a measurable path no fact touches) | 0.8 | the code moved or died — or this one file eluded extraction |
| Superseded intent still measured (edge covered only by a retired page) | 0.8 | the code may lag the superseding decision — or the successor's intent is undeclared |

**Superseded pages retire from current intent.** Two signals mark a
page retired: an outgoing `superseded-by` relation (enola's own
closed vocabulary), or the status token `superseded` — the one
status token enola reads meaning into; the rest of the status
taxonomy stays yours. A retired page's seams, claims, anchors and
scope stop verdicting — history must not nag as drift — while its
relations still verdict, because the supersession trail itself must
not break. The one thing a retired page still *says*: a measured
seam that only a retired declaration covers surfaces as *superseded
intent still measured* — the code has not caught up with the
superseding decision, or the successor's intent is not declared
where enola can see it. That finding replaces the generic
unexpected-seam verdict for such edges, because it carries the
diagnosis.

**Anchored code is traversable back to its decisions.**
`impact_analysis` on any node reports the knowledge pages whose
anchors cover the node's file — the governing decision trail,
surfaced exactly when someone is about to change the code it
governs — with each page's type and status joined from its own
declaration, and each page's outgoing relations riding along so
the trail continues past the first hop: the decision names what
it is part of, depends on, or supersedes. `show_symbol` carries
the same governing line beside the source. When the question is
governance alone, the `governing_intent` tool answers it directly
in either direction — a fact name or file path lists the pages
that govern it; a compiled page path lists its anchors with the
measured coverage under each — without computing a blast radius.
Its empty states stay honest to the counterparty rule: a snapshot
with no compiled pages answers *not asked*, which is never the
same answer as *asked, none governs*.

Repos that declare nothing are unasked — adoption is per-repo, and
undeclared is not a finding. A declared seam whose counterparty is
absent from the graph is skipped, never failed; an anchor into a
repo the graph never measured is skipped the same way — and so is
a file anchor whose kind the repo's graph never measures: for a
file with an extension the kind is the extension (a README, a
doc), and for a file without one the kind is its exact basename
(a Gemfile, a Dockerfile, a version dotfile — the manifests this
rule exists for are extensionless almost by convention, and a
repo measuring extensionless scripts has not thereby measured
them). No extractor could have proven any of these either way, so
they are unasked, never dangling. Only a path the graph plausibly
measures and does not touch is dangling. An anchor
that joins is silence — and the join is the point: it is the
stale-citation check a wiki otherwise performs by hand, and it
makes every anchored file's governing decisions reachable from the
graph.

Scope and affects, by contrast, are **never verdicted**: they
speak the wiki's own repo vocabulary, and the mapping from that
vocabulary to cluster labels is the deriving toolchain's side of
the boundary (working rule 5) — a page about one name may compile
against a cluster that labels the same repo another way. Keeping
those names truthful is the wiki's job, where the mapping is
known. Declared layer
patterns verdict at 1.0 through the layers explainer, *alongside*
heuristic recognition rather than instead of it: a declaring repo gets
its declared pattern and its proof-class violations, and the
snapshot-wide heuristic pattern is still reported next to them. The
heuristic cannot be switched off per repo, because its confidence is a
ratio over every module in the snapshot — suppressing one repo's
modules would move the score, and possibly the verdict, for repos that
declared nothing.

Two consequences worth designing for. A **declared seam also earns
coverage**: unresolved client calls in a repo with exactly one
declared HTTP target attribute to it (`attributed_by_intent`) —
declarations supply what static analysis cannot, visibly, without
inventing an edge. And a declared seam **no linker can measure yet**
(e.g. `object-storage`) surfaces as a standing 0.8 — that is the
honest state, not noise; judge it once in whatever ledger your
workflow keeps and it stays acknowledged.

## Working with intent, the enola way

1. **Declare only what you know.** A declaration triggers
   unexpected-seam verdicts over everything it measures for that
   repo — declare a repo's seams when you can stand behind the
   complete list, not for completeness's sake.
2. **Put the declaration where the decision lives.** If an ADR
   decides that the mobile app talks to the backend over GraphQL,
   that ADR's page carries the seam — and every future verdict cites
   it.
3. **One seam, one deciding page.** The same seam declared on two
   pages is a compile error, not a merge.
4. **Let claims guard your load-bearing numbers.** Any count your
   documentation states as fact can be a `fact-count` claim; from
   then on the number failing is a snapshot finding, not a stale
   sentence.
5. **Derive, don't hand-maintain, what your wiki already knows.**
   If your pages carry structured metadata (kinds, statuses,
   relations, source citations), generate the `enola_intent:` block
   from it with your own tooling and check the derivation in CI —
   enola validates the block; keeping it truthful to your
   conventions is yours. Source citations that name code paths are
   anchors waiting to be derived: the richest page-to-code signal a
   wiki carries is usually already in its citation discipline.
6. **Treat vocabulary gaps as decisions.** If a real seam has no
   via, that is a proposal for this page — not a reason to shoehorn
   it into a wrong one or drop it silently.
