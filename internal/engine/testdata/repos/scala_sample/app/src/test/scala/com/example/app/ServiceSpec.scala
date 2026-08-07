package com.example.app

/** A test source set. It must contribute no facts at all — no symbols, no
  * module. There is no ScalaExtractor.ExtractTestRefs yet (that is a later
  * version), so it contributes no reference facts either. */
class ServiceSpec {
  def testFind(): Unit = {
    val svc = new Service(null)
    assert(svc.id == 0L)
  }
}
