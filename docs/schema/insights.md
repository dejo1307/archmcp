# insights.json

A JSON array of findings produced by enola's explainers. The file always
contains an array; a snapshot with no findings contains `[]`.

## Insight object

| Field | Type | Present when | Meaning |
|---|---|---|---|
| `title` | string | always | One-line finding summary |
| `source` | string | when set | Explainer that produced the finding, such as `cycles` or `unused-routes` |
| `description` | string | always | Presentation text describing the finding; not a stable identifier |
| `confidence` | number from 0 to 1 | always | `1.0` for structural findings; less than `1.0` for heuristic findings |
| `evidence` | array of Evidence | always; may be empty | Facts, files, or symbols cited by the finding |
| `suggested_actions` | array of string | when set | Suggested responses to the finding |
| `informational` | bool | when true | Indicates that the finding describes the graph and is not gradeable |
| `metrics` | object | when set | Machine-readable values used by the finding; consumers should accept integer or floating-point JSON numbers |

## Evidence object

| Field | Type | Present when | Meaning |
|---|---|---|---|
| `file` | string | when set | Repo-relative evidence file |
| `symbol` | string | when set | Fact name cited as a symbol |
| `fact` | string | when set | Fact name cited without assuming a fact kind |
| `detail` | string | when set | One-line description of the evidence |
| `line` / `end_line` / `column` / `end_column` | int | when measured | Source span recorded by the producer |
| `fact_id` | string, 32 lowercase hex chars | when resolved and supported by the writer | ID from `facts.jsonl` — [Identity and IDs](facts.md#identity-and-ids) |

An evidence entry may contain `symbol`, `fact`, or both. Current writers resolve
`fact_id` as follows:

1. Use `symbol` when non-empty; otherwise use `fact` as the reference name.
2. If `file` is present and facts with that name and file exist, emit an ID only
   when those matches have the same identity.
3. Otherwise, emit an ID only when every fact with the reference name has the
   same identity.

Use `fact_id` when present. When absent, preserve the readable reference fields.
Absence can mean that the reference is ambiguous, outside the snapshot, or
intentionally missing. For example, a finding about an undefined handler cites
a name for which no fact exists.
