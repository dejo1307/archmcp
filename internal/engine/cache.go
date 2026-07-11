package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"sort"
	"strings"

	"github.com/enola-labs/enola/internal/extractors"
	"github.com/enola-labs/enola/internal/facts"
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
// only third-party APIs — golf, calling ZeptoMail and MailerLite via base-URL
// concats — accumulated phantom "unresolved internal edges" and was misclassified
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
const cacheVersion = "v106"

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

// loadExtractorCache reads the cache file at path. A missing or unreadable file
// yields an empty (but usable) cache, so caching degrades to a full run.
func loadExtractorCache(path string) *extractorCache {
	c := &extractorCache{
		prev: map[string]json.RawMessage{},
		next: map[string]json.RawMessage{},
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return c
	}
	var on struct {
		Version string                     `json:"version"`
		Entries map[string]json.RawMessage `json:"entries"`
	}
	if err := json.Unmarshal(data, &on); err != nil || on.Version != cacheVersion {
		return c // treat schema mismatch as a cold cache
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

// save writes the keys used this run to path.
func (c *extractorCache) save(path string) error {
	on := struct {
		Version string                     `json:"version"`
		Entries map[string]json.RawMessage `json:"entries"`
	}{Version: cacheVersion, Entries: c.next}
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
