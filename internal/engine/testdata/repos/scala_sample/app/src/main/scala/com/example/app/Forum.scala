package com.example.app

/** A forum has topics. None of them are Kafka topics, and this file's ABSENCE from
  * the storage facts is the assertion — a phantom topic would invent cross-repo
  * coupling by name ownership. */
object Forum {
  val closeTopic = "closeTopic"
  val hideTopic  = "hideTopic"
}
