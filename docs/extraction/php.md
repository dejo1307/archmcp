# PHP — what enola extracts

Parsed with tree-sitter. Detected by `composer.json`, a WordPress bootstrap file
(`wp-load.php` / `wp-settings.php` / `wp-config.php`), or any `.php` source within three
directory levels — so a legacy tree with no package manager is still indexed.

Fixtures: [`php_sample`](../../internal/engine/testdata/repos/php_sample/) (WordPress) ·
[`php_laravel_sample`](../../internal/engine/testdata/repos/php_laravel_sample/) ·
[`php_symfony_sample`](../../internal/engine/testdata/repos/php_symfony_sample/) ·
[`php_multirepo`](../../internal/engine/testdata/repos/php_multirepo/)

## At a glance

| You write | enola stores | Kind |
|---|---|---|
| a source directory | one module, `framework=laravel` / `symfony` where detected | `module` |
| `class`, `interface`, `trait`, `function` | a symbol with `symbol_kind` | `symbol` |
| `use App\Other\Thing;` | a dependency | `dependency` |
| `Route::get('/x', …)` (Laravel) | a server route | `route` |
| `Route::apiResource('photos', …)` | **seven** routes, one per RESTful action | `route` |
| `#[Route('/x')]` (Symfony attribute) | a server route with its route name | `route` |
| `config/routes.yaml` | a server route per configured method | `route` |
| `add_action` / `add_filter` (WordPress) | a pseudo-route on the hook name | `route` |
| Guzzle / Laravel `Http::` / Symfony HttpClient | a client route with `role: client` | `route` |
| Eloquent / Doctrine models | an entity | `storage` |

## Routes — Laravel, including resource expansion

```php
Route::get('/api/users',  [UserController::class, 'index'])->name('users.index');
Route::post('/api/users', [UserController::class, 'store']);
Route::apiResource('photos', PhotoController::class);
```

Line 9 is one call. It becomes seven facts:

```
GET    /api/photos              action=index     handler=PhotoController::index
POST   /api/photos              action=store     handler=PhotoController::store
GET    /api/photos/create       action=create    handler=PhotoController::create
GET    /api/photos/{id}         action=show      handler=PhotoController::show
PUT    /api/photos/{id}         action=update    handler=PhotoController::update
DELETE /api/photos/{id}         action=destroy   handler=PhotoController::destroy
GET    /api/photos/{id}/edit    action=edit      handler=PhotoController::edit
```

All seven keep `routes/api.php:9` as their location, because that is the line you edit to
change any of them. Route groups and prefixes compose onto these paths, and `->name()`
is preserved as `name` so a `route('users.index')` reference resolves.

## Routes — Symfony, from two sources

Attributes on the controller and YAML in `config/` are both read, and the fact records
which:

```
route  /products/{id}   src/Controller/ProductController.php:13
       props: framework=symfony, name=product_show, handler=App\Controller\ProductController::show
route  /health          config/routes.yaml
       props: framework=symfony, name=health_check, source=symfony-config,
              handler=App\Controller\HealthController::check
route  /old/home        config/routes.yaml     method=GET   ┐ one fact per method,
route  /old/home        config/routes.yaml     method=HEAD  ┘ as the config declares
```

A YAML-declared route has no line number — it is a configuration entry, not a statement —
and `source=symfony-config` says so rather than inventing one.

## Routes — WordPress hooks

WordPress has no router; it has hooks. They are the actual entry points, so they are
recorded as routes with the hook type as the method:

```php
add_action('init', 'acme_setup');
add_filter('the_title', 'acme_format');
```

```
route  ACTION init       inc/hooks.php:6   props: framework=wordpress, hook=add_action, callback=acme_setup
route  FILTER the_title  inc/hooks.php:7   props: framework=wordpress, hook=add_filter, callback=acme_format
```

This is what stops a WordPress plugin's entire callback surface from reading as dead code:
those functions have no PHP caller anywhere.

## Outbound calls and cross-repo linking

```
route  /billing/invoices   consumer/src/BillingClient.php:16
       props: role=client, framework=guzzle, api=BillingClient, source=php-http-client
route  /billing/invoices   provider/routes/api.php:9
       props: role=server, framework=laravel, handler=InvoiceController::index

service consumer  props: edge_coverage=[{detected: 2, resolved: 2, unresolved: 0}]
                  --depends_on--> provider
```

Guzzle, Laravel's `Http::` facade and Symfony's `HttpClient` are all recognised, each
tagged with its own `framework` so you can tell which client library made the call.

Note the third provider route in that fixture:

```
route  /billing/invoices/{id}   props: role=server, unmatched_by_clients=true
```

The provider serves it; no loaded client calls it. That is the `unused-routes` finding —
a candidate at confidence `0.6`, because the caller may simply be a repository you have
not loaded.

## What is deliberately not extracted

- **Routes registered in a loop** over a config array, or by a plugin discovery pass at
  runtime.
- **Non-literal paths and URLs.** `$client->get($endpoint)` with no literal binding is
  not guessed.
- **`__call` / `__callStatic` magic methods** — a call through them resolves to nothing
  rather than to every candidate.
- **Blade and Twig templates** are not yet parsed for helper calls, so a method used only
  from a template can read as unreferenced. (The Ruby extractor does read ERB; PHP does
  not yet.)

---

Measured on real PHP repositories: [BENCHMARKS.md](../BENCHMARKS.md).
