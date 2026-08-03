package facts

// This file is the SHARED VOCABULARY that crosses the extractor -> linker boundary.
//
// An extractor writes a prop value; a linker, binder or explainer in a different
// package reads it back and changes its behavior based on the exact string. That makes
// the value a contract between two packages that never reference each other — and until
// these constants existed, both sides simply spelled the literal out and hoped.
//
// They did not always agree. The cross-repo linker classified an edge as
// via="http-client" from a private list of ten `source` values; the Java extractor emits
// two more ("java-http-client", "feign") that were never added to it, so every Java
// RestTemplate and Feign call site linked as a generic OpenAPI-style "http" edge. No test
// failed, because nothing tied the writing side to the reading side. That is the class of
// bug this file exists to make impossible: the values live here, beside the registry that
// consumes them, and TestContractVocabulary_NoUnregisteredLiterals fails the build if an
// extractor spells one out by hand.
//
// SCOPE. Only values that are READ outside the package that writes them belong here. A
// `framework` value no one branches on (e.g. "gorilla/mux") is descriptive metadata, not
// a contract, and stays a literal at its emission site.

// Prop keys whose VALUES form a cross-package contract (below). Only these keys are
// listed: this is not a general prop-key namespace, and the many descriptive props
// (line counts, exported flags, complexity metrics) deliberately stay literals.
const (
	// PropSource records WHERE a fact came from — which extractor pass, DSL or
	// generator produced it. See the RouteSource* and DepSource* blocks: the key
	// carries two unrelated vocabularies, discriminated by the fact's Kind.
	PropSource = "source"
	// PropFramework names the framework a fact was extracted from. Mostly
	// descriptive; only the values in the Framework* block are branched on.
	PropFramework = "framework"
	// PropRole is which side of a call a route fact represents (RoleClient/RoleServer).
	PropRole = "role"
	// PropRouteType sub-classifies a route beyond HTTP (RouteTypeGRPC,
	// RouteTypeMiddleware). Absent means a plain HTTP route.
	PropRouteType = "type"
)

// Route role values (the PropRole prop on a KindRoute fact): which side of the call
// this route represents. The cross-repo HTTP linker matches RoleClient routes against
// RoleServer ones; a route with no role is treated as a server route, because an
// extractor that found a route declaration without a call site found a served endpoint.
const (
	RoleClient = "client"
	RoleServer = "server"
)

// Route type values (the PropRouteType prop on a KindRoute fact).
const (
	// RouteTypeGRPC marks a route derived from a .proto service definition or a gRPC
	// call site, rather than an HTTP path. Its path is the wire path
	// "/pkg.Service/Method", and grpcimpl binding keys on it.
	RouteTypeGRPC = "grpc"
	// RouteTypeMiddleware marks a registration that wraps other routes rather than
	// serving one. Handler binding skips these: a middleware's "handler" is a
	// middleware func, and binding it to a route would pollute handled_by.
	RouteTypeMiddleware = "middleware"
)

// Framework values that are BRANCHED ON outside the extractor that writes them. Every
// other framework value is descriptive and stays a literal at its emission site.
const (
	// FrameworkGRPC marks a route as a gRPC call site or service definition. The
	// cross-repo linker reads it to label the edge via="grpc" rather than
	// via="http-client", so a gRPC dependency is distinguishable from an HTTP one.
	FrameworkGRPC = "grpc"
)

// RouteSource values — the PropSource prop on a KindRoute fact. This is the provenance
// of the route: which extractor pass or contract format produced it.
//
// The cross-repo linker reads these to decide HOW an edge was established (see
// HandWrittenClientSources), and the gRPC client-FQN binder reads
// RouteSourcePythonGRPCClient to find the routes it must rewrite. Both are behavior
// changes keyed on the exact string, which is what makes these a contract rather than a
// label.
const (
	// Hand-written HTTP call sites: code a human wrote that issues a request.
	RouteSourceGoHTTPClient   = "go-http-client"
	RouteSourceTSHTTPClient   = "ts-http-client"
	RouteSourceRubyHTTPClient = "ruby-http-client"
	RouteSourcePHPHTTPClient  = "php-http-client"
	RouteSourceJavaHTTPClient = "java-http-client" // Spring RestTemplate / WebClient
	RouteSourceFeign          = "feign"            // Spring Cloud @FeignClient interface
	RouteSourceRetrofit       = "retrofit"         // Kotlin/Java Retrofit service interface
	RouteSourceURLSession     = "urlsession"       // Swift URLSession
	RouteSourceSwiftEndpoint  = "swift-endpoint"   // Swift endpoint enum / protocol extension

	// Hand-written gRPC call sites. Same "a human wrote this call" property as the
	// HTTP sources above, over a different transport.
	RouteSourceGoGRPCClient     = "go-grpc-client"
	RouteSourceTSGRPCClient     = "ts-grpc-client"
	RouteSourcePythonGRPCClient = "python-grpc-client"

	// Contract-derived routes: read from a spec or IDL rather than from call-site
	// code. These describe an interface, so they are NOT hand-written call sites.
	RouteSourceGRPCProto         = "grpc-proto"         // .proto service definition
	RouteSourceOpenAPI           = "openapi"            // OpenAPI/Swagger spec
	RouteSourceOpenAPITypeScript = "openapi-typescript" // generated TS client from a spec
	RouteSourceSymfonyConfig     = "symfony-config"     // Symfony YAML/XML route config
)

// HandWrittenClientSources is the set of RouteSource values that mean "a human wrote
// this call site", as opposed to a route derived from a generated client or a contract
// spec. The cross-repo linker reads it to label an edge via="http-client" instead of the
// generic via="http", so a reader can tell a call someone wrote from one a spec implies.
//
// It lives here, beside the constants extractors emit, rather than inside the linker.
// The linker previously kept a private copy, and it silently omitted the two Java
// sources for as long as the Java HTTP-client extractor has existed — the drift this
// file is designed to prevent. Membership is a property of the source, so it is declared
// where the source is.
//
// gRPC sources are members: they are hand-written call sites too. The linker labels them
// via="grpc" first (FrameworkGRPC wins), so their membership only matters if that
// framework prop is ever absent.
var HandWrittenClientSources = map[string]bool{
	RouteSourceGoHTTPClient:     true,
	RouteSourceTSHTTPClient:     true,
	RouteSourceRubyHTTPClient:   true,
	RouteSourcePHPHTTPClient:    true,
	RouteSourceJavaHTTPClient:   true,
	RouteSourceFeign:            true,
	RouteSourceRetrofit:         true,
	RouteSourceURLSession:       true,
	RouteSourceSwiftEndpoint:    true,
	RouteSourceGoGRPCClient:     true,
	RouteSourceTSGRPCClient:     true,
	RouteSourcePythonGRPCClient: true,
}

// AllRouteSources is every registered RouteSource value. It exists for the conformance
// test, which checks that no route fact in the golden corpus carries a source this file
// does not know about — the check that catches a new extractor pass inventing a value
// and never telling the linker.
var AllRouteSources = map[string]bool{
	RouteSourceGoHTTPClient:      true,
	RouteSourceTSHTTPClient:      true,
	RouteSourceRubyHTTPClient:    true,
	RouteSourcePHPHTTPClient:     true,
	RouteSourceJavaHTTPClient:    true,
	RouteSourceFeign:             true,
	RouteSourceRetrofit:          true,
	RouteSourceURLSession:        true,
	RouteSourceSwiftEndpoint:     true,
	RouteSourceGoGRPCClient:      true,
	RouteSourceTSGRPCClient:      true,
	RouteSourcePythonGRPCClient:  true,
	RouteSourceGRPCProto:         true,
	RouteSourceOpenAPI:           true,
	RouteSourceOpenAPITypeScript: true,
	RouteSourceSymfonyConfig:     true,
}

// DepSource values — the PropSource prop on a KindDependency fact. A SECOND, unrelated
// vocabulary on the same prop key, discriminated by the fact's Kind: here `source`
// classifies where an import RESOLVES TO, not which pass produced the fact.
//
// The overload is historical and not worth a migration (renaming a prop key rewrites
// every golden and every saved snapshot), but it must be stated somewhere, because
// reading `source` without first checking Kind gets you a value from the wrong
// vocabulary. Registered here so the conformance test can tell the two apart rather than
// reporting every "internal" as an unknown route source.
const (
	DepSourceInternal = "internal" // resolves to a module inside this repo
	DepSourceExternal = "external" // resolves to a third-party package
	DepSourceStdlib   = "stdlib"   // resolves to the language's standard library
)
