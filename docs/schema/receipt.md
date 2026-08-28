# receipt.json

A single JSON object: the compact, machine-readable manifest of what a
snapshot was generated over and how complete the extraction was. It is a
projection of `snapshot.meta.json` (the internal superset, which adds the
per-file hash list and is not a stable contract).

Do not confuse it with `~/.enola/receipt.json` — the graph-wide manifest of
the current multi-repo "graph of graphs" (which repos compose it, when each
entered). That file is machine-global state, not a per-snapshot artifact.

## Fields

| Field | Type | Meaning |
|---|---|---|
| `snapshot_id` | string, `"sha256:..."` | Content fingerprint: SHA-256 over the byte-stable fact serialization plus the enola version and effective config hash. Same inputs → same ID, on any machine |
| `format_version` | int | The generation of the artifact format this receipt describes — [README, Versioning](README.md#versioning). Current: 1. Zero means not stamped by the current writer; treat as unknown |
| `enola_version` | string | The build that produced the snapshot (`dev` for local builds) |
| `extractor_version` | string, when set | The extraction BEHAVIOUR — bumped whenever an extractor starts reading something differently. Differs from `enola_version` for every local build, where the latter is the constant `dev` |
| `generated_at` | string, RFC3339 | When the snapshot was written |
| `duration` | string | How long generation took |
| `repo_path` | string | Absolute path of the analyzed repository |
| `git` | object, when a git repo | `{ref, commit, dirty, remote}` — the VCS state at snapshot time. `remote` is normalized to a comparable identity (`github.com/org/repo` — no scheme, credentials, port or `.git` suffix) |
| `extractors` / `explainers` / `renderers` | array of string | The plugin sets actually used |
| `providers` | array, when set | One record per configured external fact provider. `name`, `fact_count`, and `version` / `skipped` / `reason` when set are the contract — skips are recorded rather than dropped. A record may carry further diagnostic counters (cache reuse, a per-provider census, agreement with the extractor's own reading of the same call sites); those are reporting detail, not contract, and a consumer must not branch on them |
| `config_hash` | string, `"sha256:..."`, when set | Hash of the effective configuration (extractors, explainers, renderers, globs, output). A differing hash between two snapshots explains churn that is a config change rather than a code change |
| `ignore_glob_hash` | string, `"sha256:..."`, when set | Hash of the sorted ignore+test globs (a subset of `config_hash`) |
| `output_hashes` | object, when set | Artifact name → `"sha256:"` hash of its written bytes (`facts.jsonl`, `insights.json`, renderer outputs) |
| `fact_count` / `insight_count` | int | Totals in this snapshot |
| `quality` | object | Extraction-completeness metrics — below |

## quality

| Field | Meaning |
|---|---|
| `files_seen` | Source files the walker enumerated (excludes ignored) |
| `files_parsed` | Distinct files that produced at least one fact |
| `files_skipped` / `dirs_skipped` | Ignored files visited / ignored directories pruned whole (their contents are counted nowhere else) |
| `skipped_sample` | A capped sample of both, each naming the glob that matched it |
| `parse_errors` | Count of extractor detect/parse failures (non-fatal) |
| `parse_error_sample` | A capped sample: `{extractor, file?, msg}` |
| `heuristic_insights` | Count of insights with confidence < 1.0 (heuristics, vs structural findings) |
| `coverage` | object, multi-repo mode only — `{services_total, coverage_gaps, unresolved_edges, external_edges?, extractors_reporting?, extraction_unresolved?}`: the cross-repo edge-coverage rollup |
| `census` | object — per-file walk accounting: `{files_walked, parsed, excluded_by_ignore, excluded_by_kind, excluded_kinds?, skipped_with_cause, top_skip_causes?}`. The buckets sum back to `files_walked`, so no file can quietly fall out of the account |

The receipt's own field names are pinned by `internal/facts/wireformat_test.go`,
like the other artifacts': renaming one — `format_version` above all, which is
how a consumer decides whether it can read the rest — fails the build.

## Reading quality

A consumer that trusts a graph should read `quality` first: `parse_errors > 0`,
a small `files_parsed` against `files_seen`, or a non-empty `excluded_kinds`
names the files and languages this graph is blind to. A zero everywhere is a
real answer ("could not see: nothing"), which is why the object is written even
then.
