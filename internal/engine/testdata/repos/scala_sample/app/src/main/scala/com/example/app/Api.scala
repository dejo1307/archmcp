package com.example.app

/** Scala 3 syntax: braceless bodies, enum, given, extension. */
enum Status(val code: Int):
  case Active extends Status(1)
  case Closed extends Status(0)

enum Color:
  case Red, Green, Blue

given Ordering[Status] with
  def compare(a: Status, b: Status): Int = a.code - b.code

extension (s: String)
  def shout: String = s.toUpperCase

trait Store[F[_]]:
  def get(id: Long): F[Option[String]]
