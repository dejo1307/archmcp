package com.example.app

import com.example.core.{Base, User}
import com.example.core.LegacyId
import com.example.core.Registry.Id

/** Cross-module resolution: `Base` is declared in another sbt project, and
  * `LegacyId` in another LANGUAGE in that project. Both must resolve internal. */
class Service(repo: UserRepo) extends Base {
  def id: Long = 0L

  private val seed = new User(1L, "seed", null)

  def legacy(): LegacyId = new LegacyId(7L)
}

trait UserRepo {
  def find(id: Id): Option[User]
}
