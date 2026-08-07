package com.example.core.test

import com.example.core.User

/** A shipped fixture LIBRARY: production code living under a directory named
  * `test`. zio-test is the real case this pins — its presence in the golden is
  * the assertion that the Scala test globs scope to the sbt source set. */
object Fixtures {
  def sample(): User = new User(1L, "sample", null)
}
