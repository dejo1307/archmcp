package com.example.app

/** A test source set. It contributes no SYMBOLS and no module — so it can never
  * become a dead-code candidate itself — but its outbound REFERENCES are kept as a
  * single test_ref fact, so `Service`, exercised only from here, does not read as
  * dead. Both halves are the assertion. */
class ServiceSpec {
  def testFind(): Unit = {
    val svc = new Service(null)
    assert(svc.id == 0L)
  }
}
