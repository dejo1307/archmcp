package com.example.core

import java.time.Instant

/** A trait becomes an interface; a case class carries its marker. */
trait Base {
  def id: Long
}

sealed abstract class Entity extends Base

case class User(id: Long, name: String, createdAt: Instant) extends Entity

case object Anonymous extends Entity {
  def id: Long = 0L
}

object Registry {
  type Id = Long
  private val counter = 0
  def next(): Id = counter + 1

  object Inner {
    def deep(): Int = 1
  }
}
