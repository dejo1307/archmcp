# Ruby — what enola extracts

Parsed with tree-sitter. Detected by a `Gemfile` at the repository root. Rails
conventions are recognised, but a plain Ruby project still produces modules, symbols and
call edges.

Fixture: [`ruby_sample`](../../internal/engine/testdata/repos/ruby_sample/)

## At a glance

| You write | enola stores | Kind |
|---|---|---|
| `app/services`, `app/models/concerns`, … | one module per directory, `framework=rails` | `module` |
| `class Foo < Bar` | a symbol with `superclass` | `symbol` |
| `module Foo` | a symbol with `symbol_kind=interface` and an `abstract` verdict | `symbol` |
| `def x` / `def self.x` | `Foo#x` (instance) / `Foo.x` (class) | `symbol` |
| `STOP_WORDS = …` | a `constant` symbol | `symbol` |
| `resources :things` in `routes.rb` | one route per generated path **and** method | `route` |
| a class-body DSL macro (`track_metrics :runs`) | a `calls` edge on the class fact | relation |
| `delegate :a, :b, to: :x` | `calls` edges for each delegated name | relation |
| `public_send(:"report_#{t}")` | a `dynamic_send_prefixes` hint on the file | `file_ref` |
| an ERB template | a `file_ref` carrying its helper and presenter calls | `file_ref` |
| `spec/**/*_spec.rb`, `test/**/*_test.rb` | a reference-only `test_ref` | `test_ref` |

## Routes — nested resource path shapes

23 lines of routing DSL:

```ruby
Rails.application.routes.draw do
  namespace :admin do
    resources :reports, only: [:index, :show] do
      resource  :export,   only: [:show, :create]
      resources :sections, only: [:index, :show]
    end
  end

  resource :session, only: [:show, :create, :destroy]
  root to: "home#index"
end
```

Ten route facts, each with the method and the controller action:

```
GET    /admin/reports                              action=index
GET    /admin/reports/:id                          action=show
GET    /admin/reports/:report_id/export            action=show      ← singular: no :id of its own
POST   /admin/reports/:report_id/export            action=create
GET    /admin/reports/:report_id/sections          action=index
GET    /admin/reports/:report_id/sections/:id      action=show      ← plural: keeps its own :id
GET    /session                                    action=show
POST   /session                                    action=create
DELETE /session                                    action=destroy
GET    /                                           handler=home#index
```

The distinction the extractor has to get right is the third line versus the sixth. A
singular `resource` nested in a plural `resources` hangs off the parent **member**
(`/:report_id`) and has no `:id`; a plural `resources` keeps one. Get that wrong and a
client calling `/admin/reports/7/export` matches nothing. `scope` and `namespace`
prefixes compose the same way.

## Modules, classes and the `abstract` verdict

Ruby modules do two unrelated jobs, and enola separates them:

```ruby
module Trackable                 # a mixin: defines instance methods
  extend ActiveSupport::Concern
  def track!; touch(:tracked_at); end
end

module Reporting                 # a namespace: only wraps a class
  class ReportWorker < BaseWorker; end
end
```

```
symbol Trackable   props: symbol_kind=interface, concern=true, abstract=true
symbol Reporting   props: symbol_kind=interface,               abstract=false
symbol Reporting::ReportWorker  props: symbol_kind=class, superclass=BaseWorker
```

`abstract` feeds package-metrics abstractness. A Concern that defines instance methods is
never instantiated on its own and *is* an abstraction; a Rails namespace module is not,
and counting it as one skews every module's abstractness score.

## DSL macros, delegation and dynamic dispatch

Ruby's metaprogramming is where naive extraction produces false dead code. Three cases,
all resolved into ordinary edges:

```ruby
class ReportWorker < BaseWorker
  track_metrics :runs                              # class-body macro
  delegate :formatted_name, :subtitle, to: :presenter

  def self.build(type)
    public_send(:"report_#{type}", type)           # dynamic dispatch
  end

  def report_daily(type); end
  def report_weekly(type); end
end
```

- The macro is recorded as a `calls` edge (`track_metrics`) **on the class fact**, so the
  base method backing it is not reported as dead.
- `delegate` folds each delegated name into `calls`, so `formatted_name` and `subtitle`
  have callers.
- The interpolated symbol contributes `dynamic_send_prefixes: ["report_"]` to the file,
  and same-prefix methods are treated as dispatched rather than dead. The prefix is only
  committed because this scope also calls `send`/`public_send` — proximity to a
  dispatcher is what makes the guess safe enough to act on.

### Loop counting that does not cry wolf

```ruby
def summarize(users)
  users.each { |user| notify(user); user.recent_posts }  # scaling → loop_depth 1
  6.times { |i| bucket(i) }                              # constant-bounded → no depth
  STOP_WORDS.each { |w| index(w) }                       # ALL-CAPS constant → bounded
  %w[csv pdf].map.each { |fmt| render_format(fmt) }      # bounded literal behind a chain
end

def reindex(model)
  model.find_in_batches { |batch| batch.each { |r| persist(r) } }   # depth 1, not 2
end
```

```
symbol …#summarize  props: loop_count=4, loop_depth=1, calls_in_loop=[notify, recent_posts]
symbol …#reindex    props: loop_count=1, loop_depth=1, calls_in_loop=[persist]
```

Four loops, one of them scaling. `find_in_batches` yields a *batch*, so the inner `.each`
is the real per-element loop and the nesting is depth 1 — reporting depth 2 there would
flag the standard Rails batching idiom as a performance problem in every Rails codebase.

### `super` is not recursion

```ruby
def display_label
  decorate(__method__)
  super
end
```

`super` records a call to the same-named ancestor method and is deliberately *not*
flagged `recursive_self` — it climbs the ancestor chain and terminates.

## Views and tests

```
file_ref app/views/reports/show.html.erb  --calls--> ReportPresenter.render_summary
                                          --calls--> current_user
                                          --calls--> can_view_reports?
test_ref spec/services/report_worker_spec.rb --calls--> Reporting::ReportWorker.build
```

ERB templates carry the only caller many presenters and helpers have. Specs are excluded
from indexing but recovered as reference-only facts, so a method used only by a spec has
an incoming edge without the spec becoming architecture.

### One naming hazard, handled

`app/jobs/cache_warmup_ab_test.rb` is production code whose basename ends in `test`. The
test globs are **directory-scoped** (`**/test/**/*_test.rb`), not a bare `*_test.rb`
suffix, so this file is indexed as source. A bare suffix glob both ignored it and routed
it to reference-only extraction, and the class vanished from the graph entirely.

## What is deliberately not extracted

- **Routes drawn dynamically** — a `routes.rb` that loops over a config array is not
  unrolled.
- **`method_missing` and `define_method` with computed names**, beyond the
  dispatcher-proximity prefix heuristic above.
- **`ApplicationRecord`, `*BaseController` and `*::Base`** are excluded from god-class
  candidacy: their fan-in comes from every subclass inheriting them, which is the
  framework working, not a design problem.

---

Measured on real Ruby repositories: [BENCHMARKS.md](../BENCHMARKS.md).
