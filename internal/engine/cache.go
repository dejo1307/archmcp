package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/enola-labs/enola/internal/extractors"
	"github.com/enola-labs/enola/internal/facts"
	"github.com/enola-labs/enola/internal/version"
	"github.com/enola-labs/enola/pkg/plugin"
)

// cacheVersion is mixed into every cache key. Bump it whenever the fact schema or
// an extractor's output format changes in a way that invalidates stored facts.
// v2: Swift URLSession extractor precision (file-URL exclusion, interpolation fix).
// v3: Python route facts use method/role/bare-path Name (was http_method, verb-in-name).
// v4: Java HTTP client detection (RestTemplate call sites + @FeignClient interfaces).
// v5: PHP HTTP client detection + Laravel/Symfony route DSLs (attributes, YAML/XML config).
// v6: Ruby bare-constant references emitted as RelCalls edges (dead-code precision).
// v7: Ruby skips builtin-constant edges (god-class noise) + serializer attribute/include_ folding.
// v8: C/C++ file-scope registration-macro args (module_init/EXPORT_SYMBOL/DEVICE_ATTR) emitted as module-fact call edges.
// v9: C/C++ function-pointer field assignments (obj->cb = fn) + macro-body call references (IDENT( inside #define) emitted as call edges.
// v10: C/C++ qualifier-prefixed registration macros (static DEFINE_*_PM_OPS(name, suspend, resume)) record their function-name args as call edges.
// v11: C/C++ in-body compound-literal designated initializers (cfg = (struct X){ .cb = fn }) record their function-pointer fields as call edges.
// v12: C/C++ macro-body scan also captures value-position function pointers (.field = fn / = &fn inside #define), e.g. ops tables defined via a macro.
// v13: C/C++ in-extractor macro expansion of file-scope invocations recovers token-pasted callbacks (CONFIGFS_ATTR/DEVICE_ATTR_RO -> name##_show).
// v14: C/C++ static single-arg DEVICE_ATTR/BUS_ATTR expansion, all-ident scan of expanded macros (DEFINE_SHOW_ATTRIBUTE), capitalized C callee resolution.
// v15: C/C++ salvage function-pointer refs from file-scope ERROR regions (macro-opened structs like MACHINE_START/DT_MACHINE_START ... MACHINE_END).
// v16: extend that salvage to file-scope assignment_expression/field_expression fragments (machine_desc blocks parse that way when surrounded by other code).
// v17: full-tree salvage of `.field = fn` macro-struct debris (machine_desc) regardless of where tree-sitter scatters it (skips function bodies).
// v18: Ruby records custom class-body macro names + resolves self/self.class receivers as call edges (dead-code precision). KindTestRef facts index outbound refs from spec/test files.
// v19: Ruby captures call edges outside method bodies — class/module-body qualified & argument-position calls attach to the class fact; top-level/file-scope calls (fixtures, after_initialize blocks) attach to a new KindFileRef fact folded by the orphan collector.
// v20: Ruby call capture outside method bodies generalized to a per-scope pass (whole class/module body + top-level program), so assignment RHS and all non-`call` statements — e.g. `x = GlobalSetting.foo`, `CONST = { Proc.new { Group.bar } }` — are covered, not just bare call statements.
// v21: Ruby records the static prefix of interpolated symbols (`:"report_#{type}"` -> "report_") as a KindFileRef prop, so the dead-code detector treats dynamically dispatched (public_send/send) same-prefix methods as used.
// v22: Ruby records `super` as a call to the same-named ancestor method, and literal-symbol dispatch args (`obj.try(:foo)`, `send(:bar)`, `respond_to?(:baz)`) as calls to the named method.
// v23: Ruby captures no-arg calls on a chained receiver (ActiveRecord scope/class-method chains `Model.scope.final`, `assoc.class_method`, `x.class.method`; cheap attribute reads skipped) and indexes .rake/Rakefile files.
// v24: Ruby walks method default-parameter values for calls (`def f(x = self.class.foo)`) and records single-level predicate/bang calls (`viewer.rich?`, `x.save!`) which — unlike plain attribute reads — are unambiguously method invocations.
// v25: Ruby folds `delegate :a, :b, ..., to: X` method names as calls, and records calls on a bare-method (non-local identifier) receiver when the method name is scope-like (`some_relation.pluck_job_id`).
// v26: Ruby records scope-like (underscored) method calls on ANY identifier receiver, including local relation variables (`items = ...; items.preload_relations`).
// v27: Ruby resolves underscored calls on @ivar/@@cvar/$gvar receivers (`@klass.bo_search_fields`) and indexes view templates (ERB/Slim/HAML) for embedded Ruby calls (helpers/class methods), emitting KindFileRef references.
// v28: Ruby records method calls on a `klass`/`clazz`/`klazz` (or @klass) receiver as class-method dispatch (`klass.inline`), regardless of the method name.
// v29: Ruby extends the interpolated-prefix dispatch heuristic to strings (`"present_#{idx}"`), not just symbols, so send()-by-computed-string-name marks same-prefix methods used.
// v30: interpolated-string prefixes are now gated on dispatcher-proximity (committed only when the enclosing scope also calls send/public_send/…), so cache/Redis-key strings (`"fetch_#{id}"`) no longer hide genuine orphans; interpolated symbols remain unconditional.
// v31: Ruby block parameters (`each do |user| … end`) are now treated as locals, so a bare block var whose name matches an association no longer records a spurious in-loop call (N+1 false positive); and find_in_batches/in_batches are no longer counted as element loops (their block yields a batch, so the inner .each/.map is the real per-element loop) — fixing O(n²) mislabels of single-pass batch scans.
// v32: Ruby no longer flags `super` (climbs the inheritance chain, terminates) or a same-named call on an explicit non-self receiver (SimpleDelegator/decorator, `@delegate.render`/`new.call`) as self-recursion — both set recursive_self spuriously, the dominant recursion false positive.
// v33: Ruby recursion is now gated to same-object self dispatch — `self.class.foo` (instance method calling its sibling class method) and `obj.try(:foo)` (dispatch to a different object) no longer set recursive_self; receiverless calls, `self.foo`, and `Const.foo` matching the method's own full name still do.
// v34: Ruby constant-bounded iterators (`6.times`, `[…].each`, `%w[…]`, ALL-CAPS `CONST.each`) no longer add scaling loop_depth — they run a fixed number of times, so they no longer inflate a genuine O(n) into a false O(n²)/O(n³).
// v35: constant-bounded-loop detection now unwraps trailing size-preserving chain methods (`[a,b].compact.all?`, `%w[…].map.each`), so a bounded literal/constant behind `.compact`/`.uniq`/`.map`/… is still recognized as bounded.
// v36: Ruby module symbols now carry an `abstract` bool prop — true for mixins (modules that define instance methods) and ActiveSupport::Concerns, false for namespace/utility modules — so package-metrics abstractness (A) no longer counts Rails namespaces as abstractions. Bare-constant coupling resolution is also namespace-aware now.
// v37: Swift resolves modules at the SPM/XcodeGen *target* level instead of by leaf directory — it parses project.yml (and its include: files) so each product target's files form one module (Sources/<Name>), and routes SPM package sources into their target module; symbol names, module facts, and inter-target dependency edges all change accordingly.
// v38: Swift XcodeGen targets sharing one primary source root (e.g. the app plus its SwiftUI-preview and unit-test host targets) now collapse to a single module — the first target by sorted name owns the identity, shadow targets emit no duplicate module fact.
// v39: Swift emits SymbolMethod (not SymbolFunc) for functions declared inside a type, and records member-call edges for any receiver — self?.method() cross-extension/closure dispatch, and lowercase/property-chain receivers (coordinator?.foo(), delegate?.bar()) — resolved against a project-wide method index in a serial post-pass (unique→qualified, ambiguous→bare short name, unmatched→dropped). Also credits the method in Type.foo() and suppresses the phantom `defer` call edge. Fixes coordinator-pattern dead-code false positives.
// v40: Swift captures top-level/file-scope calls (bare `foo()` and `let x = foo()` in #!/usr/bin/swift scripts) as a KindFileRef fact so file-scope-invoked functions aren't flagged dead, and emits call edges for custom-operator usage (infix/prefix `custom_operator`, e.g. `a <- b`) resolved against operator overloads now added to the method index. (Standard-token operators like +/+=/^ are intentionally not tracked to avoid fan-in flooding.)
// v41: Swift custom-operator usage now excludes stdlib operators that the scanner emits as `custom_operator` tokens (multi-char `<=`, `>=`, `??`, `..<`, …) — only genuinely user-defined operators (`<-`) get usage edges, so comparison overloads (Time.<=/>=) no longer collect spurious fan-in / false recursion.
// v42: Swift resolves member calls to top-level functions (funcIndex fallback in resolveMethodCalls) so methods of a type whose body tree-sitter fails to parse — flattened to top-level functions, e.g. ImageUploadModel with a tuple-metatype `(T,U).self` — are no longer seen as dead; and property initializer/computed-getter calls now attach to the property as owner (essential inside `extension` blocks, which push no type owner), so a helper called only from an extension property is no longer flagged dead.
// v43: the v42 property-owner change is now scoped to the ownerless (extension) case only — class/struct property init edges stay attributed to the enclosing type as before, avoiding a broad re-attribution of the coupling graph while still fixing extension-property call capture.
// v44: Swift constant-bounded loops (`for i in 0..<10`, literal-bound `stride(...)`, and iterator closures over an array/dictionary literal or ALL-CAPS constant like `STOP_CHARS.forEach`) no longer add scaling loop_depth — they run a fixed number of times, so they stop inflating a genuine O(n) into a false O(n²)/O(n³) (Swift parity with Ruby v34/v35). Also: computed-property getters and willSet/didSet observers now emit complexity metrics (cyclomatic/loop_depth/loop_count/calls_in_loop/recursive_self), so a loop or per-iteration I/O inside `var x: [T] { … }` or `didSet { for … }` is visible to analyze_performance.
// v45: Swift subscript access (`dict[key]`, `parameters["x"] = 1`) is no longer mistaken for a function call — the tree-sitter grammar models it as a call_expression whose `[...]` is a call_suffix, so a subscript on a local/property whose name collides with a method (`parameters["x"]` inside `func parameters()`) was recorded as a self-call, producing a phantom RelCalls edge and a false `recursive_self` flag. Subscript call-expressions are now detected by their `[` call-suffix delimiter and skipped (their receiver/key are still walked for real calls), removing false recursion findings and phantom call-graph edges.
// v46: Swift `recursive_self` is now argument-label aware — a call that shares the enclosing function's bare name is flagged as recursion only when its argument labels match the function's parameter labels. This stops a call to a DIFFERENT overload/override/stdlib method of the same name being read as self-recursion: an `override func setSelected(_:animated:)` calling `super.setSelected(_:animated:)`, a `decode(key:)` extension calling stdlib `decode(_:forKey:)`, or `loadMore(completion:)` delegating to a sibling `loadMore(service:)`. Call edges are unchanged (dead-code/coupling unaffected); only the recursion signal is refined.
// v47: Swift methods now carry an `io_direct` prop when their body invokes a network/file I/O primitive (URLSession/dataTask/.data(for:), Alamofire request/download/upload, Data(contentsOf:)/String(contentsOf:)), and a transitive `performs_io` prop computed by a serial closure that propagates io_direct up the call graph — crossing ambiguous kept-bare member-call edges by expanding them through the methodIndex candidate sets (bounded), without adding edges to the shared graph. Lets the enterprise analyzer flag a genuine per-iteration network N+1 (a loop calling a method that transitively hits the network) that was previously invisible because the I/O sat behind wrapper layers and ambiguous edges.
// v48: Swift resolves inherited-method calls — a subclass (or protocol conformer) calling a base-class / protocol-extension method used to leave a dangling edge (`dir.runRequest`) because the callee isn't in the enclosing type's own method set. A serial post-pass now rewrites such dangling call targets to the declaring ancestor's method fact (`dir.DataModel.runRequest`) by walking the caller type's supertype chain (nearest-first), so class/protocol hierarchies are traversable for impact_analysis, dead-code, coupling, and the performs_io closure. Only dangling targets whose short name an ancestor declares are rewritten; already-resolved edges are untouched.
// v49: Swift models XcodeGen test-bundle targets (bundle.unit-test/bundle.ui-testing) as one module each, so a test bundle's files (e.g. Tests/Core/**) collapse into a single module instead of exploding into per-leaf-directory modules; and every module fact now carries a normalized `module_role` prop (production/test/tooling/unknown) — derived from the XcodeGen target type, the SPM target vs testTarget call, or a path heuristic for leaf-directory fallback — so package-metrics and other analyses can measure the production population without re-parsing manifests.
// v50: the `module_role` prop is now emitted by the Ruby extractor too (packwerk packages → production; leaf-directory modules → path heuristic), and the path heuristic was hoisted to facts.ModuleRoleForPath and broadened to common cross-language conventions (spec/test/tests + scripts/bin/fastlane/ci_scripts), so Ruby build-tooling modules (fastlane/, Scripts/) are classified as tooling rather than defaulting to the production population.
// v51: Swift no longer emits type-reference-derived module→module dependency edges for files that belong to a resolved SPM/XcodeGen target — those files' cross-module deps are captured completely by their `import X` statements plus the declared target graph, whereas the type-reference pass resolved bare short names through a collision-prone global index (Swift namespaces nested types, so names like Event/State/Coordinator recur across targets) and fabricated impossible back-edges (a Foundation-level target "importing" a feature target) that produced a false module cycle. For loose Swift projects (leaf-directory fallback, no target graph) the pass still runs but now skips any type name defined in more than one module. Fixes the false Swift dependency cycle.
// v52: Kotlin emits SymbolMethod (not SymbolFunc) for functions declared inside a class/object (parity with Go/Java), keeping member functions out of the high-confidence orphan bucket; records a short-name RelCalls edge for every navigation expression (receiver method call `repo.getUser()` and property/field access `slot.uniqueId`), which the short-name-matching dead-code detector needs to see live members as used; and tags `override` methods and Dagger/Hilt `@Provides`/`@Binds` methods with `override`/`di_provider` props so framework/DI entry points are excluded from orphan reporting. Fixes the mass Kotlin/Android dead-code false positives (thousands of live Retrofit/lifecycle/interface members reported as high-confidence orphans).
// v53: Kotlin base-package detection now matches Groovy build scripts (`namespace 'x'`, single quotes) in addition to Kotlin-DSL (`namespace = "x"`). A double-quote-only regex left the base package empty for `.gradle` (Groovy) projects, so every in-repo import resolved as external and bare calls to imported top-level/extension functions (`formatPrettyDate(...)`, `setMargin(...)`) emitted no call edge — reporting live utilities as high-confidence orphans.
// v54: Kotlin now walks calls that live OUTSIDE a function body — function default-parameter values (`fun f(x = helper())`) and supertype constructor-delegation arguments (`class NpeId(id) : Enrichment(npeEntity(id))`, and the object equivalent). These were previously skipped, so a helper/factory referenced only from a default value or a base-class initializer was mis-reported as a high-confidence orphan (e.g. Snowplow entity builders, Compose default-arg providers).
// v55: Kotlin captures callable references (`::foo`, `Type::foo`, e.g. `onClick = ::doNothing`, `.map(::helper)`) as short-name RelCalls edges. A function referenced only as a method reference (never called directly) was previously mis-reported as a high-confidence orphan.
// v56: Kotlin/Java perf-fact precision — (1) recursion (`recursive_self`) is now argument-count aware: a call sharing the enclosing function's name is only flagged recursion when its arg count matches the parameter count, so a call to a same-named overload (`updateItem(x)` → `updateItem(i, x)`, `onChangeStarted(2)` override → `onChangeStarted(3)`) is no longer read as self-recursion — the dominant Kotlin/Android recursion false positive (Conductor `onChange*` lifecycle). (2) Kotlin RxJava/coroutine-Flow chains no longer inflate loop_depth: in a reactive function (reactive return type Single/Observable/Maybe/Flowable/Completable/Flow, or body reactive operators subscribeOn/observeOn/applySchedulers/andThen/.subscribe/flowOn/.collect/…), the ambiguous operators (map/flatMap/filter/fold/reduce/onEach) are stream transforms, not per-element collection loops, so a `Single.flatMap { … .map { } }` is no longer a false O(n²)/O(n³). Fixes the analyze_performance false positives on this RxJava-heavy codebase.
// v57: Kotlin/Java recursion also clears the arity-matched case where an `override` delegates to a same-name, same-arity overload declared in a parent (invisible here): a body that calls `super.<self>()` marks the sibling `<self>(…)` call as delegation, not recursion (fixes the residual Conductor `onChangeEnded` false positives). And Kotlin methods now carry `io_direct`/`performs_io` when annotated as a Retrofit endpoint (@GET/@POST/@PUT/@DELETE/@PATCH/@HEAD/@OPTIONS/@HTTP) or a Room DAO op (@Query/@Insert/@Update/@Upsert/@Delete/@RawQuery) — a precise per-method I/O identity that lets analyze_performance flag a per-iteration call to a real network/DB method as a genuine N+1 (ranked high) without relying on the cross-language keyword guess.
// v58: Kotlin/Java multi-module import resolution — imports are now resolved via a
// cross-language declared-package→directory index (built from every non-test .kt AND
// .java file) instead of assuming a single global source root. In a multi-module
// Gradle project where several modules root packages at the same prefix (app/, api/,
// business/ all under de.foo.*), the old Kotlin resolver mapped every internal import
// under the single most common source root (the app module), collapsing all
// cross-module afferent coupling onto the app package (bogus Ca god-package) and
// starving library modules of Ca (falsely "useless" in package-metrics). Now an
// import resolves to the module that actually declares the package. The Java extractor
// seeds its FQN resolver with the same cross-language index so Java→Kotlin imports no
// longer drop. Kotlin & Java module facts now carry `module_role` (test for
// src/test & src/androidTest, else production) so package-metrics excludes test source
// sets; and Kotlin `sealed` classes are marked `abstract` so abstractness (A) counts
// them (they are non-instantiable), fixing inflated Distance / false "rigid" findings.
// v59: the package index now prefers a main source set (…/src/main/…) over a Gradle
// build-variant source set (src/debug, src/release, src/staging) when both declare the
// same package. An Android app's src/main and src/debug both declare the root
// application package; the v58 lexicographic tie-break wrongly mapped it to src/debug
// ('d' < 'm'), misrouting the whole app's afferent coupling onto the debug variant
// (a bogus Ca god-package). Imports of the root package now resolve to the main module.
// v60: package-metrics precision — (1) module_role now sub-token-matches compound test
// module names (split each path segment on -/_ and match an exact `test`/`tests` token),
// so Gradle test-automation modules that compile as src/main (release-tests, ui-test-utils,
// test-lab) are classified test rather than leaking into the production population, without
// misfiring on latest/contest/abtest. (2) Dagger/Hilt DI infrastructure is now tagged:
// @Component/@Subcomponent interfaces get `di_component`, @Module classes get `di_module`
// (Java & Kotlin); a Dagger @Component interface is no longer mislabeled a Spring component
// (disambiguated by interface-vs-class). Lets package-metrics exclude DI wiring from
// abstractness/type counts (a Dagger component package was falsely "useless").
// v61: TS/JS extractor now emits file-scope reference facts (KindFileRef) for JSX
// component rendering (<Foo/>), imported-identifier values (route configs like
// `{ component: Foo }`), namespace member access (`ns.foo`), require()-bound names,
// and `export … from` re-exports — plus require()/dynamic-import() dependency edges.
// Fixes massive dead-code false positives on React/CommonJS codebases, where a
// component used only via JSX or a route table previously had no incoming edge.
// v62: the TS/JS file-scope reference pass now also records same-module use
// positions — a bare call callee and identifier call arguments — so a function used
// only at module scope (`startSession()` at file top level) or passed as a value to
// an HOC (`connect(mapStateToProps)`) is no longer falsely reported dead.
// v63: a default import now also references the target module's default-export symbol
// (resolved via the known-files set + fileSymbolName), so an anonymous folder-index
// default like `export default connect(...)(X)` — named "<Folder>Index" — is no longer
// falsely reported dead when imported by the component's own name.
// v64: a `this.<member>` reference inside a class method now records a use of that
// member, so a React class-component event handler bound as a prop value
// (onClick={this.handleClick}) — never called by name — is no longer falsely dead.
// v65: the TypeScript extractor now skips minified/bundled files (any line longer
// than ~2000 chars), so checked-in vendor bundles emit no facts — invalidates
// caches that still hold the obfuscated symbols.
// v66: the TypeScript extractor now emits io_direct (body calls a network/file
// primitive or a network-module import binding) and a transitively-propagated
// performs_io prop, so cached TS facts must be re-extracted to carry them.
// v67: tightened TS io_direct — only DEFAULT/NAMESPACE network-module imports are
// I/O bindings (not named imports), and types/utils submodules are excluded, so pure
// helpers (e.g. `resolved` from network/types) no longer mislabel callers.
// v68: the TypeScript extractor now sets abstract:true on `abstract class`
// declarations (was previously indistinguishable from a concrete class), so
// package-metrics abstractness for TS must re-extract to pick up the flag.
// v69: HTTP-client detection expanded — TS options-object/openapi-fetch clients,
// Swift endpoint-enum (APIEndpoint/TargetType) clients, Rails draw(:pkg) routes now
// carry their /api/vN scope prefix, and Swift URLSession skips test/fixture sources.
// v70: Swift endpoint extractor resolves the version prefix — repo-wide default
// (protocol-extension urlPrefixComponent), single-value/switch-default overrides,
// and version-constant interpolation — so prefix-less endpoints match backend routes.
// v71: Swift endpoint extractor resolves stored-method endpoint structs (path/prefix
// computed, `method` a stored property) by reading the HTTP verb from each
// instantiation site's `method:` argument, emitting one client route per (path, verb).
// v72: Swift extractor also resolves request-wrapper endpoints (path supplied at the
// call site's `urlPathComponent:` arg, verb from `method:`/`httpMethod:` or a type
// default); Ruby extractor fixes nested Rails resource paths — a singular `resource`
// gets no `:id`, and children of a plural `resources` nest under `:<singular>_id`.
// v73: new gRPC extractor emits a server-role KindRoute per proto RPC (Name
// "/pkg.Service/Method", method POST) plus service/rpc/message symbols; the TS
// extractor detects gRPC-web client call sites as client-role routes
// (source "ts-grpc-client"), so gRPC flows through the cross-repo linker and
// unused-routes like HTTP.
// v74: Go extractor detects gRPC client call sites (NewXxxClient(...) +
// client.Method(...)) and emits client-role routes (source "go-grpc-client"),
// resolving the wire path from the generated concrete client's Invoke/NewStream
// literal. Documentation-only bump — goextractor is not a FileOwner, so its
// facts are never cached; recorded for changelog continuity.
// v75: broadened gRPC client detection — Go now resolves struct-field-injected
// clients (via the field-type map) and connect-go (procedure-const paths); the
// TypeScript extractor detects connect-es createClient/createPromiseClient(...)
// call sites. Bump required because the TS extractor is a FileOwner (cached).
// v76: classic grpc-web clients recognized (TS extractor derives service+methods
// from MethodDescriptor/rpcCall path literals); Go extractor resolves gRPC
// clients held in package-level vars. Bump required for the TS (FileOwner) change.
// v77: Ruby extractor detects Gemfile-less repos via a loose .rb/shebang scan and
// indexes extensionless Ruby executables. Bump so snapshots that cached an empty
// (undetected) Ruby result re-extract instead of serving stale zero facts.
// v78: Python extractor now emits call edges for ABSOLUTE intra-project imports
// (previously only relative imports resolved, so functions reached via
// `from pkg.mod import fn` had no incoming edge and read as dead code). A post-pass
// (resolveCallTargets) rewrites the dotted targets to canonical slash symbol names
// and drops stdlib/third-party edges; the extractor also emits KindFileRef edges for
// module-level (top-level) calls and records decorator applications as uses. Bump so
// cached Python snapshots re-extract with the new edges.
// v79: Python extractor tracks more reference mechanisms — function-local (lazy)
// imports are registered so calls through them resolve; functions/classes passed by
// name as call arguments (Depends(fn), add_command(cmd)) emit reference edges;
// parameter-default and decorator-call-argument expressions are walked (FastAPI
// Depends(...) in signatures and route-decorator dependencies); and click/Typer
// @command/@group functions are tagged cli_command. Reduces Python dead-code false
// positives; bump so cached snapshots re-extract.
// v80: Python extractor tracks three more reference mechanisms — FastAPI route
// decorators declared with the path= keyword (and empty paths) now emit route facts
// (so their handlers are rescued); pyproject.toml entry-points / console-scripts emit
// reference edges to the registered module:function; and dotted-path string literals
// (>=3 identifier segments) that name an internal symbol (lazy_load_command targets,
// provider "class-name" metadata) emit reference edges. Reduces Python dead-code
// false positives; bump so cached snapshots re-extract.
// v81: Python extractor closes three more reference gaps — class-body statements are
// walked for calls/value-refs (attrs/pydantic/SQLAlchemy field wiring like
// factory=_helper(...) and default=Factory(fn)); same-module functions/classes passed
// by name as a value are credited (via a per-module top-level-def index, excluding
// shadowing params); and FastAPI route handlers declared with a non-literal/computed
// path are tagged web_component=route_handler (framework entry points). Reduces Python
// dead-code false positives; bump so cached snapshots re-extract.
// v82: Python extractor closes the last registration gaps — functions decorated with a
// framework-registration decorator (@compiles, @x.register singledispatch, @sig.connect,
// @event.listens_for, Flask hooks) are marked used via a self file-ref edge; value
// references now also resolve attribute args (register_error_handler(404, m.handler))
// and dict/list/set/tuple values (dispatch tables like {"ds": ds_filter}). Reduces
// Python dead-code false positives; bump so cached snapshots re-extract.
// v83: complexity signals gain a new prop scaling_loop_depth (loop nesting counting only
// input-scaling loops — literal/constant/range(<const>) iterables and infinite
// while(true)/for{} event loops are discounted), emitted by the Python, TypeScript, and Go
// extractors; the Python extractor also now emits io_direct/performs_io (transitive
// DB/network/file I/O). Consumed by the enterprise performance analyzer to deflate the
// O(n^k) tail and tighten call-in-loop precision; bump so cached snapshots re-extract.
// v84: complexity signals gain calls_in_scaling_loop — the subset of calls_in_loop made
// inside an input-scaling (unbounded) loop — emitted by the Python, TypeScript, and Go
// extractors. Lets the performance analyzer treat a call in a bounded loop (literal /
// range(<const>) / while(true)) as a fixed count, not an N+1; bump so cached snapshots
// re-extract with the new signal.
// v85: Python extractor emits two new structural props so package-metrics stops
// mislabeling idiomatic-Python packages. (1) `enum` on Enum/IntEnum/StrEnum/Flag/IntFlag
// subclasses (excluded from N like Kotlin enums) and `data_class` on DTO/schema/record
// classes — @dataclass/@attrs-decorated, Pydantic BaseModel, NamedTuple, TypedDict —
// so DTO/model packages (e.g. OpenAPI-generated Pydantic "datamodels") are no longer
// flagged "rigid — extract interfaces". data_class covers Pydantic BaseModel, NamedTuple,
// and TypedDict subclasses plus @dataclass/@attrs-decorated classes. (2) `abstract` now
// also covers the duck-typed abstract pattern (a method whose whole body is
// `raise NotImplementedError`), so abstractness (A) is meaningful for Python base classes
// that don't use ABC. Bump so cached Python snapshots re-extract with the new props.
// v86: Python data_class detection broadened to Pydantic RootModel/GenericModel/BaseSettings
// and any "*BaseModel" subclass — covers project-local Pydantic bases (StrictBaseModel,
// <App>BaseModel) used by hand-written schema packages, which v85 missed because BaseModel
// wasn't a direct base (so those datamodels packages were still flagged "rigid"). Bump so
// snapshots cached under v85 re-extract with the widened detection.
// v87: Python resolveCall no longer fabricates same-module RelCalls edges for callable
// parameters, locals, and loop variables — it now resolves a bare callee only when the name
// is a known module-level def and not shadowed by a parameter (mirrors valueRefTarget). Drops
// spurious call edges; bump so cached Python snapshots re-extract with the tightened resolver.
// v88: Python extractor now emits gRPC client-role routes (source=python-grpc-client) for
// stub.Method(...) call sites, detected from source. New facts, so cached Python snapshots must
// re-extract to pick them up.
// v89: Swift HTTP-client extractor widens method inference (symmetric scan window + enum/
// .rawValue/Alamofire .method forms) and tags calls to hardcoded absolute hosts with
// external=true + host. Changes route methods and props, so cached Swift snapshots must
// re-extract.
// v90: Kotlin Retrofit extractor tags absolute-URL annotations external=true + host; Ruby
// route extractor adds `match ... via:` verbs and reads `scope`/`namespace path:` keyword
// prefixes. New/changed route facts, so cached Kotlin and Ruby snapshots must re-extract.
// v91: Ruby route extractor emits both PATCH and PUT for the resources/resource update
// action (Rails routes both verbs to update), so a client calling PUT resolves. New route
// facts, so cached Ruby snapshots must re-extract.
// v92: Ruby route extractor handles symbol path args (`get :cities_by_zip`), a bare-symbol
// `scope :users` path prefix, and the `resource(s) ..., path:` segment override. New/
// corrected route paths, so cached Ruby snapshots must re-extract.
// v93: Swift endpoint extractor's switchReturns now collects case labels that wrap across
// multiple lines, so a `case .a,\n .b: return .post` maps every label (not just the first)
// — correcting HTTP methods that previously defaulted to GET. Cached Swift snapshots must
// re-extract.
// v94: Swift endpoint extractor reads a single-value method property (`var method:
// HTTPMethod { return .post }`, no switch) and applies its lone verb to every case, instead
// of defaulting to GET. Cached Swift snapshots must re-extract.
// v95: Ruby extractor tags synthetic coupling edges with a coupling_kind prop (so the cycles
// explainer can exclude ActiveRecord associations) and adds common Rails framework constants
// (I18n, Rails, Logger, ...) to the builtin-const ignore list. Cached Ruby snapshots must
// re-extract to pick up the new edge props and suppressed references.
// v96: Python value-ref resolution now records assignment-RHS and return-statement bare-name
// references (cb = handler; return cb / return handler) as RelCalls edges, not just call
// args/decorator args/collection literals; and its shadow guard (previously param-only, shared
// with resolveCall's v87 fix) now covers any name bound in the enclosing function's own scope —
// assigned/iterated/aliased locals, not just parameters — so a loop var or local reusing a
// same-named top-level def's name no longer fabricates a same-module edge. Cached Python
// snapshots must re-extract.
// v97: the Ruby ignore/test globs are directory-scoped ("**/spec/**/*_spec.rb" rather than
// "**/*_spec.rb"), so a production file whose basename merely ends in the token _test/_spec
// (a job named cache_warmup_ab_test.rb) is indexed as source instead of being ignored and
// misrouted to reference-only test-ref extraction. The glob matcher gained the
// "<prefix>/**/<fileglob>" form to express it. Cached Ruby snapshots must re-extract: the
// file set reaching the extractor changes.
// v98: the Kotlin extractor learned bounded-loop discounting and now emits
// scaling_loop_depth (and calls_in_scaling_loop) alongside loop_depth, joining the
// Go/Python/TypeScript convention. A constant-count loop — a literal integer range
// (0..2, 0 until 3, 2 downTo 0, 0..10 step 2), a collection-literal receiver
// (listOf(a, b).forEach), an ALL_CAPS data constant, or a size-preserving chain over
// either — no longer inflates the Big-O exponent, and neither does an infinite
// while (true) / do-while (true). Previously perf.scalingDepth() silently substituted the
// raw loop_depth for Kotlin, so `for (ring in 1..6) { for (i in 0 until sides) }` reported
// O(n2) at full confidence. Cached Kotlin snapshots must re-extract.
// v99: calls_in_scaling_loop now means "calls inside a loop that repeats a NON-CONSTANT
// number of times", not "calls inside an input-scaling loop", and is emitted by the Go,
// Python, TypeScript and Kotlin extractors whenever calls_in_loop is — even when empty.
// Two bugs, one cause: `for {}` / `while (true)` were classed bounded, which is right for
// the Big-O exponent (they add no factor of n) but wrong for N+1 detection (a parent-chain
// walk runs one query per level), and an omitted empty subset was read by
// perf.scalingLoopCalls() as "extractor never computed it", falling back to the unfiltered
// calls_in_loop. The second bug masked the first: fixing only the emptiness would have
// deleted true-positive N+1 findings on infinite loops. Cached Go/Python/TypeScript/Kotlin
// snapshots must re-extract.
// v100: the Go extractor implements plugin.TestRefExtractor and config.Default().TestGlobs
// gained "**/*_test.go", so a production function whose only caller is its own _test.go is
// no longer reported as high-confidence dead code. Both gates were needed: the glob alone
// is a no-op (runTestRefExtractors skips non-implementers), and the interface alone never
// sees the files (walkRepo collects an ignored file only when it matches a test glob). Go
// was the sharpest case of GAP-XL-02 — _test.go is ignored under BOTH shipped configs and a
// plain function's orphan tier is `high`. Test refs are resolved with the production
// resolvers (flattenSelector/collectLocalTypes/resolveChain), so a reference from a test is
// spelled exactly as one from production and inherits goBuiltins filtering: a package-level
// `min` shadowing the Go 1.21 builtin is still not credited, from either side. The file set
// reaching the extractors changes, so cached snapshots must re-extract.
// v101: the Go HTTP-client extractor recovers the host that `baseURL + "/path"`
// concatenation discards. It resolves the base identifier through a package-scoped
// string-literal index (const/var/assignment/composite-field bindings) and, when
// every binding is an absolute http(s) URL, tags the route external=true (plus a
// host prop when the bindings agree on one host). Before this, a service calling
// only third-party APIs via base-URL concats accumulated phantom "unresolved
// internal edges" and was misclassified
// coverage_gap. A config-injected base (options.BaseURL, no literal binding) stays
// an internal client route. Route facts gain external/host props, so cached Go
// snapshots must re-extract. (The paired linker change — matching a client route
// before bucketing it external, so a hardcoded internal host still resolves — runs
// post-extraction and needs no cache bump; it is covered by TestGolden here.)
// v102: the Swift extractor folds a typealias's aliased type into the alias fact
// as a RelInstantiates edge. `typealias FooViewModel = FooEditorState` used to
// leave FooEditorState with no incoming edge, so a type reached only through its
// alias name was mis-reported as unreferenced dead code (GAP-SW-09). handleTypeAlias
// now emits the edge (guarded like handleInit — system types and function/tuple/
// optional RHS shapes, which have no simple type name, emit nothing). Swift symbol
// facts gain a relation, so cached Swift snapshots must re-extract.
// v103: the TypeScript extractor implements plugin.TestRefExtractor, and
// config.Default().TestGlobs gains the four *.test.ts(x)/*.spec.ts(x) globs, so a
// production symbol whose only caller is its co-located test keeps an incoming
// edge and is no longer reported dead (GAP-XL-02 TS half — the last language
// affected under config.Default(), after Go at v100). ExtractTestRefs reuses the
// file-ref walk's production resolvers so targets are fully qualified (no bare-name
// over-crediting via orphans' lastSeg fold). TS test files now emit test_ref facts,
// so cached TS snapshots must re-extract; the bump is required because the TS
// extractor is a FileOwner (cached).
// v104: the Java extractor learned bounded-loop discounting and now emits
// scaling_loop_depth (and calls_in_scaling_loop) alongside loop_depth, joining the
// Go/Python/TypeScript/Kotlin convention (GAP-JV-01, the Java third of GAP-XL-01). A
// constant-count loop — a literal-bounded for (for (int i = 0; i < 3; i++)), a for-each
// over a collection literal (List.of(...)) or an ALL_CAPS constant, or a stream iterator
// over such a receiver — no longer inflates the Big-O exponent; an infinite for (;;) /
// while (true) is likewise discounted from the exponent but keeps its per-iteration calls
// as N+1 candidates (three-valued classification: constant / infinite / scaling — see
// v99). Previously perf.scalingDepth() silently substituted the raw loop_depth for Java,
// so a fixed-count for reported O(n2)/O(n3) at full confidence. Cached Java snapshots must
// re-extract. C/C++ and PHP remain undiscounted (GAP-XL-01 stays open for them).
// v105: the C/C++ extractor learned bounded-loop discounting and now emits
// scaling_loop_depth (and calls_in_scaling_loop) alongside loop_depth, joining the
// convention (GAP-CP-01). A constant-count loop — a literal-bounded for
// (for (int i = 0; i < 3; i++)) or a range-for over a braced init-list ({1, 2, 3}) —
// no longer inflates the Big-O exponent; an infinite for (;;) / while (true) / while (1)
// is discounted from the exponent but keeps its per-iteration calls as N+1 candidates
// (three-valued classification, cache.go v99). STL algorithms (std::for_each over
// [begin, end)) scale with the container and count toward scaling_loop_depth. C/C++ was
// the worst-affected language — fixed-size array iteration is idiomatic. Cached C/C++
// snapshots must re-extract.
// v106: the PHP extractor learned bounded-loop discounting and now emits
// scaling_loop_depth (and calls_in_scaling_loop) alongside loop_depth, joining the
// convention (GAP-PH-01, the last of GAP-XL-01). A constant-count loop — a
// literal-bounded for ($i = 0; $i < 3; $i++) or a foreach over an array literal
// ([1, 2, 3]) / ALL_CAPS constant — no longer inflates the Big-O exponent; an infinite
// while (true) / for (;;) is discounted from the exponent but keeps its per-iteration
// calls as N+1 candidates. Cached PHP snapshots must re-extract. With this, all seven
// AST extractors that discount bounded loops (Go, Python, TS, Kotlin, Java, C/C++, PHP)
// plus Ruby/Swift (inline) are complete — GAP-XL-01 is fully closed.
// v107: the Swift extractor now emits the `override` prop on a method declared with the
// `override` modifier, mirroring the Kotlin extractor (kotlin_ast.go). An override is
// dispatched polymorphically through its supertype — UIKit/SwiftUI lifecycle callbacks
// (viewDidLoad, viewWillAppear, …) are invoked by the framework, never by the override's
// own literal name — so the enterprise dead-code detector (orphans.go, which already
// consumes the prop and documented it as Kotlin/Swift) now excludes them as framework
// entry points instead of flagging them as orphans. Cached Swift snapshots must
// re-extract (GAP-SW-01).
// v108: the Swift extractor now emits `conditional: true` on every symbol declared
// inside a #if/#elseif/#else conditional-compilation branch. Tree-sitter walks both
// branches (the compile-time condition is not evaluated), so a type declared once per
// branch yields two same-name symbol facts; tagging them lets the counting consumers
// (exported-surface, complexity-outliers, package-metrics, via facts.CanonicalSymbols)
// collapse the duplicate while both declarations stay individually queryable. Cached
// Swift snapshots must re-extract (GAP-SW-10).
// v109: the Python extractor now detects Flask routes. Classic @app.route / @bp.route
// (verbs read from methods=[...], default GET) and Flask-AppBuilder @expose — which has
// no receiver dot and was invisible to the FastAPI verb regex — now emit KindRoute facts
// tagged framework="flask". The @app.get verb shorthand is labeled from project detection
// (detectFlask/detectFastAPI) instead of a hardcoded "fastapi", so a Flask 2.0 app is no
// longer mislabeled. Cached Python snapshots must re-extract (GAP-PY-01).
// v110: the Java extractor now emits `io_direct`/`performs_io` on methods that are
// genuine DB/network round-trips — every method of a @FeignClient interface or a
// Spring Data repository interface (extends JpaRepository/CrudRepository/…), any
// @Query/@Modifying/@Procedure method, and Room ops inside a @Dao interface. This
// populates enola-enterprise's isExpensiveJvmCall I/O index, which was empty on
// pure-Java Spring/JPA repos (v57 wired the prop for Kotlin only), so a per-iteration
// repository call now ranks a confirmed N+1 `high` instead of a keyword guess. The
// seed is deliberately type-level and never keys off a bare HTTP verb (@GET/@GetMapping),
// which on server-side Java is an inbound JAX-RS/Spring handler, not I/O; and there is
// no transitive fixpoint (performs_io == io_direct), matching the Kotlin design. Cached
// Java snapshots must re-extract (GAP-JV-02).
// v111: the Go extractor now emits `http_handler: true` on any func or method whose
// signature is exactly func(http.ResponseWriter, *http.Request) — net/http's handler
// contract, decidable from the AST. It exists so a route can be bound to the symbol that
// actually serves it: goextractor renders a route's `handler` prop from the REGISTRATION
// site, so it is the receiver VARIABLE chain ("h.fooHandler.DoThing")
// while the symbol is named by its receiver TYPE (".../foo.HandlerV2.DoThing").
// The two key spaces are disjoint — in a real service the handler props barely
// intersected the symbol names at all — so every consumer keyed on the handler (the
// enterprise route-handler escalation, orphans' handler rescue) matched nothing, and
// almost no routes carried a handled_by edge. The method name is all the two sides share, and
// it is ambiguous: a service can have several same-named methods (the handler, a service, a mock, and
// a null-object stub in the WIRING package where the routes are registered), so a
// name-only or package-scoped binder resolves the route to the stub — and a wrong
// handled_by edge feeds impact_analysis and find_path. The signature is the structural
// discriminator that rejects the stub by construction. Consumed by the new engine pass
// bindHTTPHandlers (post-extraction, outside the cache). Cached Go snapshots must
// re-extract (new/18).
// v112: the TypeScript extractor now emits `storage` facts for the three mainstream TS
// ORMs — a TypeORM `@Entity()` class (the extractor's first decorator support; it read
// none before), a Drizzle `pgTable("orders", …)` const, and every `model` in a Prisma
// `schema.prisma` (read off-glob, as package.json and tsconfig.json already are, since
// the schema is a separate DSL that tree-sitter never sees). Facts are named after the
// declaration with the physical table in a `table` prop, matching Kotlin/Room and
// Java/JPA. TypeScript was the ONLY backend language in enola that modelled no storage
// at all — Go, Java, Kotlin, Python and Ruby all do — so a database-backed Node service
// reported zero tables in explore, impact_analysis, llm_context and --explain.
//
// The same change adds the distinctive ORM query methods (findMany/findUnique/queryRaw/…)
// to the io_direct seed set, so computeTSPerformsIO propagates performs_io through a
// REPOSITORY WRAPPER. A direct in-loop `prisma.post.findMany()` was already caught by the
// analyzer's name list; a wrapper around it invoked no network primitive, so it was never
// io_direct and a per-iteration call to it was invisible. The generic verbs
// (find/save/update) stay OUT of the seed set: frontend TS reuses them for in-memory
// helpers and everything is exported, which is what flooded the high bucket with false
// N+1s and is why the TS detector was narrowed in the first place. Detection is gated on
// the package.json dependency, so a class coincidentally decorated @Entity in a non-ORM
// repo models nothing. Cached TypeScript snapshots must re-extract (GAP-XL-04, new/26).
// v113: Java rescues framework-loaded classes that carry no in-code caller and were
// reported as dead-code orphans. (1) SPI service-file fold — the extractor reads
// META-INF/services/* (JDK ServiceLoader) and META-INF/dubbo/** (Dubbo SPI) files,
// resolves each registered impl FQN to its in-repo canonical type, and emits a
// KindFileRef fact whose RelCalls edges reference those impls (external entries
// dropped) — so a Dubbo/JDK SPI extension registered only in a service file, with no
// @Activate annotation, is no longer a false orphan. (2) @RuleNode — a class annotated
// with a runtime classpath-scanned plugin annotation (ThingsBoard @RuleNode) now
// carries scanned_plugin=true (consumed by the enterprise dead-code detector as an
// entry point). Both are new/changed facts, so cached Java snapshots must re-extract
// (GAP-JV-08, new/60).
// v114: adds the Rust extractor — fn/struct/enum/trait/type/const/static symbols;
// impl/trait "implements" edges attached via a post-pass; use-based dependency facts
// classified internal/external/stdlib; cyclomatic complexity; Axum route facts; and
// RelCalls/RelInstantiates edges covering calls inside a macro invocation's token_tree
// (bail!/format!/matches! arguments, and an ordinary call embedded in any attribute
// macro, e.g. thiserror's #[error("{}", helper(x))]), a function passed by name as a
// value (call argument, struct field, &f, nested inside a macro argument like
// vec![Box::new(f)], or a local fn declared earlier in the same body), serde/clap/merge
// attribute strings and scoped paths (default, skip_serializing_if, value_parser,
// strategy), an unprefixed `use foo::bar;` where foo is a body-less `mod foo;` in the
// same file/mod scope (classified internal instead of external), calls/references made
// in macro content with no enclosing symbol (a macro_rules! template body, or an
// item-level macro invocation standing in for a whole function — recorded on a file_ref
// fact instead of dropped), a scoped associated-function call (Type::new(),
// some_mod::Type::from(x)), Type::Variant (bare value or match pattern), and a bare
// capitalized identifier used as a plain value (unit struct argument/let-binding) — the
// last three all recorded as RelInstantiates for Type, the dominant Rust construction
// idioms beyond a struct literal. Drop::drop and Future::poll are tagged override
// (compiler/runtime-invoked, never called by name), mirroring Kotlin/Swift's `override`
// handling. Rust is a new FileOwner, so its arrival also reshuffles which files count as
// "shared" for every other cached extractor's key in a mixed-language repo. Cached
// snapshots of any repo containing Rust files must re-extract.
// v115: Rust recognizes a #[test]/#[tokio::test]/#[wasm_bindgen_test] fn wherever it
// lives — a plain tests.rs file, an un-gated mod tests {} — not just inside a
// #[cfg(test)] module; it gets no symbol fact, and its calls into production code are
// credited via a file_ref/test_ref fact instead of counted as dead production code.
// Also records a function referenced bare inside an array literal (a `&[f, g]`
// dispatch table) as used, and resolves the schemars crate's
// #[schemars(schema_with = "fn")] attribute string (bare or crate::-qualified)
// like serde's default/skip_serializing_if. Fixes a latent bug where
// self.method() was silently dropped (no edge at all) whenever method wasn't
// a sibling of the immediately enclosing impl block — e.g. a type with
// several impl blocks, or a trait's own default method.
// v116: the C/C++ extractor now credits calls inside a detached top-level
// compound_statement — the body of a function defined by a name-carrying macro
// (SYSCALL_DEFINE0-6, COMPAT_SYSCALL_DEFINE*, and kin) that tree-sitter parses as a
// separate errored call_expression plus a loose `{ … }` block. Previously the body's
// calls had no owning function_definition and were dropped, so a static helper
// reached only from a syscall handler was mis-reported as dead code (find_orphans
// high-confidence false positive). The body's function-position identifiers are now
// hung off the module owner (same mechanism as a #define body); no symbol is emitted
// for the macro-defined handler itself (a syscall is dispatched by table, never
// called by name, so a handler symbol would be a fresh orphan FP). Cached C/C++
// snapshots must re-extract.
// v117: the Python extractor now walks function/class definitions nested inside
// function bodies, crediting their references (calls, decorators on nested defs,
// lazy imports, value/string refs) to the enclosing symbol — as lambdas always
// were. Previously walkForCalls hard-stopped at nested defs, which own no symbol,
// so every helper reached only from a closure was mis-reported dead (find_orphans
// high-confidence FP; the FastAPI router-factory pattern `def get_x_router():
// @router.post async def handler(): helper()` was the dominant bucket — ~80 of 326
// high-confidence orphans across the Python corpus). Metrics stay suppressed in
// nested scopes (a closure's loops are not the enclosing function's complexity and
// must not seed its N+1 candidates), and nested-scope bindings — including the
// nested def's own name, now also bound in the enclosing scope — shadow same-named
// module defs so bare calls cannot fabricate edges. Cached Python snapshots must
// re-extract.
// v118: the Python extractor's registration-decorator set now covers MCP servers —
// FastMCP @mcp.tool / @mcp.resource / @mcp.prompt / @mcp.custom_route, and bare
// re-exported wrappers (@tool, @prompt) that app cores build around FastMCP. These
// decorators register the function as a protocol handler the server dispatches per
// client request, so it has no in-code caller by construction; previously every
// MCP tool/resource/prompt/health-route handler was mis-reported as dead code
// (find_orphans high-confidence FP — the largest Python bucket, 18 of 234
// high-confidence orphans across the Python corpus). Matching functions get the
// existing registration self file-ref edge; functions carrying only wrapper
// decorators (e.g. @log_usage) are untouched, so genuinely unregistered handlers
// still flag. Cached Python snapshots must re-extract.
// v119: the Python extractor now registers imports inside module-level try/if
// compound statements — previously walkStatement had no case for them, so the
// try/except ImportError dual-import idiom, `if __name__ == "__main__":` imports,
// and `if TYPE_CHECKING:` imports registered no binding and emitted no dependency
// fact. Calls through the guarded names were then unresolvable (or fabricated a
// same-module target), so the callees read as dead (find_orphans high-confidence
// FPs — a Python MCP server's entire server-utils surface, ~13 of 103 across the
// Python corpus). Imports inside an except_clause register as fallbacks that never
// clobber the try-branch binding (the relative form resolves to a real path; the
// bare fallback would be dropped as external, deleting the edge). Separately, a
// module-level assignment whose RHS is a bare def name (the
// `click.echo = echo_to_stderr` monkeypatch / dispatch-table / alias-export
// idiom) now folds a value-ref to that def, so a function installed by assignment
// is not mis-flagged dead. Def/class definitions *guarded by a conditional* are
// deliberately NOT emitted as symbols: they are almost always intentional shims
// (a macOS-only fallback, a TYPE_CHECKING typing stub, an ImportError
// alternative) whose name is bound by a sibling branch, so a symbol for one is a
// dead-code false positive. Cached Python snapshots must re-extract.
// v120: the Svelte extractor now scans a SFC's template/markup for identifiers
// referenced only from there — event-handler attributes (on:click={fn},
// onclick={fn}), mustache expressions ({fn(x)}), and bind:/use: directives —
// folding them into a KindFileRef fact, mirroring the TS extractor's JSX file-ref
// pass. Previously extractSvelteSFC fed only the <script> block to the parser and
// discarded the template outright, so any handler/action/binding wired solely from
// markup had zero incoming edges and read as dead (find_orphans medium-confidence
// FP; ~136 of 229 medium orphans across the Svelte corpus). Cached Svelte snapshots
// must re-extract.
//
// v121: the Rust extractor now emits the loop/IO complexity metadata every other
// extractor already emits — loop_depth, scaling_loop_depth, loop_count,
// calls_in_loop, calls_in_scaling_loop (syntactic for/while/loop with a
// constant-trip-vs-scaling distinction), io_direct (direct filesystem/DB/HTTP
// primitives on the callee), performs_io (transitive fixpoint over the call
// graph), and recursive_self. Previously Rust symbols carried only cyclomatic, so
// the performance analyzer produced nothing for Rust regardless of nesting depth
// or I/O. Cached Rust snapshots must re-extract to gain the new props.
//
// v122: the PHP extractor now emits io_direct (direct filesystem/DB/HTTP primitives
// on the callee — file_get_contents/fopen/curl_exec/mysqli_*/wp_remote_*, and
// distinctive DB methods $wpdb->get_results/get_row/get_var/get_col, PDO/mysqli
// fetch_all/query/execute) and performs_io (transitive fixpoint over the call
// graph). Previously PHP emitted loop metadata but no I/O signal, so an in-loop
// call to a DB/HTTP wrapper was not recognized as an N+1. Cached PHP snapshots must
// re-extract to gain the new props.
//
// v123: the C++ extractor now emits io_direct (a narrow, unambiguous set of direct
// file/socket data-transfer primitives on a free/namespaced call — fopen/freopen/
// fread/fwrite/socket/recvfrom/sendto; console/logging fprintf and the ambiguous
// socket verbs bind/connect/send are deliberately excluded to avoid mass FPs) and
// performs_io (transitive fixpoint over the call graph). C++ <fstream> stream I/O
// via member calls is not detectable (dropped upstream). Previously C++ emitted
// loop metadata but no I/O signal, so an in-loop file/socket wrapper call was not
// recognized as an N+1. Cached C/C++ snapshots must re-extract to gain the new props.
//
// v124: the TS extractor now classifies SvelteKit's file/export-name-convention
// entry points as route_handler — `load` in +page.ts/+layout.ts/+page.server.ts/
// +layout.server.ts (under routes/), GET/POST/... in +server.ts (under routes/),
// and handle/handleError/handleFetch in hooks.server.ts — reusing the existing
// web_component=="route_handler" exclusion classifySymbol already applies to
// Next.js App Router handlers. detectSvelteKitRoute only ever ran for .svelte SFCs,
// so these plain-.ts framework entry points got no route fact and no symbol
// classification at all; their exports read as find_orphans false positives
// (11 of 80 remaining medium orphans across the Svelte corpus after v120). No
// enterprise-side change: the route_handler consumer already existed. Cached
// TypeScript/Svelte snapshots must re-extract.
//
// v125: the Go route extractor now composes gorilla/mux (and chi) subrouter
// PathPrefix mounts INTERPROCEDURALLY. Previously it composed prefixes only within
// a single function, so a real backend that creates `apiRouter :=
// router.PathPrefix("/api").Subrouter()` and passes it into a per-file/per-package
// registration function (`func RegisterX(r *mux.Router){ r.HandleFunc("/courses",…) }`)
// stored the bare leaf "/courses" instead of the true runtime "/api/courses". A
// module-wide fixpoint (buildRoutePrefixIndex) now propagates the prefix a router
// argument carries at each call site — including receiver-method registrations
// (`h.RegisterRoutes(sub)`, `app.Handler.RegisterRoutes(sub)`), resolved via the
// shared resolveChain type resolver — to the callee's router parameter, seeding
// extractRoutes so routes are stored at their full mounted path. This corrects
// cross-repo client↔route matching (unused-routes false positives) and route
// facts consumed by impact_analysis/traverse. Cached Go snapshots must re-extract.
//
// v126: three Go-extractor accuracy fixes. (a) Collection-root routes registered
// as `subrouter.HandleFunc("", h)` are no longer dropped by the empty-path guard
// that fired before subrouter-prefix composition — an empty STRING LITERAL is now
// composed to the subrouter prefix (e.g. "/api/settings/courses"), while a dynamic
// (non-literal) path arg still drops; same relaxation in chi/net-http. (b) A struct
// used as a composite literal (`T{}`/`&T{}`) or as another struct's named field
// type now emits a RelInstantiates edge (internal types only), so a struct used
// only as a value/field is no longer a dead-code false positive — matching the
// Swift/Rust convention. (c) scaling_loop_depth now discounts HIERARCHICAL nested
// loops: a loop whose ranged collection is reached through an enclosing loop
// variable (`range pkg.Files`, `range pkgs[i]…`) visits each element once and adds
// no factor of n, so a tree/AST walk no longer reads as O(n²+); all-pairs over an
// independent/same collection still counts. Cached Go snapshots must re-extract.
//
// v127: two Rails route-extractor accuracy fixes. (a) `resources :x, shallow: true`
// nested under a parent resource now emits its MEMBER routes (show/edit/update/
// destroy) at the shallow path — the parent segment and its :parent_id are dropped
// (e.g. /api/v3/replies/:id, not /api/v3/posts/:post_id/replies/:id) — while
// collection routes (index/create/new) stay nested, mirroring Rails. (b) An optional
// route segment `foo(/:bar)` now expands to both concrete paths it serves
// (/foo and /foo/:bar) instead of one literal that matched nothing. (c) The
// hash-rocket route form `get 'path' => 'ctrl#action'` (path as the string hash
// key, not a `to:` keyword) is now extracted — previously the path was skipped
// entirely and the route was never emitted. Cached Ruby snapshots must re-extract.
// v128: four TS HTTP-client extractor precision fixes that cut false cross-repo
// "unresolved edges" from a frontend. (a) The fetch()/makeRequest() matcher gained
// a left word-boundary, so a call whose name merely ENDS in "fetch"
// (router.prefetch(...), query.refetch(...) — navigation/cache primitives) is no
// longer captured as an outbound call. (b) An options-object `url:` is a request
// only when the object carries a real HTTP verb or a request-payload key; a Next.js
// SEO block (openGraph { url, type:'website', siteName }) / JSON-LD, whose only
// signal is a non-verb type:, is no longer mis-read as a call. (c) cleanTSPath now
// requires a leading "/", dropping non-path literals (a lone ",", a fragment of an
// analysis script scanning for "fetch(") that became phantom routes. (d) cleanTSPath
// strips a query-string placeholder fused to the final segment (`${queryParams}` →
// ".../role-distribution{}" → ".../role-distribution"), which a real `?` inside the
// interpolated variable hid from the query strip; own-segment "/{}" path params are
// untouched. Cached TypeScript snapshots must re-extract.
// v129: the TS http-client extractor now resolves a file-local base-URL literal
// interpolated at the head of a call path. A client call written as
// `${this.basePath}/calculate`, where `basePath = '/api/settings/pricing'` is a
// const/class-field/ctor-default "/"-rooted string literal in the same file, is
// reconstructed to its full path ("/api/settings/pricing/calculate") instead of
// collapsing to the single-segment suffix "/calculate" that the cross-repo matcher
// skips — so these real calls resolve to their server route. An identifier bound to
// two different literals in the file is ambiguous and left unstripped; an
// injected/env/absolute base (not a "/"-rooted literal) still falls back to the
// suffix, as before. Cached TypeScript snapshots must re-extract.
// v130: the Rust/Axum route extractor now composes `.nest(prefix, module::router())`
// mount prefixes INTERPROCEDURALLY. A router built by `pub fn router() -> Router` in
// one file (registering bare in-router paths like "/status", "/{id}/data") mounted
// by a parent via `.nest("/api/v1/datasets", routers::datasets::router())` in another
// file is now stored at its true runtime path ("/api/v1/datasets/status"). A crate-
// wide fixpoint resolves each nest to its callee builder (by fn name + module/file
// stem, same-crate preferred) and propagates prefixes from root builders; a router
// mounted at several prefixes emits each route once per prefix, and a root or
// unresolvable mount keeps the bare path so composition never drops a route — the
// Axum analog of the Go gorilla/mux+chi subrouter composition (v125). This fixes
// cross-repo client↔route matching (unused-routes / coverage FPs) and the route
// facts consumed by impact_analysis/traverse. Cached Rust snapshots must re-extract.
// v131: the Swift extractor now sub-divides an XcodeGen application/app-extension
// target into per-directory packages (Go/Ruby-style leaf-directory modules) instead
// of collapsing the whole target to a single module, and runs the type-reference pass
// INTRA-target to synthesize the directory→directory coupling edges that files within
// one Swift module never express via `import`. Framework and SPM targets are kept
// whole (they are import units addressed by name); test bundles still collapse per
// bundle. A monolithic iOS app target now surfaces its internal structure in
// package_metrics (per-directory Ca/Ce/abstractness) instead of appearing as one
// 400+-type package. Cached Swift snapshots must re-extract.
// v132: the Go extractor emits a topic-reference fact (KindStorage,
// storage_kind=topic, messaging=kafka) for the two ways a Kafka topic string reaches
// Go code without a literal at the call site — an envconfig `default:` tag on a
// *Topic-suffixed config field, and env.Get("<KEY>_TOPIC", "<default>"). Both anchor
// on an explicit topic marker, so an in-process event bus (whose Subscribe takes a Go
// event symbol, not a topic string) emits nothing by construction. The cross-repo
// linker gained a matching signal that binds consumer -> producer by the topic name's
// owning-service prefix (the same leading-segment-to-repo resolution the import linker
// uses), making asynchronous coupling visible where no import, call, or route exists;
// a repo's own topic and a topic owned by no loaded repo draw no edge. Go does not
// participate in the extractor cache, so this bump documents the behavior change
// rather than invalidating anything.
// v133: Python FastAPI routers are no longer invisible. Route decorators on a def
// nested in a function body are extracted (the router-factory idiom `def
// get_x_router(): router = APIRouter(); @router.post("/") ...`, which module-level
// statement walking never reached — v117 fixed only its dead-code attribution), and
// a repo-wide fixpoint folds include_router mount prefixes onto the bare decorator
// path, so a route is stored at the path it actually serves ("/api/v1/cognify", not
// "/"). Mirrors the Go (v125) and Axum (v130) prefix composition, keyed by the
// decorator's receiver rather than by line span; an unresolved or ambiguous mount
// keeps the bare path, and Flask Blueprint url_prefix stays unfolded (GAP-PY-06).
// TypeScript module facts gained package_name (the nearest package.json name), which
// the cross-repo import linker reads to recognize a repo's own npm @scope.
// v134: the TypeScript HTTP-client extractor reads a call's verb from that call's
// OWN options-object literal (brace-matched from the second argument) instead of a
// flat 200-byte window scanned forward from the URL. The window let a later call's
// `method:` bleed backwards, so a plain fetch("/a/b") sitting above a POST was
// emitted as POST — a wrong verb on a real path, which the cross-repo linker then
// matches against the wrong server route or fails to match at all. Options passed
// as a variable carry no readable verb and now fall back to GET rather than
// adopting an unrelated one. Mirrors how Pass 3 already scopes its scan with
// enclosingObject.
// v135: "**/testdata/**" joins the default ignore globs, and the two extractors
// that walk the repo themselves rather than consuming the engine's filtered file
// list — OpenAPI (Extract) and gRPC (Detect) — repeat the exclusion in their own
// skipDir, which the globs cannot reach. Go reserves testdata/ for fixtures, and
// a fixture repo is a whole miniature codebase: enola's own testdata/repos/**
// supplied every one of its detected outbound HTTP call sites, manufacturing a
// cross-repo coverage gap for a service that makes no outbound calls at all.
// Fixture-driven tests are unaffected — they root each scan INSIDE the fixture,
// so the scanned-relative paths contain no testdata/ segment to match.
// v136: the Python extractor implements plugin.TestRefExtractor, so a production
// symbol exercised only by a pytest file is no longer mis-reported as dead — on a
// real corpus ~58% of Python symbols with no incoming call edge were imported or
// called from tests. plugin.TestRefExtractor now also receives the production file
// list: Python spells an absolute-import target as a dotted path ("pkg.mod.func")
// and can only rewrite it to a canonical symbol name by checking that path against
// the set of files that exist, which the engine alone knows post-ignore-globs.
// The pass emits ONLY KindTestRef facts — never symbols, modules or routes — so
// re-reading files the ignore globs exclude cannot put a pytest fixture's
// include_router prefix back into the production route graph (the v135 regression).
// The global symbol index is deliberately not rebuilt (Extract's Pass 1 is the
// expensive half of Python extraction), so receiver-typed method calls go
// unresolved; an unresolved target is dropped, never guessed, leaving its symbol
// the dead-code candidate it already was.
// v137: Python call targets imported through a package __init__.py re-export now
// resolve to the module that DEFINES the symbol instead of dangling as a dotted
// string. A package is a directory, so "from pkg.sub import thing" left a target
// "pkg.sub.thing" whose prefix matched no module file (the set holds
// pkg/sub/__init__ and pkg/sub/thing, never pkg/sub) — 674 such targets carrying
// 4,660 call edges in one corpus, including every router factory its API
// composition root mounts, so find_path from that file reached none of them. The
// walker already recorded the data (Props["reexports"] plus the import target);
// resolveCallTargets now indexes it, keyed by full package dir so a lookup is
// exact. Exact module resolution still runs first, making this purely additive; a
// name re-exported from two modules in one package stays dotted rather than bind
// arbitrarily. This is a GRAPH fix, not a dead-code one — the orphans explainer's
// short-name matching already treated these symbols as used.
// v138: two more Python call-target resolution fixes, both about targets that were
// wrong or retained rather than merely unresolved.
//
// (a) Suffix matching and the internal-root gate accepted ANY directory name at
// any depth, so an internal dir sharing a name with a third-party package captured
// its imports — a repo with ".../databases/relational/sqlalchemy" and
// "cognee/alembic" resolved plain `import sqlalchemy` internally, marking those
// dependencies source:internal and keeping ~500 third-party call edges as
// first-party. Both now apply Python's actual rule: a directory is a top-level
// package only if its parent is not itself one (no __init__.py). With no package
// information the rule cannot fire, so callers that do not track __init__.py keep
// the historical permissive behaviour.
//
// (b) resolveAbsolute drops trailing segments on failure, which is right for an
// import but wrong for a call target: "pkg.mod.Cls.method" resolved to pkg/mod and
// then kept only the last segment, yielding "pkg/mod.method" — not a dangling
// target but a WRONG one, pointing at whatever else bore that name. Resolution now
// walks the module/symbol split point leftwards with an EXACT module lookup, so the
// class segment moves into the symbol part. A multi-segment symbol part is only
// accepted if it names a symbol the snapshot actually has; unconfirmed candidates
// stay dotted rather than minting a plausible-but-wrong edge.
// v139: two new signals that let the dead-code detector drop findings it can never
// act on. Facts from a file carrying a codegen banner ("DO NOT EDIT", "generated
// by", "@generated" — matched against the file head, so it works for any generator
// and any language) gain generated=true; a generator emits a whole API surface
// regardless of how much the project calls, so its unreferenced half is guaranteed
// noise. Python functions registered with a framework by decorator (FastAPI
// @app.exception_handler/@app.middleware/@app.on_event/@router.websocket, Modal
// @app.local_entrypoint and — gated on the file importing modal, since the names
// alone are far too generic — @app.function/@app.cls) gain
// framework_registered=true, the same treatment click/Typer commands already got.
// Facts are flagged, never dropped: callers of generated code must still resolve,
// so only the dead-code judgement changes.
// v140: two Python reference gaps, each making live code read as dead.
//
// importableRoots stopped scanning a path at the first package parent, but a
// directory with no __init__.py BREAKS the package chain and starts a new source
// root, so a segment below a package can itself be importable. A non-package
// directory nested in a package puts its own siblings on sys.path, and
// "from corpus import read_source" between two files there is a real internal
// import; classifying those external made a whole analysis tree read as dead. Now
// judged per position, matching buildSuffixIndex — which already did — so a
// like-named third-party directory whose parent IS a package stays excluded (v138).
//
// A function handed to a decorator as a VALUE (@override_run_tasks(run_tasks),
// @register(handler)) is a real use: the decorator stores it and the framework
// invokes it later. The decorator-argument walk looked only for nested CALLS, so a
// bare identifier slipped past and the referenced function had no incoming edge.
// v141: two TypeScript HTTP-client detection gaps, both of which made outbound
// calls vanish from the graph entirely — not counted detected, external OR
// unresolved, so the cross-repo residual under-reported with no sign it had.
//
// The optional type-argument group in every client-call pattern was "<[^>]*>",
// which cannot span a NESTED type argument: on fetch<ApiResponse<Foo>>(…) the inner
// class stops at the first ">", the following "\s*\(" meets a ">" and fails, and
// (RE2 having no recursion) backtracking to the empty alternative then meets "<".
// One level of generics matched, two silently did not — and this hit the paths the
// extractor advertises, openapi-fetch's API.GET<ApiResponse<T>>(…) among them. The
// group is now bounded on "(", which a type argument never contains.
//
// Lowercase verb calls — axios.get('/path'), http.post('/path'), the dominant
// hand-written idiom — matched nothing at all. They were excluded to avoid
// colliding with map.get()/cache.delete(), which is a real hazard; the new
// lowerVerbCall pays for admitting them by requiring a "/"-rooted literal argument,
// a condition cleanTSPath already enforces downstream, so no collection lookup can
// reach it. A lowercase call on an interpolated template (`${base}/x`) is still
// deliberately not matched.
//
// Admitting lowercase verbs immediately required a guard it did not need before:
// a supertest call in an e2e suite — request(app).get('/v2/me') — is byte-identical
// in shape to production axios traffic. Client-route extraction is therefore now
// gated on !facts.IsTestPath, because a test's HTTP traffic is not an architectural
// dependency, and facts.IsTestPath learned the two e2e conventions its .spec/.test
// suffixes could never match (".e2e-spec.ts", Nest's generated form, and ".e2e.ts",
// Playwright's — the hyphen means neither carries the leading dot those entries
// require). Ungated, a NestJS API's own test suite became 500+ client routes,
// promoted the service from isolated to connected, and fabricated a cross-repo
// dependency edge out of test traffic — the paths matched a real server because
// they are the routes under test. Same principle as v-era GAP-XL-15, which keeps
// test_ref facts out of the coupling graph.
// v142: TypeScript gains its first SERVER-side route DSL. Until now every `role` the
// extractor wrote was "client" — server routes existed only for file-based routers
// (Next.js, Nuxt, SvelteKit) — so a decorator-routed backend contributed zero routes,
// every client call against it fell into the unresolved residual, and the backend was
// classified `isolated`, i.e. a leaf.
//
// A class carrying @Controller (NestJS) or @controller (InversifyJS) now emits one
// server route per verb-decorated method, composing the class base path with the
// method sub-path through facts.JoinRoutePath. Both argument forms are read —
// @Controller("/users") and @Controller({path: "/users"}), the latter being the form
// real NestJS code overwhelmingly uses. Emission is gated on the controller
// decorator, so a generic @Get on an ordinary class mints nothing, and the two
// frameworks' verb vocabularies are kept separate so a class cannot mix them.
//
// Two things are deliberately NOT composed into the path, because the decorator does
// not determine them: a `version:` property (NestJS versioning may be header- or
// media-type-based) and the application's global prefix (routinely read from the
// environment). The linker's >=2-segment suffix match resolves the difference.
//
// Gated on facts.IsTestPath like v141's client side: an e2e fixture's controller
// would otherwise mint server routes no production client calls, which is a false
// unused-route finding rather than a discovery.
// v143: the other half of TypeScript's server side — routes registered by CALL
// rather than by decorator. `<recv>.<verb>('/path', handler)` is the shape Express,
// Fastify, Hono and Koa/Oak all share, so one pass covers the family, and both the
// ESM and CommonJS binding forms are read (`const app = require('express')()` is as
// common as the import form, and matching only the latter found zero routes on the
// one real Express server available to measure against).
//
// The shape is also v141's CLIENT shape — axios.get('/x') and router.get('/x') are
// the same text — so the two passes are separated by RECEIVER BINDING, resolved per
// file. A receiver bound to an app or router registers routes; anything else stays a
// client call, unchanged. The client pass skips known server receivers so a single
// call site cannot be emitted twice, once in each direction; measured on the corpus,
// that also corrected two registrations v141 had been reporting as outbound calls.
//
// A sub-router with no visible mount emits NOTHING. Its paths are fragments —
// router.post('/login') in a module mounted at '/webhooks' elsewhere serves
// '/webhooks/login' — so emitting '/login' would be a wrong fact, and a wrong path can
// false-match another repo's route, which is worse than silence. Mounts declared in
// the same file are composed; cross-file mount resolution needs a repo-wide pass and
// is deliberately not attempted. Bare catch-alls (app.get('*')) are skipped for the
// same reason: a SPA fallback is not an endpoint and would match any client path.
// v144: two annotation/chain forms that real code uses and the extractors did not
// read, each found by adding a production repository to the benchmark corpus rather
// than by inspection — which is the point of having one.
//
// Kotlin/Retrofit: `@GET(value = "topics")`. Kotlin permits any single-argument
// annotation to be written with its argument named, and Google's reference Android
// app writes EVERY endpoint that way. Matching only the positional form yielded zero
// client routes there, and a Retrofit interface with no routes contributes no
// mobile-to-backend edges at all — so "which screens break if I change this
// endpoint" answered nothing, silently, on an entire class of Android codebase.
//
// Rust/Axum: `.route("/x", get(handler).layer(mw))`. A non-verb method in the
// MethodRouter chain was treated as a terminator, so per-route middleware — which is
// idiomatic Axum — discarded the verbs beneath it and dropped the route entirely,
// with `get` sitting in plain sight. Non-verb methods are now transparent: the walk
// recurses past them and keeps the chain below. Only ever applied to the second
// argument of `.route(path, …)`, which is a MethodRouter by construction, so any
// method on it is a wrapper around one.
//
// Unchanged, and still deliberate: `.route(path, handler_var)` emits nothing. There
// is no verb to infer, and inventing one would produce a route that could false-match
// another repository's endpoint — worse than the visible gap.
// v145: TypeScript alias resolution picked among OVERLAPPING aliases by Go map
// iteration order, taking the first match rather than the most specific one.
//
// Wrong twice over. tsconfig `paths` resolution is most-specific-first, so a project
// mapping both "@acme/schema" and "@acme/" means the former for "@acme/schema/x" —
// the old code could resolve it to either. And because map iteration is randomized,
// "either" meant a DIFFERENT answer on different runs of the same unchanged tree.
//
// Measured on a 15k-file monorepo that maps one package both to its source and to its
// built output: 2 facts of 163,582 flipped between runs — enough that three
// consecutive `enola check` runs on an untouched tree reported `edges +1/-1`, then
// `+6/-6`, then clean. It was the only repository of 38 that failed byte-level
// reproducibility, and the failure is the expensive kind: a delta tool that invents
// churn is worse than one that is merely incomplete, because invented churn is
// indistinguishable from a real change.
//
// Longest matching prefix now wins, ties broken on the prefix string so the result is
// a total order rather than a less-arbitrary one.
// v146: package.json is now read DIRECTLY FROM DISK by the TypeScript extractor rather
// than from the engine's ignore-glob-filtered file list, so a config that drops JSON as
// data noise can no longer disable the module `package_name` prop.
//
// The bundled mcp-arch.yaml ignores "**/*.json" — a reasonable thing to write, and the
// only config most snapshots actually run under. Under it, collectPackageNames saw no
// package.json at all, so NO module carried package_name, so the cross-repo linker's
// own-@scope guard could not fire. A repo importing a sibling package it publishes
// itself ("@acme/native-darwin-arm64" from the repo that publishes "@acme/sdk") was
// reported as depending on whatever other repo happened to be labelled "acme".
//
// Observed on a real pair: a Rust repo publishing @scope-prefixed platform packages drew
// a fabricated import edge to the Python repo of the same name — an edge pointing the
// wrong way down a dependency that does not exist.
//
// Nothing caught it in either direction. No error was raised, and the golden fixtures
// kept passing because TestGolden builds its engine from config.Default(), which has no
// such glob — so the fixture and the shipped config had been disagreeing for as long as
// the guard existed. This is the same principle the OpenAPI and Symfony-config
// extractors already apply: the globs exist to suppress config/data noise, not to hide
// architecturally meaningful files.
//
// Because the globs no longer apply to this read, the directories that must not be
// descended into are named in the extractor instead: tsSkipDirs (shared with the
// alias-root walk), dot-directories, and testdata — node_modules being the critical one,
// since a dependency's package.json would otherwise read as one the repo publishes.
// v147: the Go extractor no longer reads ENGLISH PROSE as SQL. `reSelectFrom` matched a
// bare `FROM <word>` anywhere in any string literal, and "from the", "from what", "from
// one" and "from in" are ordinary English — so a help string or an error message produced a
// storage fact for a table named `the`, `what`, `one` or `in`. It now requires the literal
// to contain a SELECT before a bare FROM is trusted.
//
// Found four separate times while building an unrelated feature, each time as an
// unexplained `storage: +N` in a diff of a change that touched no storage — which is how it
// became clear this was a defect rather than a curiosity. Every instance came from this
// repository's own text; the phrases are quoted in selectGate.
//
// It is worth a version bump for what it is rather than for how many facts it moves. A
// false FINDING is an opinion a reader weighs and can discard. A false FACT is part of the
// layer every finding, every edge and every diff is computed from, and the graph's central
// claim is that its contents are derived rather than guessed. A regex reading prose as SQL
// is guessing; it is simply a regex doing it rather than a model.
//
// Four rules, each measured against Grafana's 6,184 Go files rather than argued for:
//
//  1. A bare FROM is trusted only when the literal also contains SELECT. Not a clause-word
//     list — WHERE, ORDER, GROUP and LIMIT are ordinary English too, and that variant was
//     measured to admit 844 prose literals, which is worse than the bug.
//  2. JOINed tables are now read, but only inside the FROM CLAUSE. Reading them literal-wide
//     reported a table called `time` from a JSON schema whose SELECT was 50 KB away.
//  3. Literals over 16 KB are not searched. 403 real SELECT+FROM literals in Grafana: median
//     65 B, p99 2.9 KB, largest 6.4 KB, none over 8 KB. A document contains every keyword
//     eventually, so only a bound helps.
//  4. `-- …` comments are stripped first: a comment inside a query is prose surrounded by
//     SQL, and removing it is reading the language correctly rather than guessing.
//
// Net on Grafana: 419 storage facts over 108 distinct names that read as a real schema,
// where the previous code produced `IF`, `as`, `by`, `time` and `was` AND missed five real
// tables in one Postgres catalog query. Residual, measured: ~1% still come from English
// genuinely shaped like SQL ("unable to create table response"), which no rule separates
// without also losing lowercase SQL.
// v148: the TypeScript extractor gains Ember/Glimmer support. Template-tag files
// (.gts/.gjs) are parsed by blanking their <template> blocks in place — newlines
// preserved, so every fact's Line is true to the original file — and their
// template references resolve through the file's own import bindings (strict
// mode makes that exact: an identifier a template renders is either imported or
// local). Classic .hbs templates emit a file_ref carrier with their invocation
// names; the new ember-resolver binder joins those, and @service injections, to
// the declared symbols after extraction, skipping anything ambiguous. Router.map
// declarations become page routes with parent paths composed, and ember-data
// model classes gain a storage companion — the same modelling Nuxt/SvelteKit
// routes and ActiveRecord models already get. Before this, a .gts/.gjs/.hbs file
// produced no facts at all: an Ember app's component architecture was invisible
// while its plain-.ts half made the graph look populated.
// v149: Ember routes become first-class graph citizens, and Ember's test tree
// stops reading as production code. Router.map composition honors resetNamespace
// (the route NAME restarts at the segment while the URL path keeps nesting — on
// one production router this was the difference between 12 and 406 of 510
// routes binding); each router-map route gains a handled_by edge to the route
// class its dot-name resolves to; `<LinkTo @route=…>` names and literal
// `transitionTo`/`replaceWith` arguments become navigation edges to route
// facts, with the implicit `.index` child resolving to its parent. Page-type UI
// routes (Ember, Nuxt, SvelteKit, Next pages, Vue router configs) are excluded
// from cross-repo HTTP server indexing — a browser navigation URL is not a
// served endpoint, and indexing one manufactured false unused-route findings.
// tests/**/*-test.{js,ts,gjs,gts} joins the default ignore + test globs: the
// hyphenated suffix is ember-cli's reserved convention INSIDE tests/, and
// without the entry an app's acceptance suite indexed as production symbols
// (the directory is demanded because a bare *-test.ts also swallows an
// experimentation util named ab-test.ts — the Ruby _test.rb precedent).
// v150: the ember-complete-coverage epic lands in one pass. Container-resolved
// role classes (adapters, serializers, transforms, initializers, routes,
// controllers) gain framework_registered so live singletons stop reading as
// dead code — the Python v139 precedent applied to Ember's container.
// @attr('type') binds models to their app-defined transforms; literal
// {{component "x"}}/(helper …)/(modifier …) forms resolve as typed
// invocations; loading/error substate and index templates find their parent
// routes. V1 addon publishes resolve by chasing the recorded re-export stubs
// (which also resolves barrels everywhere); engines compose this.mount with
// their buildRoutes maps when the mount is unique, and engine templates
// resolve in their own isolated tree. Contextual components join two recorded
// literals — a yield-hash entry and a block-param consumption. Pods and the
// classic templates/components split join the candidate fragments (layouts
// are fragments, never a mode). File-local single-assignment string constants
// fold into name arguments (derivation, not inference), and irreducibly
// dynamic sites are counted with capped samples — visible, never guessed.
// v151: the deterministic-coverage epic, part 1 — GraphQL contracts, React
// Navigation, Sequel. graphql-ruby root-type `field` declarations and client
// gql-tagged/.graphql operations emit route facts named by root field
// (`Query.pageViews`, type=graphql), joined cross-repo by a new directional
// signal on the exact name — the HTTP linker's shape for GraphQL — while
// staying out of HTTP path matching the way gRPC does; schema COPIES on the
// client side (type-definition blocks, codegen inputs) deliberately emit
// nothing. React Navigation screen registrations become page routes handled_by
// their imported components, and literal navigate()/push() calls become
// navigation edges from the enclosing symbol (the Ember router mechanism,
// text-scanned because React Native writes JSX in .js). Sequel::Model classes
// emit the ActiveRecord companion storage shape, the literal dataset argument
// winning as the physical table. Part 2, same version (unreleased): two new
// extractors. hcl reads Terraform with an exact block scanner — blocks become
// symbols addressed as Terraform addresses them, references (prefixed,
// declared-set bare addresses, depends_on lists) become depends_on edges, and
// a module block's literal local source draws the directory dependency, remote
// sources marked external. ansible walks the repository itself (YAML is
// ignore-globbed — the OpenAPI self-walk precedent): plays depend on the roles
// they list, import_role/include_role literal names draw role-to-role edges,
// and .j2 templates are counted without ever being rendered.
const cacheVersion = "v151"

// extractorCache holds per-extractor facts keyed by a content hash of the files
// the extractor depends on. It is loaded from disk at the start of a snapshot and
// written back at the end, carrying forward only the keys used this run so stale
// entries are garbage-collected.
//
// Reuse is correct because an extractor is a deterministic function of its inputs
// (verified: parallel and serial runs produce byte-identical facts), and a key
// captures every input that can change its output — see computeExtractorKeys.
type extractorCache struct {
	prev map[string]json.RawMessage // loaded from disk
	next map[string]json.RawMessage // to persist (this run's keys only)
	hits int
}

// buildIdentity identifies the binary that produced a cache entry: the release
// version, plus the executable's size and modification time.
//
// cacheVersion alone is not enough, because it is a constant a human has to remember
// to bump. When an extractor's behaviour changes and the bump is missed — routine
// while developing, and possible in a release — every cache written by the old binary
// keeps being served by the new one, silently, for files whose contents never changed.
// That is not a theoretical risk: it produced a snapshot 568 facts larger than a
// cold run of the same binary on the same tree, which then surfaced as hundreds of
// phantom "removed" facts in a diff against a snapshot taken after the cache caught up.
// For a tool whose value rests on "a clean diff means something", facts that depend on
// which binary happened to write the cache are the most damaging kind of wrong.
//
// Size and mtime rather than a content hash: the binary is tens of megabytes and this
// runs on every snapshot, whereas a stat is free. The failure mode of the cheap check
// is a needless re-parse (a rebuilt-but-identical binary), never a stale reuse.
func buildIdentity() string {
	id := version.Version
	if exe, err := os.Executable(); err == nil {
		if fi, err := os.Stat(exe); err == nil {
			id = fmt.Sprintf("%s|%d|%d", id, fi.Size(), fi.ModTime().UnixNano())
		}
	}
	return id
}

// cacheFile is the on-disk shape of the extractor cache.
type cacheFile struct {
	Version string                     `json:"version"`
	Build   string                     `json:"build,omitempty"`
	Entries map[string]json.RawMessage `json:"entries"`
}

// loadExtractorCache reads the cache file at path. A missing, unreadable, or
// foreign-build file yields an empty (but usable) cache, so caching degrades to a
// full run rather than to wrong facts.
func loadExtractorCache(path string) *extractorCache {
	c := &extractorCache{
		prev: map[string]json.RawMessage{},
		next: map[string]json.RawMessage{},
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return c
	}
	var on cacheFile
	if err := json.Unmarshal(data, &on); err != nil || on.Version != cacheVersion {
		return c // treat schema mismatch as a cold cache
	}
	// A cache written by a different binary is discarded wholesale. Entries carry no
	// record of which extractor logic produced them, so there is no safe way to reuse
	// part of it — and a cache written before this field existed has an empty Build,
	// which correctly fails the comparison.
	if on.Build != buildIdentity() {
		return c
	}
	if on.Entries != nil {
		c.prev = on.Entries
	}
	return c
}

// get returns the cached facts for key (deep-copied from JSON, so the caller may
// mutate them freely) and carries the original bytes forward to the next save.
func (c *extractorCache) get(key string) ([]facts.Fact, bool) {
	raw, ok := c.prev[key]
	if !ok {
		return nil, false
	}
	var ff []facts.Fact
	if err := json.Unmarshal(raw, &ff); err != nil {
		return nil, false
	}
	c.next[key] = raw // keep the clean, pre-mutation bytes
	c.hits++
	return ff, true
}

// put stores ff for key. It marshals immediately (before the engine tags or
// otherwise mutates the facts) so the persisted bytes stay clean.
func (c *extractorCache) put(key string, ff []facts.Fact) {
	raw, err := json.Marshal(ff)
	if err != nil {
		return
	}
	c.next[key] = raw
}

// save writes the keys used this run to path, stamped with the binary that produced
// them so a later run by a different build discards rather than reuses them.
func (c *extractorCache) save(path string) error {
	on := cacheFile{Version: cacheVersion, Build: buildIdentity(), Entries: c.next}
	data, err := json.Marshal(on)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// computeExtractorKeys returns a cache key for every extractor that implements
// plugin.FileOwner. A key covers exactly the inputs that can change that
// extractor's output:
//
//   - the content hashes of the files it owns, and
//   - a shared hash of every file no FileOwner owns (configs, manifests, and
//     sources of non-cacheable extractors) — these are where detection markers
//     (tsconfig.json, build.gradle, Gemfile, …) live.
//
// Because cross-language source files never affect another language's output, a
// file owned by a *different* FileOwner is correctly excluded from this
// extractor's key. The shared-config hash over-invalidates (any manifest change
// busts every cache) but never under-invalidates, so reuse is always sound.
func computeExtractorKeys(all []extractors.Extractor, files []string, hashes map[string]string) map[string]string {
	owners := map[string]plugin.FileOwner{}
	for _, ext := range all {
		if fo, ok := ext.(plugin.FileOwner); ok {
			owners[ext.Name()] = fo
		}
	}
	if len(owners) == 0 {
		return nil
	}

	// Partition files: per-owner owned lists + the shared (un-owned) remainder.
	// keyFiles is owned ∪ AffectsKey — the full set whose contents feed the key,
	// so a cross-language file that a KeyDependent extractor reads (but does not
	// own) still invalidates its cache. Ownership (and thus the shared remainder)
	// is decided purely by OwnsFile; AffectsKey only widens the key, never the
	// ownership partition.
	owned := map[string][]string{}
	keyFiles := map[string][]string{}
	var shared []string
	for _, f := range files {
		ownedByAny := false
		for name, fo := range owners {
			owns := fo.OwnsFile(f)
			if owns {
				owned[name] = append(owned[name], f)
				ownedByAny = true
			}
			if owns {
				keyFiles[name] = append(keyFiles[name], f)
			} else if kd, ok := fo.(plugin.KeyDependent); ok && kd.AffectsKey(f) {
				keyFiles[name] = append(keyFiles[name], f)
			}
		}
		if !ownedByAny {
			shared = append(shared, f)
		}
	}
	sharedHash := hashFileSet(shared, hashes)

	keys := make(map[string]string, len(owners))
	for name := range owners {
		h := sha256.New()
		h.Write([]byte(cacheVersion + "\x00" + name + "\x00" + sharedHash + "\x00"))
		h.Write([]byte(hashFileSet(keyFiles[name], hashes)))
		keys[name] = hex.EncodeToString(h.Sum(nil))
	}
	return keys
}

// hashFileSet returns a stable hash over (path, contentHash) pairs, sorted by
// path so the result is independent of the input order.
func hashFileSet(files []string, hashes map[string]string) string {
	sorted := append([]string(nil), files...)
	sort.Strings(sorted)
	h := sha256.New()
	for _, f := range sorted {
		h.Write([]byte(f))
		h.Write([]byte{0})
		h.Write([]byte(hashes[f])) // empty for unreadable files — still deterministic
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// extractorCachePath returns the on-disk location of the cache for a repo.
func extractorCachePath(outDir string) string {
	return strings.TrimRight(outDir, "/") + "/extractor_cache.json"
}
