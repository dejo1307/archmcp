# The documentation

Every page in this tree, and the question it answers. If you are looking for something
and cannot tell which page has it, this is the map.

The two pages outside this directory come first, because most readers need them first:
[README.md](../README.md) is what enola is and why you would run it, and
[ARCHITECTURE.md](../ARCHITECTURE.md) is the concept, the fact model, the pipeline, the
MCP tool reference and the value model.
[CHANGELOG.md](../CHANGELOG.md) is every released version, newest first.

## Getting it running

| Page | The question it answers |
|---|---|
| [CLI.md](CLI.md) | How do I install it, connect it to an agent, and what does every command and flag do? Includes the gate, the exit codes and the scope flags. |
| [RAILS.md](RAILS.md) | I have a Rails application — what do I actually do? The workflow end to end, with the output each step prints. |

## What it finds, and what that is worth

| Page | The question it answers |
|---|---|
| [EXPLAINERS.md](EXPLAINERS.md) | What do the eighteen explainers compute, why is a derived finding still not a verdict, and how does a delta turn thousands of findings about a corpus into the one about your change? |
| [SNAPSHOTS.md](SNAPSHOTS.md) | Why compute the graph on demand and keep it as an addressable value, rather than maintaining one continuously-updated graph? |
| [HISTORY.md](HISTORY.md) | When did this happen? The question a single snapshot structurally cannot answer. |
| [BLIND-SPOTS.md](BLIND-SPOTS.md) | What can an agent not see? Six reproducible failures against five public codebases, each with the commit and the command. One of the six is a bug in enola. |
| [BENCHMARKS.md](BENCHMARKS.md) | Reproducibility, delta precision, cross-repo coverage and scale, measured on 91 public repositories by scripts you can re-run. |

## Declaring what you meant

| Page | The question it answers |
|---|---|
| [INTENT.md](INTENT.md) | Where does a declaration live, what exactly does enola read, and how do verdicts behave? The three carriers, the repo/cluster/page schema, and the closed vocabularies. |
| [CONSTRAINTS.md](CONSTRAINTS.md) | What is this repository **not allowed to do**? Components and their selectors, the 21 rule forms, modes, exemptions, recipes you bind or author, laws written in Ruby, and the `constraints` and `plan` surfaces. |
| [PROVIDERS.md](PROVIDERS.md) | How does a fact enola did not extract get into the graph? The fail-closed seam, and the Rubydex, runtime and RBS/Sorbet providers. |

## Reference

| Page | The question it answers |
|---|---|
| [GLOSSARY.md](GLOSSARY.md) | What does enola mean by *finding*, *baseline*, *receipt*, *coverage gap*, *incidental shift*? The vocabulary that means something specific here. |
| [extraction/](extraction/README.md) | Per language, what does this specific code produce in the graph — and what does the extractor deliberately not resolve? One page per language, every example from a committed fixture. |
| [EXTENDING.md](EXTENDING.md) | How do I teach enola a connection it does not know? Binders, cross-repo signals, and the `linking:` vocabulary that fixes a wrong edge from configuration rather than a patch. |

## If you are changing enola

[CONTRIBUTING.md](../CONTRIBUTING.md) covers the workflow. Two things in this tree are
enforced rather than suggested, and both fail the build:

- **Counts and paths in prose are checked against the code.** A page claiming a number
  of explainers, MCP tools or rule forms is verdicted against the live inventory, and a
  backticked path into this repository must exist. A number that is deliberately frozen —
  a measurement taken when the inventory was smaller — is waived by name, with its
  reason, in `internal/docslint`.
- **An extraction page must state its limits.** Every page under `docs/extraction/` needs
  the section its own index promises, and the index must list every page.

Prose has no compiler; `internal/docslint` is the closest thing this repository has.
