package com.example.app

import com.example.core.User

/** Phase 2 surface: the loop model, the call graph, and the I/O closure.
  *
  * The two methods below are the pair the whole loop model exists to tell apart —
  * both are `for` comprehensions over one generator, and only one of them repeats.
  */
class Reporting(repo: UserRepo) {

  /** A real iteration: `load` runs once per id, so it is an N+1 candidate. */
  def loadAll(ids: List[Long]): Unit =
    for (id <- ids) { load(id) }

  /** A monadic bind: `enrich` runs exactly once. Counting this as a loop is what
    * would put an N+1 on every effectful method in a Scala codebase. */
  def pipeline(id: Long) =
    for (u <- load(id); e <- enrich(u)) yield e

  /** The generator is evaluated once, so `fetchIds` is not per-iteration work. */
  def report(): Unit =
    for (id <- fetchIds()) { load(id) }

  /** Nested collection combinators compound; `synchronized` is not a loop. */
  def crossJoin(xs: List[Long]): Unit =
    xs.foreach { a => xs.foreach { b => lock.synchronized { combine(a, b) } } }

  /** Reaches the database only through two wrapper hops — performs_io must
    * propagate all the way here for the analyzer to see the N+1 above. */
  def load(id: Long): User = fetchOne(id)
  private def fetchOne(id: Long): User = repo.find(id)

  def enrich(u: User): User = u
  def fetchIds(): List[Long] = Nil
  def combine(a: Long, b: Long): Unit = ()

  private val lock = new Object()
}

/** The direct I/O sink: `db.run` is the point every Slick query reaches the
  * database, and the receiver is what makes the generic method name qualify. */
object Queries {
  def find(id: Long): User = db.run(byId(id))
  def byId(id: Long): Long = id
  def recurse(n: Int): Int = if (n <= 0) 0 else recurse(n - 1)
}
