package com.example.app

import org.apache.pekko.http.scaladsl.server.Directives._
import org.http4s.HttpRoutes
import sttp.client3._
import org.apache.kafka.clients.producer.KafkaProducer

/** Pekko HTTP: nested and conjoined verbs, a composed multi-segment prefix, and a
  * prefix held in a value that cannot be resolved without evaluating Scala. */
object AdminApi {
  val routes =
    pathPrefix("api") {
      path("state") { get { complete(OK) } } ~
      (path("disable") & post) { complete(OK) } ~
      pathPrefix("v2" / "admin") { path("ping") { complete(OK) } } ~
      pathPrefix(collection.path) { path("count") { get { complete(OK) } } }
    }
}

/** http4s: the endpoint lives in a pattern rather than in calls. */
object PublicApi {
  val routes = HttpRoutes.of[IO] {
    case GET -> Root / "health"              => Ok()
    case GET -> Root / "users" / LongVar(id) => Ok(id)
    case POST -> Root / "teams"              => Ok()
  }
}

/** Outbound calls the cross-repo linker can join to whoever serves them, plus one
  * third-party call that must NOT read as an unresolved internal edge. */
class Downstream {
  def load() = basicRequest.get(uri"/api/inventory")
  def charge() = basicRequest.post(uri"https://api.stripe.com/v1/charges")
}

/** A MIXIN, not an abstraction: a self-type and a body of concrete definitions, with
  * no abstract member anywhere. This is the ordinary way to compose a Scala service,
  * and counting it as an abstraction reported a package of these as "useless". Its
  * `abstract=false` in the golden is the assertion. */
trait AdminRoutes extends Runner {
  self: Handlers.type =>
  val mounted = true
  def register(): Unit = dispatch(0)
}

/** A real abstraction, for contrast: one member declared and not implemented. */
trait Store {
  def get(id: Long): Option[String]
}

/** A topic constant beside a real broker import: the asynchronous coupling a call
  * graph structurally cannot see. */
object Topics {
  val ordersTopic = "orders-v1"
}
