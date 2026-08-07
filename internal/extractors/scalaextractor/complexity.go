package scalaextractor

// Loop, complexity and I/O classification for Scala.
//
// Scala's problem is that iteration and effect-sequencing share a syntax. A
// `for` comprehension desugars to map/flatMap, so `for (u <- users) yield load(u)`
// and `for (a <- fetchA; b <- fetchB(a)) yield b` are the same construct: the first
// runs `load` once per user, the second runs `fetchB` exactly once. No other
// supported language has this ambiguity, and getting it wrong does not merely add
// noise — it manufactures a per-iteration-I/O finding on every effectful service in
// the repository, which is the single largest false-positive class Scala can produce.
//
// Types would settle it and are not available here, so the split is made on the
// syntax that correlates with intent, and the correlation was MEASURED over the
// 8,119 production files of the benchmark corpus rather than assumed. Taking "the
// file imports an effect monad" as a proxy for "this is a bind":
//
//	for … yield        3038 sites   60.4% in effect-importing files
//	for  (no yield)     704 sites    9.7%
//	while / do-while   2337 sites    0.0%
//	.flatMap { }       1646 sites   48.9%
//	.fold { }           140 sites   50.0%
//	.map { }           5492 sites   13.9%
//	.foreach { }       2553 sites    8.0%
//
// So `for … yield` is a bind MORE OFTEN THAN a loop, while the same construct
// without `yield` is iteration six times out of seven. That is the split below, and
// it is structural rather than a name list that could drift.
//
// The ambiguous forms are not discarded — they raise `loop_depth` but not
// `scaling_loop_depth`, the same discount Go and C# apply to a constant-trip loop.
// The performance analyzer reads the scaling variants for its findings and the raw
// ones for evidence, so an ambiguous construct still lowers a finding's confidence
// and annotates it, instead of either inventing an N+1 or vanishing.

// scalingCombinators iterate a collection and repeat their block per element. Every
// one of them is collection-dominant in the corpus measurement above, so counting a
// call inside one as per-iteration work is right far more often than not.
//
// `foldLeft`/`foldRight` are here while bare `fold` is not, and that is the same
// distinction in miniature: the explicitly-directional folds exist only on
// collections, whereas `fold` is also how Option, Try, Either and IO collapse a
// single value.
var scalingCombinators = map[string]bool{
	"foreach": true, "map": true, "filter": true, "filterNot": true,
	"collect": true, "collectFirst": true, "exists": true, "forall": true,
	"count": true, "find": true, "takeWhile": true, "dropWhile": true,
	"partition": true, "groupBy": true, "groupMap": true, "sortBy": true,
	"sortWith": true, "maxBy": true, "minBy": true, "sumBy": true,
	"foldLeft": true, "foldRight": true, "reduce": true, "reduceLeft": true,
	"reduceRight": true, "scanLeft": true, "scanRight": true,
	"zipWithIndex": true, "mkString": true, "distinctBy": true,
	"flatten": true, "indexWhere": true, "span": true, "withFilter": true,
	// Java interop and parallel collections spell the same idea differently.
	"forEach": true, "removeIf": true, "computeIfAbsent": true,
}

// ambiguousCombinators repeat per element on a collection and run exactly once on an
// effect, an Option or a Try — and the corpus says roughly half of each. They raise
// loop_depth so nesting is still visible, but never scaling depth: the analyzer's
// findings are computed from the scaling variants, so an in-loop call reached only
// through one of these is not reported as an N+1.
var ambiguousCombinators = map[string]bool{
	"flatMap": true, "fold": true, "traverse": true, "traverse_": true,
	"mapN": true, "semiflatMap": true, "subflatMap": true, "biflatMap": true,
}

// nonLoopBlockCalls take a by-name block or lambda that runs AT MOST ONCE. They are
// not repetition in any reading, so they neither raise loop_depth nor reset it —
// they are simply transparent. Without this, `synchronized { }` (445 sites),
// `getOrElse { }` (390) and cats-effect's `Resource.use { }` (223) would each read
// as a loop enclosing whatever they wrap.
var nonLoopBlockCalls = map[string]bool{
	"synchronized": true, "getOrElse": true, "orElse": true, "orElseGet": true,
	"use": true, "useForever": true, "bracket": true, "guarantee": true,
	"guaranteeCase": true, "onError": true, "onErrorHandle": true,
	"handleError": true, "handleErrorWith": true, "recover": true,
	"recoverWith": true, "attempt": true, "transform": true, "transformWith": true,
	"tap": true, "pipe": true, "tapError": true, "andThen": true, "compose": true,
	"succeed": true, "suspend": true, "suspendSucceed": true, "delay": true,
	"blocking": true, "interruptible": true, "memoize": true, "cached": true,
	"lazily": true, "withScope": true, "timed": true, "timeout": true,
	"ensuring": true, "finalizer": true, "acquireRelease": true,
	"unsafeRunSync": true, "unsafeRunAsync": true, "runBlocking": true,
	"transaction": true, "withTransaction": true, "inTransaction": true,
	"getOrElseUpdate": true, "fromTry": true, "fromOption": true,
}

// effectConstructors take a block that is BUILT now and run later (or once). Same
// treatment as nonLoopBlockCalls; separated because these are applied as a bare
// name (`Future { … }`, `IO { … }`) rather than on a receiver.
var effectConstructors = map[string]bool{
	"Future": true, "IO": true, "Try": true, "Task": true, "ZIO": true,
	"Some": true, "Option": true, "Right": true, "Left": true, "Sync": true,
	"Async": true, "Resource": true, "Ref": true, "Deferred": true,
	"Stream": true, "Source": true, "Flow": true, "Sink": true,
}

// ioPrimitives are receiver-qualified or bare call targets that perform network,
// filesystem or database I/O directly. A method whose body invokes one is tagged
// io_direct, which a transitive fixpoint then propagates into performs_io so the
// analyzer can recognise a per-iteration call to a wrapper that eventually hits the
// network.
//
// Matched on the method-name segment, so a receiver of any name works. Deliberately
// distinctive names only: a generic `get`/`run`/`execute` names hundreds of
// in-memory operations in a large codebase, and admitting them would tag most of the
// repository as doing I/O, which is the same as tagging none of it.
var ioPrimitives = map[string]bool{
	// JDBC and the SQL libraries that wrap it.
	"executeQuery": true, "executeUpdate": true, "executeBatch": true,
	"executeLargeUpdate": true, "prepareStatement": true, "createStatement": true,
	"getConnection": true, "commit": true, "rollback": true,
	"transact": true, // doobie
	// Slick: `db.run(query)` is the point every Slick query reaches the database.
	"withSession": true, "withTransaction": true,
	// Filesystem.
	"readAllBytes": true, "readAllLines": true, "readString": true,
	"newInputStream": true, "newOutputStream": true, "newBufferedReader": true,
	"newBufferedWriter": true, "createDirectories": true, "copy": true,
	"fromFile": true, "fromURL": true, "fromInputStream": true, "fromResource": true,
	// HTTP clients: sttp, http4s, Play WS, Pekko/Akka HTTP, java.net.
	"singleRequest": true, "expect": true, "fetchAs": true, "toEntity": true,
	"openConnection": true, "openStream": true,
	// Messaging.
	"publish": true, "poll": true, "commitSync": true, "commitAsync": true,
	"subscribe": true,
}

// ioReceivers are receiver tokens that make an otherwise-generic method name an I/O
// call. `db.run(...)` is Slick's entire surface and `run` alone is far too common to
// admit, so the pair is what qualifies.
var ioReceivers = map[string]bool{
	"db": true, "database": true, "session": true, "conn": true, "connection": true,
	"httpClient": true, "client": true, "ws": true, "producer": true, "consumer": true,
	"jdbc": true, "dataSource": true, "sql": true,
}

// ioReceiverMethods are the generic method names that count as I/O only when the
// receiver is one of ioReceivers.
var ioReceiverMethods = map[string]bool{
	"run": true, "runAsync": true, "execute": true, "send": true, "sendAsync": true,
	"query": true, "insert": true, "update": true, "delete": true, "url": true,
}

// classifyBlockCall says how a call that carries a lambda or block affects the loop
// counters, from its method-name segment alone.
type blockCallKind int

const (
	blockCallNone      blockCallKind = iota // not a repetition construct
	blockCallScaling                        // repeats per element: raises both depths
	blockCallAmbiguous                      // repeats on a collection, runs once on an effect
)

func classifyBlockCall(method string) blockCallKind {
	switch {
	case nonLoopBlockCalls[method] || effectConstructors[method]:
		return blockCallNone
	case scalingCombinators[method]:
		return blockCallScaling
	case ambiguousCombinators[method]:
		return blockCallAmbiguous
	}
	return blockCallNone
}

// isIOCall reports whether a call to method on receiver performs I/O directly.
// receiver may be empty for a bare call.
func isIOCall(receiver, method string) bool {
	if ioPrimitives[method] {
		return true
	}
	return receiver != "" && ioReceivers[receiver] && ioReceiverMethods[method]
}
