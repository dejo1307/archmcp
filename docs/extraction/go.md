# Go — what enola extracts

Parsed with `go/ast` (not tree-sitter), so the Go graph is built from the same syntax
tree the compiler uses. Detected by a `go.mod` at the repository root.

Fixtures: [`go_sample`](../../internal/engine/testdata/repos/go_sample/) ·
[`go_httpclient_multirepo`](../../internal/engine/testdata/repos/go_httpclient_multirepo/) ·
[`go_grpc_multirepo`](../../internal/engine/testdata/repos/go_grpc_multirepo/) ·
[`go_kafka_multirepo`](../../internal/engine/testdata/repos/go_kafka_multirepo/)

## At a glance

| You write | enola stores | Kind |
|---|---|---|
| a package directory | one module, keyed by its path | `module` |
| `func`, `type`, `method` | one symbol with `symbol_kind`, `exported`, `cyclomatic` | `symbol` |
| `import "..."` | a dependency tagged `internal` / `external` / `stdlib` | `dependency` |
| a call inside a function | a `calls` relation on the calling symbol | relation |
| a call inside a `for` | the callee also in `calls_in_loop` / `calls_in_scaling_loop` | props |
| `r.HandleFunc("/x", h)` | a server route, **at its composed runtime path** | `route` |
| `http.Get(base + "/x")` | a client route with `role: client` and a resolved `host` | `route` |
| a `.proto` service + generated client | both sides of a gRPC edge | `route` |
| a Kafka topic constant | a topic, attributed to the service that owns it | `storage` |
| `*_test.go` | a reference-only `test_ref`, so test-only callees are not dead | `test_ref` |

## Modules, symbols and imports

```go
// pkg/a/a.go
package a

import "example.com/gosample/pkg/b"

func Alpha() {
	b.Beta()
}
```

```
module      pkg/a                          props: package=a, language=go
symbol      pkg/a.Alpha    pkg/a/a.go:7    props: symbol_kind=function, exported=true, cyclomatic=1
                                           --calls--> pkg/b.Beta
                                           --declares--> pkg/a
dependency  pkg/a -> example.com/gosample/pkg/b   pkg/a/a.go:3   props: source=internal
                                           --imports--> pkg/b
```

`source` separates `internal` (your own module path), `external` (a third-party module)
and `stdlib`. Only `internal` edges take part in cycle detection — a package that
imports `net/http` is not coupled to anything you own.

### Cycles

`go_sample` closes an `a` → `b` → `a` import loop on purpose. Tarjan's SCC over the
`internal` dependency edges reports it at confidence `1.0`, which is the one finding
[`enola check`](../CLI.md) fails a build on.

### Loops, for N+1 hunting

```go
func GetPath(id int) {
	for {
		getByID(id)
	}
}

func Seed() {
	for _, c := range []string{"a", "b"} {
		setup(c)
	}
}
```

```
symbol pkg/a.GetPath   props: loop_count=1, loop_depth=1, scaling_loop_depth=0,
                              calls_in_loop=[pkg/a.getByID],
                              calls_in_scaling_loop=[pkg/a.getByID]
symbol pkg/a.Seed      props: loop_count=1, loop_depth=1, scaling_loop_depth=0,
                              calls_in_loop=[pkg/a.setup],
                              calls_in_scaling_loop=[]        ← empty, not absent
```

A bare `for {}` walking a parent chain repeats without adding a factor of *n*, so
`getByID` stays in the N+1 candidate set. `Seed`'s loop is bounded by a literal slice,
so its `calls_in_scaling_loop` is emitted **empty rather than omitted** — an omitted key
would make a consumer fall back to the unfiltered list and re-report the bounded call.

### Test references

`*_test.go` is ignored for indexing, so a function called only from a test would look
dead. Both Go test idioms are recovered as `test_ref` facts that carry the call edge
without being architecture:

```
test_ref  pkg/a/a_test.go       --calls--> pkg/a.helper     (in-package test)
test_ref  pkg/a/a_ext_test.go   --calls--> pkg/a.Gamma      (package a_test, via import alias)
```

## Routes — prefix composition across function boundaries

This is the part that decides whether a client call resolves at all.

```go
// api/server.go
func main() {
	r := mux.NewRouter()
	v1 := r.PathPrefix("/v1").Subrouter()   // line 16
	registerThings(v1)                      // line 17 — the subrouter crosses a call boundary
	http.ListenAndServe(":8080", r)
}

func registerThings(r *mux.Router) {
	r.HandleFunc("/things/{id}", getThing).Methods("GET")   // line 24 — bare leaf path
}
```

Neither function contains the served path. The fact does:

```
route  /v1/things/{id}   api/server.go:24
       props: framework=gorilla/mux, method=GET, handler=getThing
       --handled_by--> ..getThing
```

The route is **stored at `/v1/things/{id}`**, not at the `/things/{id}` that appears on
line 24. Composition runs as a module-wide fixpoint, so a mount can be declared in one
function, one file, or one package and consumed in another. The same machinery covers
chi's `r.Mount` / `r.Route`.

Why it matters: a client calling `/v1/things/{id}` matches the composed path and nothing
else. Store the bare path and the edge silently does not exist — and a missing cross-repo
edge looks exactly like a service with no dependents.

## Outbound HTTP calls

```go
const extBase = "https://api.example.com"       // third-party, no loaded server
const internalBase = "http://api:8080"          // an internal host that IS a loaded repo

func (c *Client) run(ctx context.Context) {
	http.Get(extBase + "/v1/widgets")
	http.NewRequestWithContext(ctx, "GET", internalBase+"/v1/things/{id}", nil)
	http.NewRequest("POST", c.zepto.baseURL+"/v3/messages", nil)   // region switch: several hosts
	http.Get(c.options.BaseURL + "/v1/internal")                   // host injected from config
}
```

Four call sites, four different outcomes — and enola distinguishes them:

| Call | Stored as | Why |
|---|---|---|
| `/v1/widgets` | `role=client, external=true, host=api.example.com` | literal host, no loaded server |
| `/v1/things/{id}` | `role=client, external=true, host=api:8080` **and resolved to `api`** | internal host that matches a loaded repo — the server match is attempted before the external bucket |
| `/v3/messages` | `role=client, external=true`, **no `host`** | the base URL is bound to several literals that disagree on host (a region switch) |
| `/v1/internal` | `unmatched_by_server=true, unmatched_reason=path_unknown` | the base URL comes from injected config with no string-literal binding |

The service fact carries the tally, which is what `enola coverage` reports:

```
service consumer  props: edge_coverage=[{edge_type: http_client,
                                         detected: 4, resolved: 1, external: 2, unresolved: 1}]
                  --depends_on--> api
```

One unresolved out of four, stated rather than hidden. A report that only showed the
resolved count would be advertising.

## gRPC

A `.proto` service and a generated Go client become the two ends of one edge:

```
route  /users.v1.UserService/GetUser   server/proto/users/v1/users.proto:11
       props: role=server, source=grpc-proto, rpc_service=users.v1.UserService,
              rpc_method=GetUser, streaming=none
route  /users.v1.UserService/GetUser   client/repo.go:16
       props: role=client, source=go-grpc-client

dependency client -> server
       props: type=cross_repo, via=[grpc], confidence=verified, endpoint_count=2
```

`confidence=verified` — not `probable`. A gRPC edge is matched against a declared
contract, so it is not a heuristic the way a URL-string match is.

## Kafka

```
storage  svc-orders.order_placed        svc-billing/consumer.go:10
         props: storage_kind=topic, messaging=kafka, source=config_default
storage  analytics-sink.rows_exported   svc-billing/consumer.go:22
         props: storage_kind=topic, messaging=kafka, source=env_default
```

Topics are namespaced by the service that **owns** them, not the one that mentions
them, so a consumer reading `svc-orders.order_placed` produces a `depends_on` edge to
`svc-orders` even though the two share no import. `source` records where the topic name
came from (a config default, an env default), because a topic assembled at runtime is a
weaker claim than a constant.

## What is deliberately not extracted

- **Dynamically built paths.** `r.HandleFunc(pathVar, h)` where `pathVar` is not a
  literal is skipped rather than guessed. Same for a client URL with no literal binding.
- **Mounts resolved across files by variable aliasing** beyond the fixpoint's reach —
  the leaf keeps its bare path rather than being dropped, so it is still visible and
  still shows up as an unresolved client edge.
- **Absolute URLs to hosts you have not loaded.** These are tagged `external` with the
  host recorded, never invented as an internal edge.
- **Reflection and `interface{}` dispatch.** A call through an interface resolves to the
  interface method, not to every implementation.

Every one of these shows up as an unresolved count in
[`enola coverage`](../CLI.md), never as a confident wrong edge.

---

Measured on real Go repositories: [BENCHMARKS.md](../BENCHMARKS.md).
The full detection rules: [ARCHITECTURE.md → Supported languages](../../ARCHITECTURE.md#supported-languages).
