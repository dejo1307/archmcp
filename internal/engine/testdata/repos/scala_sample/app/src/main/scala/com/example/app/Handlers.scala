package com.example.app

/** An anonymous class is where Scala puts implementations, and its body was dropped
  * whole before v185 — so a method whose only call site lives in one read as dead.
  * Both spellings are here: Scala 3's braceless `new:` and the classic `new T { }`. */
object Handlers {

  def make(): Runner =
    build { x =>
      new:
        val prepared = normalize(x)
        def run(): Unit = dispatch(prepared)
    }

  def legacy(): Runner =
    new Runner {
      val prepared = normalize(0)
      def run(): Unit = dispatch(prepared)
    }

  /** Called WITHOUT parentheses, which Scala's uniform access principle permits for
    * a parameterless method. The grammar reports it as a field access, so before v186
    * it produced no edge and `ready` read as dead code. */
  def poll(): Boolean = source.ready

  def normalize(x: Int): Int = x
  def dispatch(x: Int): Unit = ()
  def build(f: Int => Runner): Runner = f(0)
}

trait Runner { def run(): Unit }

object source { def ready: Boolean = true }
