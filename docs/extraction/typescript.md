# TypeScript / JavaScript — what enola extracts

Parsed with tree-sitter's TypeScript grammar, which handles JavaScript natively — there
is no separate JS extractor. Detected by `tsconfig.json`, `tsconfig.base.json`, or a
`package.json` with TypeScript, at the root or one level deep, so monorepos are picked up
per package. Vue, Svelte and Ember are the same extractor with their own
single-file-component / template handling.

Fixtures: [`ts_sample`](../../internal/engine/testdata/repos/ts_sample/) ·
[`ts_express_multirepo`](../../internal/engine/testdata/repos/ts_express_multirepo/) ·
[`ts_nest_multirepo`](../../internal/engine/testdata/repos/ts_nest_multirepo/) ·
[`ts_orm_sample`](../../internal/engine/testdata/repos/ts_orm_sample/) ·
[`ts_ember_sample`](../../internal/engine/testdata/repos/ts_ember_sample/)

## At a glance

| You write | enola stores | Kind |
|---|---|---|
| a source directory | one module per directory | `module` |
| `function`, `class`, `interface`, `const fn = …` | a symbol with `symbol_kind` | `symbol` |
| `import … from` | a dependency, `internal` / `external` | `dependency` |
| `app.get("/x", h)` (Express) | a server route | `route` |
| `@Get()` on a `@Controller("v2/slots")` | a server route at the **composed** path | `route` |
| `@controller(…)` (InversifyJS) | a server route | `route` |
| `fetch("/x")`, `axios.post("/y")` | a client route with `role: client` | `route` |
| `pages/about.vue` (Nuxt) | a route derived from the file path | `route` |
| `src/routes/blog/[slug]/+page.svelte` | a route derived from the file path | `route` |
| a Prisma / TypeORM / Drizzle model | an entity with its table name | `storage` |
| a `.gts`/`.gjs` Glimmer component | a component symbol with its template's references | `symbol` |
| `this.route('book', { path: '/:book_id' })` (Ember) | a page route at the composed path | `route` |
| an ember-data `Model` subclass | a model with its dasherized name | `storage` |
| top-level statements | a `file_ref` carrying the call edges | `file_ref` |
| `*.test.ts`, `*.spec.tsx` | a reference-only `test_ref` | `test_ref` |

## Routes — NestJS controller prefixes

```ts
// api/src/slots/slots.controller.ts
@Controller('v2/slots')
export class SlotsController {
  @Get('available')  getAvailableSlots() {}   // line 10
  @Post('reserve')   reserveSlot() {}         // line 15
}
```

```
route  /v2/slots/available   api/src/slots/slots.controller.ts:10
       props: framework=nestjs, method=GET, handler=getAvailableSlots, role=server
route  /v2/slots/reserve     api/src/slots/slots.controller.ts:15
       props: framework=nestjs, method=POST, handler=reserveSlot, role=server
```

The class-level prefix is folded onto each method decorator, so the stored path is the
one a client actually calls. InversifyJS `@controller` prefixes are handled the same way
(`/api/orders`, `/api/orders/:id/cancel` in the same fixture).

## Routes — Express

```
route  /healthcheck        server/index.js:8    props: framework=express, method=GET
route  /healthcheck        server/index.js:9    props: framework=express, method=OPTIONS
route  /go/:name           server/index.js:10   props: framework=express, method=GET,
                                                       unmatched_by_clients=true
route  /admin/users/:id/ban server/index.js:20  props: framework=express, method=POST
```

`unmatched_by_clients=true` is what the `unused-routes` explainer reports: an endpoint
this repository serves that no loaded client calls. It is a *candidate* at confidence
`0.6`, not a verdict — the client may simply not be in the graph.

## Client calls, and how a near-miss is reported

```ts
await axios.get('/healthcheck');
await axios.get('/admin/users');
await axios.post(`/admin/users/${id}/ban`);
await axios.get('/not/served/anywhere');
```

```
route  /healthcheck           props: role=client, framework=axios,
                                     unmatched_by_server=true, unmatched_reason=generic_path
route  /admin/users           props: role=client, framework=axios          → resolves
route  /admin/users/{}/ban    props: role=client, framework=axios          → resolves
route  /not/served/anywhere   props: role=client, framework=axios,
                                     unmatched_by_server=true, unmatched_reason=path_unknown
```

Two things worth noticing. A template literal becomes `{}`, which matches the server's
`:id` — that is how `/admin/users/${id}/ban` links to `/admin/users/:id/ban`. And the two
misses carry **different reasons**: `/healthcheck` is too generic to attribute to one
server with confidence, while `/not/served/anywhere` has no candidate at all. The service
tally is `detected: 4, resolved: 2, unresolved: 2` — reported, not rounded up.

## File-based routing

Nuxt pages and SvelteKit routes have no decorator to read; the path *is* the route.

```
pages/about.vue                          → route /about          framework=nuxt
src/routes/+page.svelte                  → route /               type=page
src/routes/about/+page.svelte            → route /about          type=page
src/routes/blog/[slug]/+page.svelte      → route /blog/[slug]    type=page
src/routes/api/users/+server.ts          → route /api/users      type=server, method=ALL
src/routes/(app)/dashboard/+page.svelte  → route /dashboard      route group stripped
src/routes/+layout.svelte                → route /               type=layout
```

Two deliberate non-routes, both covered by tests: `+page.server.ts` is a load function
rather than an endpoint and emits nothing, and a `.svelte` file outside `src/routes/`
(a component in `src/lib/`) is not a route.

## Ember — template tags, classic templates, and the resolver

Detected by an `ember-source` dependency in `package.json`. A Glimmer template-tag
file is TypeScript with embedded `<template>` blocks; the blocks are blanked in
place (newlines preserved) so the remainder parses with the standard grammar and
every fact's line number stays true to the original file:

```ts
// app/components/book-card.gts
import Component from '@glimmer/component';
import { service } from '@ember/service';
import Badge from 'shelf/components/badge';

export default class BookCard extends Component {   // line 5
  @service declare library: unknown;

  <template>
    <Badge>{{@status}}</Badge>
  </template>
}
```

```
symbol  app/components.BookCard   app/components/book-card.gts:5
        props: symbol_kind=class, web_component=component, framework=ember,
               ember_injected_services=[library]
        relations: calls -> app/components.Badge
                   injects -> app/services.LibraryService
```

Two things worth noticing. The template's `<Badge>` reference resolved through the
file's own imports — Glimmer strict mode guarantees that anything a template
renders is either imported or local, so import bindings plus the file's own
declarations are the exact resolution set, and a `{{this.title}}` or an `@arg`
can never produce a wrong edge. And the `injects` edge targets `LibraryService`,
the class `app/services/library.ts` *actually declares* — not a name guessed from
the service's `library` lookup key. That join happens in the post-link
`ember-resolver` binder, where the whole store is visible.

All three template positions the RFC allows are handled, each owning its own
template's references: a class-body template (above), a named binding
(`export const Question = <template>…` — the symbol classifies as a component and
carries exactly its own template's edges), and a standalone/default-export
template (the file's default component, synthesized when nothing claims the
segment). A class that embeds a template is a component whatever its superclass.

Classic `.hbs` templates have no imports to anchor on, so their invocations are
recorded and resolved the same way — via Ember's resolver layout, one candidate
file and one plausible symbol required, anything ambiguous skipped and counted in
`ember_unresolved` rather than guessed:

```
{{format-count this.rounded}}   →  calls -> app/helpers.formatCount
<Badge>…</Badge>                →  calls -> app/components.Badge
{{auto-focus}}                  →  calls -> app/modifiers.autoFocus
{{title}}                       →  nothing: indistinguishable from a property
```

Lookups anchor to the `app/` tree — Ember's own resolver rule — across
`components/`, `helpers/` and `modifiers/`; a lookalike path elsewhere cannot
shadow the real file, and a co-located template is one component with its class,
not an ambiguity. A component template with no co-located class is a
template-only component and synthesizes its component symbol. A **route
template** (`app/templates/catalog.hbs`) is owned by its route class
(`app/routes/catalog.ts`, falling back to the controller), so components
rendered only from route templates still show their consumers in
`impact_analysis`. A `.hbs` file in a repo without `ember-source` emits nothing.

`Router.map` declarations become page routes with parent paths composed the way
the router composes them:

```
route  /catalog                    app/router.ts:6   ember_route_name=catalog
route  /catalog/:book_id           app/router.ts:7   ember_route_name=catalog.book
route  /catalog/:book_id/reviews   app/router.ts:8   ember_route_name=catalog.book.reviews
route  /my-account                 app/router.ts:11  ember_route_name=account
```

These carry `type=page`, `framework=ember` — UI routes in the same sense as Nuxt
pages and SvelteKit routes, never HTTP contracts. An ember-data `Model` subclass
additionally emits a storage companion (`storage_kind=model`,
`framework=ember-data`, `table` holding the dasherized model name), the same
shape ActiveRecord models get in the Ruby extractor — and its
`@belongsTo`/`@hasMany` fields become `depends_on` edges to the storage facts of
the models they name (`ember_relationships` records the declared set; a bare
`@hasMany` is skipped, since recovering a singular model name from a plural
field would be a guess).

## Storage — three ORMs, one shape

```
storage  prisma.Post   prisma/schema.prisma:5   props: framework=prisma,  table=Post
storage  src.User      src/entity.ts:5          props: framework=typeorm, table=users
storage  src.Session   src/entity.ts:15         props: framework=typeorm, table=Session
storage  src.orders    src/schema.ts:4          props: framework=drizzle, table=orders
```

`table` is the *physical* name — `@Entity('users')` gives `users`, while an undecorated
`Session` class keeps its class name. That distinction matters when the same database is
reached from another repository in another language.

## What is deliberately not extracted

- **Non-literal paths.** `axios.get(url)` where `url` is a variable with no literal
  binding produces no route. A base URL resolved from a file-local literal *is* followed;
  one injected from config is not.
- **`openGraph` and metadata objects** that contain URL-shaped strings are not requests,
  and are not recorded as client routes.
- **Prefetch and refetch helpers** are separated from real request boundaries, so a
  query-cache warm-up does not double-count as an outbound call.
- **Paths without a leading `/`.** A bare `"users"` string is too ambiguous to treat as a
  request path.
- **Runtime-registered routes** — an Express router assembled in a loop over a config
  array is not unrolled.
- **Ember names the default resolver cannot map.** Addon components (they resolve
  into `node_modules`), pods layout, custom resolvers, and engine mount points
  produce no edge; the misses are recorded in `ember_unresolved`, not guessed at.

---

Measured on real TypeScript repositories: [BENCHMARKS.md](../BENCHMARKS.md).
