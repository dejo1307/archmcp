# What enola extracts, per language

These pages answer one question: **given this code, what ends up in the graph?**

Every example is a file that ships in this repository, and every fact shown is copied
from the golden file the test suite asserts against. So none of it is a description of
intended behaviour — it is the behaviour, and if an extractor changes without these
pages changing, the golden tests fail first.

| | |
|---|---|
| Fixture sources | [`internal/engine/testdata/repos/`](../../internal/engine/testdata/repos/) |
| Expected facts | [`internal/engine/testdata/golden/`](../../internal/engine/testdata/golden/) |
| Measured on real repositories | [BENCHMARKS.md](../BENCHMARKS.md) |

## The pages

| Language | Routes and clients it understands | |
|---|---|---|
| [Go](go.md) | gorilla/mux, chi, Gin, Echo, `net/http` clients, gRPC, Kafka | prefix composition across function boundaries |
| [TypeScript / JavaScript](typescript.md) | Express, NestJS, Next.js, `fetch`, axios, Prisma, TypeORM, Drizzle | Vue, Svelte, and file-based routing |
| [Python](python.md) | FastAPI, Flask, Django, SQLAlchemy, gRPC | `include_router` prefixes folded repo-wide |
| [Ruby](ruby.md) | Rails `routes.rb`, ActiveRecord, Packwerk | nested `resource`/`resources` path shapes |
| [Java](java.md) | Spring MVC, RestTemplate, Feign, JPA, Dubbo SPI | |
| [Kotlin](kotlin.md) | Retrofit, Room, Compose, Hilt | |
| [Swift](swift.md) | URLSession, SwiftUI, UIKit | endpoint enums, protocol-extension prefixes |
| [PHP](php.md) | Laravel, Symfony, WordPress, Guzzle | `apiResource` expansion, YAML route config |
| [Rust](rust.md) | Axum route DSL | `.nest()` mounts composed crate-wide |
| [C / C++](cpp.md) | — | header/source method merging, namespaces, templates |
| [gRPC and OpenAPI](grpc-openapi.md) | `.proto` services, OpenAPI specs | the contract as the server side of an edge |

## How to read a page

Each one is organized the same way:

1. **At a glance** — a table from source construct to fact kind.
2. **What each construct produces** — the code, then the facts, then the query it unlocks.
3. **What is deliberately not extracted** — the limits, stated next to the capability.

That last section is not an apology. A missing edge shows up in `enola coverage` as an
unresolved count you can go and look at; a *wrong* edge is invisible and gets acted on.
Every extractor here reports the gap rather than inventing the edge, and the section
says where those gaps are.

## The fact model in one paragraph

Everything below is one of six kinds — `module`, `symbol`, `route`, `storage`,
`dependency`, `service` — plus two reference-only kinds (`file_ref`, `test_ref`) that
carry edges without being architecture themselves. Facts are name-keyed, carry a
`file:line`, and hold typed relations (`imports`, `calls`, `declares`, `handled_by`,
`depends_on`, …). [ARCHITECTURE.md](../../ARCHITECTURE.md#the-fact-model) has the full
model; these pages assume it only loosely.
