# Dart and Flutter

Every example on this page is a file that ships in
[`internal/engine/testdata/repos/dart_sample/`](../../internal/engine/testdata/repos/dart_sample/),
and every fact shown is copied from the golden the test suite asserts against.

## At a glance

| Source construct | Becomes |
|---|---|
| a directory holding `.dart` | `module`, carrying `pub_package` |
| `class` / `enum` | `symbol` (`class` / `enum`) |
| `mixin` | `symbol` (`interface`) |
| `extension`, `extension type`, `typedef` | `symbol` (`type`) |
| method, getter, setter, constructor | `symbol` (`method`) |
| top-level function | `symbol` (`function`) |
| **public** field, enum constant | `symbol` (`variable` / `constant`) |
| `extends` / `with` / `implements` | `implements` edge |
| constructor parameter of a declared type | `injects` edge |
| `import` / `export` | `dependency` (`stdlib` / `internal` / `external`) |
| `GoRoute`, `AutoRoute`, `routes:` map | `route` (`type: page`) |
| `http`/`dio` call site, `@GET(...)` | `route` (`role: client`) |
| drift `Table`, isar/hive/objectbox/floor, Firestore | `storage` |

## The module model is the pub package

A Dart repository is frequently a **workspace of many packages** — `flutter/packages`
holds over a hundred — so the package is never assumed to be the repo. Every
`pubspec.yaml` is read up front (a line scan, no YAML parser) into a name → directory
index, which is what lets a `package:` import resolve during the importing file's own
walk rather than needing a second pass.

Module facts carry `pub_package`, registered in
[`facts.CompilationUnitProps`](../../internal/facts/contract.go). That registration is
what makes the cycles explainer word a Dart cycle correctly, and Dart is the most
permissive entry in that table. C# and Rust *forbid* cycles between compilation units,
so a cycle found in either is necessarily inside one. Dart goes further: **circular
imports between libraries are legal**, compile, and are ordinary in practice. A Dart
cycle is therefore a coupling signal and never a build-order defect, and reporting one
as something that "can cause initialization issues" would simply be untrue.

## Imports map onto exactly three classes

Dart has three URI schemes and they line up with enola's three dependency classes with
no heuristics involved:

```dart
import 'dart:async';                        // stdlib
import 'package:flutter/material.dart';     // external -> dependency "flutter"
import 'package:sample_app/models/user.dart'; // internal (this repo declares it)
import '../models/user.dart';               // internal
```

An external import is attributed to the **package**, not the file. Importing twenty
widgets from `package:flutter` is one dependency edge, not twenty — otherwise every
Flutter app's dependency count would be a count of its widgets.

## Import gating

This is the decision the rest of the extractor rests on.

Dart's framework vocabulary is ordinary vocabulary. `Table` is a drift database table
**and** a Flutter layout widget. `get` is an HTTP verb **and** a map accessor.
`collection` is a Firestore call **and** a common noun. `go` is a go_router navigation
**and** an unremarkable method name. Matching those structurally is exactly what
produced four phantom routes from a metrics timer and seven phantom Kafka topics from a
forum's `closeTopic` when the Scala extractor tried it.

Dart has a defence no other language here offers: **imports are mandatory and
explicit**. There is no ambient namespace and no unqualified access to a package a file
has not named, so "could this file be using drift?" is answered by a language rule
rather than by a probability.

```dart
import 'package:drift/drift.dart';
class TodoItems extends Table { ... }   // -> storage table "todo_items"
```
```dart
import 'package:flutter/material.dart';
class TodoItems extends Table { ... }   // -> nothing. Table here is a layout widget.
```

`TestImportGatingSuppressesLookalikes` pins each gated pass against a byte-identical
lookalike differing only in its import.

A `part` file declares no directives of its own — it shares its host library's import
scope — so it is re-walked with the host's imports rather than being left with every
gate closed.

## Navigation routes are page routes

```dart
final router = GoRouter(routes: [
  GoRoute(path: '/', builder: ..., routes: [
    GoRoute(path: 'detail/:id', builder: ...),
  ]),
  GoRoute(path: SettingsScreen.routeName, builder: ...),
]);
```
```
route  /             type=page  framework=go_router  handler=HomeScreen
route  /detail/:id   type=page  framework=go_router  handler=DetailScreen
route  /settings     type=page  framework=go_router  handler=SettingsScreen
```

`type: "page"` is load-bearing, not descriptive.
[`routeindex.IsUIRoute`](../../internal/linkers/crossrepo/routeindex/routeindex.go)
keys on it to keep a route out of the cross-repo server index and out of
`unused-routes`, exactly as it already does for Next.js and Nuxt pages, SvelteKit and
Ember.

The failure it prevents is concrete. A Flutter client and its backend are frequently in
one snapshot; both have a `/users/:id`; indexing the app's **screen** as a served route
would match it against the backend's real **endpoint** — an edge in the wrong
direction, and the backend's endpoint reported as served by the phone.

Three shapes are read:

- **go_router** — nested `routes:` compose onto their parent, and a child path
  beginning with `/` is absolute and replaces it (go_router's own rule; composing
  blindly is what produced `/user/user/settings` in Scala).
- **auto_route** — the dominant idiom declares no path at all (`AutoRoute(page:
  LoginRoute.page)`; all 59 of immich's declarations), so the path is derived by
  auto_route's documented kebab-case rule, and an `initial: true` child mounts at its
  parent rather than at the root.
- the core-Flutter **`routes:` map** on `MaterialApp`.

**A path declared as a constant** — `path: SettingsScreen.routeName`, how appflowy
declares 35 of its 36 routes — is carried as a reference and dereferenced repo-wide
once every file has been walked. One that never resolves **takes its route with it**: a
fact named `SettingsScreen.routeName` would be a path no app navigates to, and the
linker would then match it against real routes.

## Outbound HTTP is what puts a Flutter app in the cross-repo graph

A mobile client serves nothing, imports nothing from its backend and shares no code
with it. A call site is the only structural evidence that the app and the service belong
to one system.

```dart
await httpClient.get(Uri.parse('/api/users/$id'));         // client route, internal
await httpClient.post(Uri.parse('/api/orders'));           // client route, internal
await httpClient.post(Uri.parse('https://crash.example.com/v1/report'));
                                                           // external=true, host recorded
```

`Uri.parse(...)` and `Uri.https(...)` are unwrapped. An absolute URL is tagged
`external` with its host so a third-party call is not counted as an unresolved internal
edge; a relative path stays internal and therefore linkable. An interpolated path is
skipped rather than published with its `$id` intact.

**retrofit and chopper** are read from their annotations rather than their call sites,
and that is not politeness — their implementation is generated into a `.g.dart` this
extractor excludes, so the annotation *is* the call site.

[`dart_multirepo`](../../internal/engine/testdata/repos/dart_multirepo/) pins the whole
join: a Flutter client's three call sites resolving to a Go `net/http` server, with the
one route no client calls staying an `unused-routes` candidate.

## Generated code produces nothing

`.g.dart`, `.freezed.dart`, `.mocks.dart`, `.gr.dart`, `.config.dart` and `.pb*.dart`
yield no facts at all — not an empty symbol set, nothing, so a directory of only
generated files emits no module either.

This is not tidiness. Generated Dart is the **majority of files** in a `build_runner`
project: one `@freezed` model yields hundreds of generated `copyWith`/`==` lines plus a
`.g.dart` of serialization, none of it code anybody navigates, all of it inflating
symbol counts and manufacturing god-class and complexity findings about machine output.
The C# extractor draws the same line for the same reason.

## Abstractness is computed, not read off the keyword

`abstract` is authoritative for the enterprise package-metrics explainer, and Dart
cannot let the keyword decide. **Every Dart class is an implicit interface** others may
`implement`, so "is implementable" says nothing; and a `mixin` routinely carries its
whole implementation, exactly as a Scala trait does.

So a type is abstract when it is declared `abstract` or `sealed`, **or** when it
declares methods and every one of them is abstract. A type with no methods at all is
data, not an abstraction — and one with public fields and no behaviour additionally gets
`data_holder`, which spares its package the "extract interfaces" advice.

## What is deliberately not extracted

- **Server-side Dart.** shelf, dart_frog and serverpod have no route awareness, so a
  Dart backend is a route *consumer* but never a *provider*.
- **Generated OpenAPI Dart clients**, for a reason worth stating precisely because it
  is not an extraction limit: the generated package is frequently **not committed**.
  immich's mobile app depends on `package:openapi` via a path dependency into
  `mobile/generated/`, which its `.gitignore` excludes — the client is produced from the
  spec at build time and never exists in a clone. No extractor can read code that is not
  there, so immich contributes navigation routes and storage but no client routes. Where
  such a client *is* committed, its call shape (a local `path` variable passed to
  `invokeAPI(...)`, rather than a literal at the call site) is still not recognised.
- **A call on an arbitrary receiver** draws a bare short-name `calls` edge rather than a
  guessed canonical target, since the receiver's static type is not tracked. Dead-code
  matching still sees the symbol used.
- **Primary constructors** (`class const Foo(final String name)`) are not accepted by
  the vendored grammar. Measured across a ten-repository corpus this affects only the
  Dart SDK's own `pkg/front_end`, which dogfoods the unreleased feature: 64 of its 307
  library files, against zero elsewhere. `pkg/analyzer` next door parses at 0.00%.
- **A computed store name** (`collection(path)`, a non-literal drift `tableName`) is
  skipped rather than invented.
