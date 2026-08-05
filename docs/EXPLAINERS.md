# A finding is not a verdict

Tarjan's strongly-connected-components algorithm is textbook. It is older than most of
the languages enola parses, it fits on a page, and enola's implementation of it is not
where the work went.

The work went into the graph it runs on, and into what comes back. Both are worth
separating from the algorithm, because they are what a finding actually rests on.

**The graph is derived, not retrieved.** An import edge exists because a route prefix
was composed across a function and package boundary, a Kotlin `import a.b.C` was
resolved to the module `a/b`, a C++ header was merged with the source that defines its
methods, or an ActiveRecord association was deliberately *excluded* so that a
bidirectional Rails relation would not manufacture a cycle that isn't there. Run a
textbook algorithm over a graph assembled carelessly and you get textbook answers to
the wrong question.

**And a finding is an answer, not a lead.** enola does not hand an agent a set of
plausibly-relevant files and let it work out whether they form a cycle. It hands back
the conclusion — these modules, this cycle, confidence `1.0`, computed by a real
algorithm over real edges — so nothing downstream has to re-derive it, and two agents
asking the same question get the same answer. Nothing in that chain is retrieved by
similarity or guessed by a model; see
[ARCHITECTURE.md → The idea](../ARCHITECTURE.md#the-idea) for why that is the founding
constraint rather than an optimisation.

All of which buys a finding you can trust. It does not buy a verdict — and the gap
between those two is what the rest of this document is about.

Across the 38 public repositories in [BENCHMARKS.md](BENCHMARKS.md), enola's ten
explainers produced **24,012 findings**. Every one of them is derived rather than
guessed, and that number is still the problem rather than the achievement. Nobody is
going to read 24,012 findings. Nobody is going to fix them. A report that hands you all
of them has told you something true and useless in the same breath, and the honest
response to it is to close the tab.

So the question that decides whether any of this is worth running is not *what can you
find*. It is **what happens to a finding after you have found it** — and that depends
entirely on the thing [SNAPSHOTS.md](SNAPSHOTS.md) describes: whether your graph is a
value you can compare against another one, or a picture of right now.

## Eleven explainers: two proofs and nine estimates

An explainer reads the fact graph and emits **findings** — a claim, a confidence, and
the entities the claim is about. There are eleven, and they fall into five kinds:

- **The proofs.** `cycles` runs Tarjan's SCC over the resolved import edges — a cycle
  either exists or it does not. `intent` diffs DECLARED architecture (a repo's
  enola-intent.yaml, or a cluster config's intent block) against the measured
  cross-repo edges: an unexpected or mis-mechanism seam is set difference between
  stated and measured, which either holds or it does not. Its one estimating verdict —
  a declared seam the graph never measured — is capped below 1.0, because the absence
  can be drift or an extraction miss.
- **Outlier tests.** `god-class`, `hotspots` and `complexity-outliers` compute a
  distribution over your repository and flag what sits above `mean + 2σ`.
- **Graph shape.** `dependency-depth` measures the longest transitive import chain;
  `exported-surface` flags large modules that export nearly everything.
- **Convention matching.** `layers` recognises an architecture by matching module paths
  against eight known taxonomies, then flags imports that run the wrong way through it.
  A repo that DECLARES its layer order skips the recognition: the declared pattern is
  stated, not guessed, and its violations are proof-class.
- **Reporters.** `crossrepo`, `coverage` and `unused-routes` compute nothing of their
  own; they summarise what the cross-repo linker already resolved — which repositories
  depend on which, where enola failed to follow a call, and which routes no loaded
  client calls.

What each one computes, every threshold it uses and what it deliberately ignores is in
[ARCHITECTURE.md → Insights](../ARCHITECTURE.md#insights-explainers). The distinction
that matters here is smaller and blunter: **two of the eleven prove something. The
other nine estimate.** A cycle is a fact about your import graph. A god class is an opinion
about your repository, expressed as a number, and reasonable people can disagree with
it.

That distinction is carried in the confidence score, and it is exact rather than
decorative: `1.0` means proven, and only `cycles`, `intent`'s set-difference verdicts,
and declared-layer violations ever reach it. Every explainer that
computes a saturating score — a fan-in ratio, a coverage share — clamps strictly below
`1.0`, so a statistical outlier can never present itself as a certainty.

## A list of everything wrong is not a list of anything you did

Here is the shape of the problem, from the corpus. Twelve repositories, before anyone
changed a line:

> **974 pre-existing findings.** Up to 159 in a single repository.

Now make a change. Add a feature, fix a bug, let an agent refactor a package. Two
questions look similar and are not:

1. *What is wrong with this repository?* — 974 answers, none of them yours.
2. *What did my change do to it?* — the question you can actually act on.

Almost every one of those 974 findings was there before you opened the editor. You did
not write them, you are not going to fix them today, and a tool that reports them
alongside your change has buried the one thing you needed in 973 things you did not.
This is how structural analysis dies in practice: not because the analysis is wrong, but
because the signal-to-noise ratio makes the honest response *turn it off*.

Answering question 2 requires something the analysis alone cannot give you. It requires
the state of the repository *before* your change — still intact, still queryable, after
the change has landed.

## Which is exactly what a snapshot is for

[SNAPSHOTS.md](SNAPSHOTS.md) makes the case that enola's graph is a **value**: computed
when you ask, named by a fingerprint of its contents, kept rather than overwritten. This
is what that buys.

Because the *before* still exists as a thing you can hold, a finding can be located in
time. And because every finding carries the entities it is about — the modules in the
cycle, the symbol with the fan-in, the dependency edge that crossed a layer — a finding
can be checked against what your change actually touched.

Put those together and the report inverts. Instead of *974 findings, one of which might
be new*, you get:

> **FAIL — 1 structural regression introduced.**

Measured, on those same twelve repositories: an injected dependency cycle was reported
as **exactly one regression, and not one of the 974 pre-existing findings was repeated**
([BENCHMARKS.md § 2](BENCHMARKS.md#2-delta-precision--the-ratchet)). Revert the change
and it goes quiet again. The verdict is a function of the tree, not of history.

That is the whole trick, and it is not a smarter explainer. It is the same ten
explainers run twice, over two values that both still exist.

## Three answers, and the third is the interesting one

Comparing findings across two snapshots gives three outcomes, not two:

- **Regression introduced** — the finding is new, *and* it cites something your change
  touched.
- **Improvement** — the finding was there and is gone, for the same reason.
- **Incidental shift** — the finding appeared or cleared, but nothing it cites was
  touched by your change.

That third bucket is small, unglamorous, and the reason the gate stays switched on.

Nine of the ten explainers are relative to your repository. `mean + 2σ` moves when the
population moves. A ranked top-N list has fixed membership size, so when a worse
offender is deleted the next module rises into the window — and a finding "appears" for
a module nobody edited. Both are real effects of statistics, not of your work.

A tool that reports those as regressions is lying to you twice a week, and you will
learn to ignore it. enola names them instead: they are listed under **incidental finding
shifts**, with the reason, and they are never graded. Nothing is hidden — but nothing
that drifted gets to masquerade as something you caused.

## Only one kind fails the build

Of the 24,012 findings across the corpus, **887 are cycles — 3.7%.** Only those are
eligible to fail a build. The other 96.3% are reported and let you through.

That ratio is the design, not an accident. A cycle is the one finding computed with
certainty rather than inferred, and it is consequential enough to be worth stopping for:
it dictates load order, it makes both modules untestable in isolation, and it raises the
price of every future refactor in that area. It is not a matter of taste.

A god class is a matter of taste. So is a deep dependency chain, a large public surface,
a complexity outlier. Those get reported so you can look, and they never break your
build — because a gate that fails on opinions is a gate that gets deleted from CI in
week two. Widen it if you disagree; the policy is a flag, not a belief.

## What it looks like when it works

The point of all this machinery is a single moment: an agent finishes a task, and
something asks *what did that do to the structure?* before it reports done.

In the headless A/B in [BENCHMARKS.md § 6](BENCHMARKS.md#6-agent-ab--does-the-agent-ship-the-regression),
an agent with no structural tooling shipped a dependency cycle on **3 of 3** trials —
TypeScript compiled it, every test passed, nothing complained. With the session hooks
installed, **0 of 3** did. From one transcript, unprompted:

> The regression was `src/domain ↔ src/store`: my first attempt made the domain
> `import { listOrders }` from the store, which closed a loop with the store's
> pre-existing `import type { Order }` from the domain. I removed the offending edge.

The agent introduced the cycle, was handed the verdict, and fixed it before saying it
was finished. Nine runs is a demonstration rather than a statistic, and the section says
so — but the mechanism is the argument, and the mechanism is the delta.

## What this does not give you

- **It will not tell you what to fix first.** Confidence is comparable *within* an
  explainer, not across them. A coverage gap at `0.9` and a layer violation at `0.8` are
  not ranked against each other, and enola does not pretend to a severity model.
- **96.3% of findings never stop anything.** If you want a god class to fail your build
  you have to say so explicitly. The default is deliberately narrow, and narrow means
  most of what enola finds is advisory.
- **Pre-existing problems stay silent by design.** The ratchet reports movement, not
  state. A repository can carry 159 findings forever and every `enola check` will pass.
  That is the right default for a gate and the wrong tool for paying down debt — for
  that, read the findings directly rather than the delta.
- **The heuristics are repo-relative.** A uniformly complex codebase reports nothing
  remarkable, because nothing in it is remarkable *for that codebase*. Findings compare
  you to yourself, never to an industry baseline.
- **The bulk of the volume is one explainer.** 20,625 of the 24,012 findings are
  hotspots, and unlike its siblings it has no output cap. "No new findings" is a
  statement about your change, never a certificate that a repository is clean.

---

Measured numbers: **[docs/BENCHMARKS.md](BENCHMARKS.md)** ·
What the words mean: **[docs/GLOSSARY.md](GLOSSARY.md)** ·
Why the graph is a snapshot: **[docs/SNAPSHOTS.md](SNAPSHOTS.md)** ·
Commands and flags: **[docs/CLI.md](CLI.md)** ·
How the engine works: **[ARCHITECTURE.md](../ARCHITECTURE.md)**
