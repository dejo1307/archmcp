# Documentation

Start with the page closest to what you need:

- **New to Enola?** Read the main [README](../README.md).
- **Installing, configuring or scripting it?** Use [CLI.md](CLI.md).
- **Using Rails specifically?** Follow [RAILS.md](RAILS.md).
- **Understanding the engine?** Read [ARCHITECTURE.md](../ARCHITECTURE.md).

## Using Enola

| Page | Covers |
|---|---|
| [CLI.md](CLI.md) | Installation, agent integration, commands, flags, exit codes, scope controls and the dashboard. |
| [RAILS.md](RAILS.md) | The Rails workflow from installation through a graded change, with the output from each step. |

## Change analysis

| Page | Covers |
|---|---|
| [EXPLAINERS.md](EXPLAINERS.md) | The eighteen structural checks, confidence levels, and how before/after comparison isolates findings introduced by a change. |
| [SNAPSHOTS.md](SNAPSHOTS.md) | Why Enola computes addressable snapshots instead of maintaining one continuously updated graph. |
| [HISTORY.md](HISTORY.md) | `blame` and `diff` for finding when a structural change happened. |

## Evidence and limitations

| Page | Covers |
|---|---|
| [BENCHMARKS.md](BENCHMARKS.md) | Reproducibility, delta precision, cross-repository coverage and scale, measured on public repositories with scripts you can rerun. |
| [BLIND-SPOTS.md](BLIND-SPOTS.md) | Six reproducible failures against public codebases, including a bug found in Enola itself. |

## Architecture policy

| Page | Covers |
|---|---|
| [INTENT.md](INTENT.md) | Declaring services, layers, dependencies and cross-repository seams in repository, cluster or page metadata. |
| [CONSTRAINTS.md](CONSTRAINTS.md) | Components, architecture rules, enforcement modes, exemptions, recipes and Ruby-authored laws. |

## Extending the graph

| Page | Covers |
|---|---|
| [PROVIDERS.md](PROVIDERS.md) | Adding facts from Rubydex, runtime observations, RBS and Sorbet through the fail-closed provider interface. |
| [extraction/](extraction/README.md) | What each language extractor records, with examples from committed fixtures and its known limits. |
| [EXTENDING.md](EXTENDING.md) | Teaching Enola a connection it does not know through binders, cross-repository signals and `linking:` configuration. |

## Reference

| Page | Covers |
|---|---|
| [GLOSSARY.md](GLOSSARY.md) | Terms used in Enola output, including findings, baselines, receipts, coverage gaps and incidental shifts. |
| [ARCHITECTURE.md](../ARCHITECTURE.md) | The fact model, pipeline, graph, MCP tools and value model. |
| [CHANGELOG.md](../CHANGELOG.md) | Every released version, newest first. |

## Contributing

[CONTRIBUTING.md](../CONTRIBUTING.md) covers the development workflow. Documentation has two build-enforced rules:

- Counts and backticked repository paths are checked against the code. Historical measurements that intentionally keep an older count are waived by name and reason in `internal/docslint`.
- Every page under `docs/extraction/` must state its limits, and the extraction index must list every page.

Prose has no compiler; `internal/docslint` is the closest thing this repository has.
