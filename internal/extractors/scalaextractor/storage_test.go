package scalaextractor

import (
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

func storageFacts(ff []facts.Fact) map[string]facts.Fact {
	out := map[string]facts.Fact{}
	for _, f := range ff {
		if f.Kind == facts.KindStorage {
			out[f.Name] = f
		}
	}
	return out
}

func TestSlickTableStorage(t *testing.T) {
	src := `package p

import slick.jdbc.PostgresProfile.api._

class Accounts(tag: Tag) extends Table[Account](tag, "ACCOUNT") {
  def userName = column[String]("USER_NAME")
}

class Tokens(tag: Tag) extends Table[AccessToken](tag, "ACCESS_TOKEN")
`
	got := storageFacts(extractDSLRoutes([]byte(src), "src/Model.scala"))
	for _, want := range []string{"ACCOUNT", "ACCESS_TOKEN"} {
		f, ok := got[want]
		if !ok {
			t.Fatalf("missing table %q; have %v", want, got)
		}
		if f.Props["storage_kind"] != "table" || f.Props[facts.PropFramework] != "slick" {
			t.Errorf("%s: props = %v", want, f.Props)
		}
	}
}

// TestTopicRequiresAMessagingImport pins the gate that removed a whole false-positive
// class. `topic` is an ordinary domain noun, and a topic fact is not inert: the
// cross-repo linker turns it into a producer/consumer edge by name ownership, so a
// phantom topic invents asynchronous coupling between unrelated services.
func TestTopicRequiresAMessagingImport(t *testing.T) {
	// A forum moderation log. Real code, real names, no broker anywhere.
	domain := `package p

object Modlog {
  val closeTopic = "closeTopic"
  val hideTopic  = "hideTopic"
}
`
	if got := storageFacts(extractDSLRoutes([]byte(domain), "src/Modlog.scala")); len(got) != 0 {
		t.Errorf("domain nouns read as messaging topics: %v", got)
	}

	withKafka := `package p

import org.apache.kafka.clients.producer.KafkaProducer

object Topics {
  val ordersTopic = "orders-v1"
}
`
	got := storageFacts(extractDSLRoutes([]byte(withKafka), "src/Topics.scala"))
	f, ok := got["orders-v1"]
	if !ok {
		t.Fatalf("the gate suppressed a genuine topic; have %v", got)
	}
	if f.Props["storage_kind"] != facts.StorageKindTopic {
		t.Errorf("storage_kind = %v, want %v", f.Props["storage_kind"], facts.StorageKindTopic)
	}
	if f.Props["declared_as"] != "ordersTopic" {
		t.Errorf("declared_as = %v, want ordersTopic", f.Props["declared_as"])
	}
}

func TestInterpolatedTopicIsNotEmitted(t *testing.T) {
	src := `package p

import org.apache.kafka.clients.producer.KafkaProducer

object Topics {
  val ordersTopic = s"orders-${env}"
}
`
	if got := storageFacts(extractDSLRoutes([]byte(src), "src/T.scala")); len(got) != 0 {
		t.Errorf("an interpolated topic name is not knowable here, but was emitted: %v", got)
	}
}

// --- outbound clients ---

func clientRoutes(ff []facts.Fact) map[string]facts.Fact {
	out := map[string]facts.Fact{}
	for _, f := range ff {
		if f.Kind == facts.KindRoute && f.Props[facts.PropRole] == facts.RoleClient {
			m, _ := f.Props["method"].(string)
			out[m+" "+f.Name] = f
		}
	}
	return out
}

func TestHTTPClientCallSites(t *testing.T) {
	src := `package p

import sttp.client3._

class Api(backend: Backend) {
  def load() = basicRequest.get(uri"/api/users")
  def save() = basicRequest.post(uri"/api/users")
  def third() = basicRequest.get(uri"https://api.stripe.com/v1/charges")
}
`
	got := clientRoutes(extractDSLRoutes([]byte(src), "src/Api.scala"))

	for _, want := range []string{"GET /api/users", "POST /api/users"} {
		f, ok := got[want]
		if !ok {
			t.Fatalf("missing client call %q; have %v", want, got)
		}
		if f.Props[facts.PropSource] != facts.RouteSourceScalaHTTPClient {
			t.Errorf("%s: source = %v", want, f.Props[facts.PropSource])
		}
		if f.Props["external"] == true {
			t.Errorf("%s: a relative path must stay internal and linkable", want)
		}
	}
	// An absolute URL names a third party. Marking it external is what stops it
	// being reported as an unresolved internal edge no repository could ever close.
	ext, ok := got["GET https://api.stripe.com/v1/charges"]
	if !ok {
		t.Fatalf("absolute-URL call missing; have %v", got)
	}
	if ext.Props["external"] != true || ext.Props["host"] != "api.stripe.com" {
		t.Errorf("external call props = %v", ext.Props)
	}
}

// TestClientRequiresAnImport is the third instance of the same lesson: `get` and
// `post` are among the most common method names in any codebase, so without the
// import gate every map lookup and builder call becomes an outbound HTTP request.
func TestClientRequiresAnImport(t *testing.T) {
	src := `package p

object Cache {
  def read(k: String) = store.get("/etc/passwd")
  def write(k: String) = store.post("/tmp/x")
}
`
	if got := clientRoutes(extractDSLRoutes([]byte(src), "src/Cache.scala")); len(got) != 0 {
		t.Errorf("ordinary get/post calls read as HTTP requests: %v", got)
	}
}

func TestInterpolatedClientPathIsSkipped(t *testing.T) {
	src := `package p

import sttp.client3._

class Api {
  def load(id: Long) = basicRequest.get(uri"/api/users/$id")
}
`
	if got := clientRoutes(extractDSLRoutes([]byte(src), "src/Api.scala")); len(got) != 0 {
		t.Errorf("an interpolated path is not knowable, but was emitted: %v", got)
	}
}
