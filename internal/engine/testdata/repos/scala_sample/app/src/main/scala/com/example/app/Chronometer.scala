package com.example.app

/** A timer, not a web server. `path` is an ordinary method name, and matching the
  * Pekko directive without the routing import produced routes from this shape. */
object Chronometer {
  def record(result: String): Unit = path(result)
  def path(r: String): Unit = ()
}
