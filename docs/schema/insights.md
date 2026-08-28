# insights.json

A JSON array of findings produced by enola's explainers (dependency cycles,
layer violations, unused routes, god-classes, hotspots, ...). The file is
always an array: a repository with no findings produces `[]`, never `null`.

## Insight object

| Field | Type | Present when | Meaning |
|---|---|---|---|
| `title` | string | always | One-line summary of the finding |
| `source` | string | when set | The explainer that produced it (e.g. `cycles`, `unused-routes`) |
| `description` | string | always | What the finding says and why it matters. Prose: shaped for a reader, regenerated every run |
| `confidence` | number 0–1 | always | 1.0 = structural/exact (a cycle is a cycle); below 1.0 = heuristic, a candidate to verify rather than a verdict |
| `evidence` | array of Evidence | always (may be empty) | The facts/files/symbols the finding cites |
| `suggested_actions` | array of string | when set | What to do about it |
| `informational` | bool | when true | The finding DESCRIBES the graph rather than complaining about it (a declared layer order, an intent override). Never gradeable |
| `metrics` | object | when set | The numbers the finding computed, as data. Mirrors the description — the prose keeps the same numbers; read values through tolerant numeric parsing, because after a JSON round-trip every number is a float |

## Evidence object

| Field | Type | Present when | Meaning |
|---|---|---|---|
| `file` | string | when set | Repo-relative file the evidence points at |
| `symbol` | string | when set | A fact's name, cited as a symbol |
| `fact` | string | when set | A fact's name, cited by kind-agnostic reference (modules and other non-symbol facts) |
| `detail` | string | when set | One line explaining what this piece of evidence shows |
| `line` / `end_line` / `column` / `end_column` | int | when the extractor measured a span | The position of the cited fact. Never derived from a name: a reader that prints a frame shows the line the extractor saw, or none |
| `fact_id` | string, 32 hex chars | when the citation resolves | The `id` of the fact this entry cites — the same identity `facts.jsonl` carries. See [facts.md, Identity and ids](facts.md#identity-and-ids) |

An evidence entry cites at most one of `symbol`/`fact`, plus an optional
`file`. Link it by `fact_id` where that is present; the name fields stay for
readability.

## When fact_id is absent

Absent is a normal outcome and must not be read as a defect. Three ways it
happens, and only the first is about resolution failing:

- The name matches facts of more than one identity, so there is no honest single
  answer. Rare: one entry in 872 in a measured corpus.
- The entry cites something the snapshot does not contain — a third-party symbol
  the repository calls but does not declare. Same condition as a relation whose
  `target` names a standard-library function.
- **The finding is ABOUT the absence.** A finding that a route names a handler
  which is not defined here cites a name no fact carries, on purpose. Its
  evidence resolving to nothing is the finding being true, and a consumer that
  treats a missing `fact_id` as a parse failure would discard exactly the
  findings that matter most.

Measured on a 20,808-fact snapshot: of 872 evidence entries, 88.5% carry a
`fact_id`.
