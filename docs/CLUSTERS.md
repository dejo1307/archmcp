# Two services, one graph

*What it takes to see an edge that neither repository contains, and how enola reports the
ones it could not see.*

A single repository is the easy case: everything a dependency needs is in the tree you
indexed. Across services it is not. The `web` service calls the `api` service, and
**nothing in either repository says so** — the call is a string, the route is a
registration, and they were written months apart by different people.

This walks the whole thing on two services small enough to read. They are
[`examples/cross-repo/`](../examples/cross-repo/), and
[`examples/cross-repo/run.sh`](../examples/cross-repo/run.sh) runs it.

---

## 1. Name the cluster

One config, listing repositories rather than describing one:

```yaml
# cluster.yaml — paths resolve relative to THIS file, so the config means the
# same thing wherever it is run from.
repos:
  - api
  - web
```

That is the entire difference between a repository run and a cluster run. Every command
that takes a repository takes this instead: **a directory is a repository, a file is a
config.**

```
$ enola --generate cluster.yaml

Snapshot complete:
  Repositories: 2
    - ./api
    - ./web
  Facts:       20
  Insights:    3
  Artifacts:   1
  Duration:    14.396792ms
  Output:      ./web/.enola
```

**Note where the output landed.** A multi-repo run writes the full union to the *last*
repository in the config. Each repo also keeps its own artifacts, but reading the first
one gets you a partial graph and no cross-repo edges at all — a mistake that looks like
enola failing to link anything.

## 2. What makes this hard

Here is the route the `api` service registers:

```go
func main() {
	r := mux.NewRouter()
	v2 := r.PathPrefix("/api/v2").Subrouter()
	registerOrders(v2)                        // the prefix is passed, not written
	http.ListenAndServe(":8080", r)
}

func registerOrders(r *mux.Router) {
	r.HandleFunc("/orders/{id}", getOrder).Methods("GET")
}
```

Read `registerOrders` on its own and the service appears to serve `/orders/{id}`. It does
not. It serves **`/api/v2/orders/{id}`**, which is what the client calls.

The prefix and the leaf path are never written together, and neither function contains
the answer. Recovering it means composing the prefix **across the call boundary**. Match
on the bare path and the client's call resolves to nothing; match on the wrong composed
path and it resolves to something wrong, which is worse — a missing edge is a gap, and a
wrong edge is acted upon.

So the routes are stored at the path they are actually served at:

```
route  /api/v2/orders       api/server.go   handler=listOrders  matched_by_clients=true
route  /api/v2/orders/{id}  api/server.go   handler=getOrder    matched_by_clients=true
```

and the edge exists:

```
dependency  web -> api   type=cross_repo  confidence=verified  via=[http-client]
                         endpoints=[GET /api/v2/orders, GET /api/v2/orders/{id}]
```

The same shape appears in Axum's `.nest()`, FastAPI's `include_router(prefix=…)`, Rails'
`scope`/`namespace`, and Swift endpoint enums whose version prefix lives in a protocol
extension several files away. [EXTENDING.md](EXTENDING.md) covers teaching enola a
connection it does not already know.

## 3. Ask what it missed

A resolved-edge count on its own is not worth much, because you cannot tell a service
with no outbound calls from one whose calls enola failed to follow. `coverage` reports
both, per service:

```
$ enola coverage cluster.yaml
Cross-repo edge coverage

  service  classification  detected  resolved  unresolved
  api      isolated              0         0           0
  web      connected             3         2           1

Unresolved outbound call sites (1 across 1 service):
  web                          http_client ×1

Each is a call enola detected but could not point at a loaded service — either a
repository you have not snapshotted, a third-party endpoint, or a blind spot in
enola's extraction. Load the missing repository and re-run to tell them apart.
```

`api` is **isolated** — it detected no outbound calls, which for a service that only
serves is correct. `web` is **connected**, with one of its three call sites unresolved.

That unresolved one is deliberate:

```go
func fetchDynamic(tenant string) {
	http.Get(apiBase + "/api/v2/" + tenant + "/orders")
}
```

The path is assembled at runtime from a value enola cannot see, so there is no path to
match against any route. enola records that an outbound call happened and reports it
unresolved rather than guessing. **The unresolved list is what makes the resolved count
worth believing** — it is always printed, and `coverage` always exits `0`, because it is a
report and not a gate.

Three things put an entry on that list, and they need different responses:

| Cause | What to do |
|---|---|
| A repository you have not loaded | Add it to `repos:` and re-run — the count moves |
| A genuinely third-party endpoint | Nothing; it is correct |
| A blind spot in enola's extraction | [BLIND-SPOTS.md](BLIND-SPOTS.md) records how these are found, including one found in enola itself |

Load the missing repository and re-run: that is the one experiment that distinguishes the
first from the other two.

## 4. Grade a change across the seam

Everything in [FIRST-CHANGE.md](FIRST-CHANGE.md) works here, with the cluster config in
place of the repository path:

```bash
enola baseline pin cluster.yaml     # every member must agree on one union
#   …change the api service's routes…
enola check cluster.yaml
```

This is where a cluster earns its cost. Rename a route in `api` and the verdict names the
client that called it, in the other repository, which no test in either one would have
caught — the api's tests pass, the web service's tests pass, and the call is broken.
`enola endpoint 'GET /api/v2/orders' cluster.yaml` asks the same question directly, without a
baseline.

A pair of explainers only mean anything over a cluster: `unused-routes` reports backend
routes that no loaded client calls, and `crossrepo` reports the seams themselves. The
first is only trustworthy when coverage is high — an unresolved client call site and an
unused route are the same fact seen from the two ends.

---

## What is deliberately not linked

enola links what it can *demonstrate*. Two services that share a type name are not
thereby coupled: a shared name with no import and no call becomes a symmetric
`cross_repo_shared_code` observation with no direction, verified against the declaring
files rather than the names alone. It is reported as coupling to look at, never as a
dependency, because a dependency is a claim about what breaks when you change something.

The full rules — what links, what does not, and how to tune it — are in
[ARCHITECTURE.md → Cross-repo](../ARCHITECTURE.md#cross-repo-the-graph-of-graphs) and
[EXTENDING.md](EXTENDING.md).

---

## Where to go next

| If you want | Read |
|---|---|
| The single-repository loop first | [FIRST-CHANGE.md](FIRST-CHANGE.md) |
| Coverage, scale and precision measured on public repositories | [BENCHMARKS.md](BENCHMARKS.md) |
| To teach enola a link it does not know | [EXTENDING.md](EXTENDING.md) |
| Declaring seams instead of inferring them | [INTENT.md](INTENT.md) |
| Every flag `coverage` takes | [CLI.md](CLI.md#command-line-reference) |
