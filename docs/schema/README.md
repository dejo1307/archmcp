# Snapshot artifact format

This folder documents the on-disk format of enola's snapshot artifacts — the
files `enola --generate` writes to a repository's output directory (default
`.enola/`). It is the contract for external consumers that read a snapshot
without running enola's query layer: cognee, for example, auto-installs an
enola release, runs `--generate`, and re-materializes the graph in its own
database from exactly these files.

## Artifacts

| File | Format | Status |
|---|---|---|
| `facts.jsonl` | JSON Lines, one fact per line | contract — [facts.md](facts.md) |
| `insights.json` | JSON array of findings | contract — [insights.md](insights.md) |
| `receipt.json` | single JSON object: provenance + extraction quality | contract — [receipt.md](receipt.md) |
| `snapshot.meta.json` | single JSON object, internal superset of the receipt (adds per-file hashes) | not a stable contract |
| `llm_context.md` | rendered prose for LLM context | not a contract (renderer output) |

## What is stable

- The JSON field names of `Fact`, `Relation`, `Insight`, `Evidence`, `Receipt`
  and the objects nested in it. They are pinned by the tests in
  `internal/facts/wireformat_test.go`: a struct-tag rename fails the build
  instead of silently dropping fields in every downstream graph.
- The fact-kind and relation-kind vocabularies (the `kind` values), the
  constants in `internal/facts/model.go`. Every registered value must appear in
  [facts.md](facts.md); `TestWireFormat_DocumentedVocabulary` fails the build
  when a new one lands undocumented, because these pages are the contract.
- The prop keys and values that form a cross-package contract, the constants in
  `internal/facts/contract.go` and `internal/facts/model.go`.
- The identity convention: a fact is identified by `(repo, kind, name)`, and a
  relation target references a fact by `name`. It is a convention, not a
  uniqueness guarantee — [facts.md, Identity and name rules](facts.md#identity-and-name-rules)
  says what a consumer keying on it merges.

## What is not stable

- Descriptive props (`language`, `exported`, `handler`, ...). Extractors add
  and remove them freely; they are metadata for readers, not a contract. A
  consumer must not branch on their presence or values.
- Prose: insight titles and descriptions, `llm_context.md`. Shaped for a human
  or an LLM, regenerated on every run.

## Versioning

`receipt.json` carries `format_version`: the generation of this format that
the receipt describes. Current value: **1**.

- Additive changes — a new optional field, a new kind or prop value — do not
  bump it. A consumer that does not know about them ignores them, which is the
  correct behavior for optional data.
- Renaming or removing a documented field, changing the identity convention,
  or redefining an existing value's meaning bumps it. A bump ships in a major
  release and is called out in CHANGELOG.md, because consumers that pin an
  enola release (cognee does, with checksums) must re-pin to adopt it.

A consumer that cannot read a given `format_version` should fail loudly rather
than guess: partially parsing an unknown format produces a graph that looks
complete but is not. A `format_version` of zero means the receipt was not
stamped by the current writer (for example, one reconstructed from a shared
history payload); treat it as unknown.

## Determinism

`facts.jsonl` and `insights.json` are byte-stable: the same tree, config and
enola build produce identical bytes on every run (facts are sorted before
serialization). `receipt.json`'s `snapshot_id` is a SHA-256 over the fact
serialization plus the enola version and effective config hash, so unchanged
inputs yield an unchanged ID on any machine. [SNAPSHOTS.md](../SNAPSHOTS.md)
makes the argument and carries the measurements.
