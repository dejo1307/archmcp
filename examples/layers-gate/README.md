# The gate, on five packages

A module built to a layer order, and one change that breaks it. Small enough to read in
a minute, and it is the fixture the outputs in the top-level README come from.

```bash
./run.sh          # or: ENOLA=../../enola ./run.sh
```

## The module

```
web/        delivery  ─┐
notify/     delivery  ─┤ may depend inwards
api/        api       ─┤
storage/    storage   ─┘ depends on nothing above it

telemetry/  (outside the layer order — nothing declares it, nothing depends on it)
```

The order is not inferred. It is stated, in [`enola-intent.yaml`](enola-intent.yaml):

```yaml
layers:
  - {name: delivery, paths: ["web/**", "notify/**"]}
  - {name: api,      paths: ["api/**"]}
  - {name: storage,  paths: ["storage/**"]}
```

**Stating it is what makes the verdict exact.** A layer order enola recognises for
itself caps at confidence `0.80`, because a recognised pattern is a guess about your
intent. A declared one is verdicted at `1.00` — and `1.00` is the floor `enola check`
gates at, so only a declared order is one you can put in front of a build.

## The change

`storage` sends the buyer a receipt. One import, entirely reasonable-looking in review,
and it is the innermost layer reaching out to the outermost:

```go
 package storage

+import "layersgate/notify"
+
+func LoadPrice(item, buyer string) int {
+	price := ReadPrice(item)
+	notify.SendReceipt(buyer, item)
+	return price
+}
```

Nothing about this file looks wrong on its own. That is the point: the defect is the
edge, not the line, and the edge is only visible against the order the module declared.

`run.sh` also appends a function to `telemetry/` — a package the change was never
described as touching. It exists for the third run below.

## The three runs

| | Enforces | Exit |
|---|---|---|
| `enola check` | nothing. Reports the violation and says no policy is set | `0` |
| `enola check --fail-on=layers` | the declared layer order | `1` |
| `… --target=storage --max-spillover=0` | the layer order **and** the scope its author declared | `1` |

The first is the default, and it is deliberately toothless: enola measures your
architecture, and what counts as a regression in it is yours to state. The second states
it. The third adds a bound that is not a finding at all — `--target=storage` says *this
change is about storage*, and the edit in `telemetry/` is a package the description did
not cover.

Note what is **not** in any of these runs: a dependency cycle. There is no cycle in this
module, and the gate never mentions one. Cycles are one of the twelve things enola
measures, available to `--fail-on` like the rest — not a rule it arrives holding.
