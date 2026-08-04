package rubyextractor

import (
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

func TestGraphQLRubyRoutes_RootFieldsOnly(t *testing.T) {
	src := []byte(`module Types
  class QueryType < Types::BaseObject
    field :page_views, [Types::PageViewType], null: false
    field :company, Types::CompanyType do
      argument :id, ID, required: true
    end
  end
end
`)
	ff := extractGraphQLRubyRoutes(src, "app/graphql/types/query_type.rb")
	if len(ff) != 2 {
		t.Fatalf("got %d routes, want 2: %+v", len(ff), ff)
	}
	if ff[0].Name != "Query.pageViews" {
		t.Errorf("name = %q, want the camelized root field", ff[0].Name)
	}
	if ff[0].Props[facts.PropRouteType] != facts.RouteTypeGraphQL || ff[0].Props[facts.PropRole] != facts.RoleServer {
		t.Errorf("props = %v", ff[0].Props)
	}
	if got := extractGraphQLRubyRoutes([]byte("class UserType < Types::BaseObject\n  field :name, String\nend\n"), "app/graphql/types/user_type.rb"); got != nil {
		t.Errorf("non-root type emitted %v — schema internals are not operations", got)
	}
	if got := extractGraphQLRubyRoutes(src, "app/models/query_type.rb"); got != nil {
		t.Errorf("outside a graphql dir emitted %v", got)
	}
}

func TestGraphQLRubyClient_QuotedAndHeredocOperations(t *testing.T) {
	src := []byte(`class SessionsJob
  def insights_query
    "query ($dateRange: DateRangeAttributes!) {
      pageviews(dateRange: $dateRange) {
        count
      }
      visits {
        count
      }
    }"
  end

  def mutation_doc
    <<~GQL
      mutation {
        trackEvent(name: "x") {
          id
        }
      }
    GQL
  end
end
`)
	ff := extractGraphQLRubyClientOps(src, "app/jobs/sessions_job.rb")
	var names []string
	for _, f := range ff {
		names = append(names, f.Name)
	}
	want := []string{"Query.pageviews", "Query.visits", "Mutation.trackEvent"}
	if len(ff) != 3 || names[0] != want[0] || names[1] != want[1] || names[2] != want[2] {
		t.Fatalf("client ops = %v, want %v", names, want)
	}
	for _, f := range ff {
		if f.Props[facts.PropRole] != facts.RoleClient || f.Props[facts.PropRouteType] != facts.RouteTypeGraphQL {
			t.Fatalf("op %s lacks client graphql props: %+v", f.Name, f.Props)
		}
	}
}

func TestGraphQLRubyClient_RubyBlocksAndServerFilesExcluded(t *testing.T) {
	block := []byte("items = query { |x| x.active }\nresult = mutation { compute }\n")
	if ff := extractGraphQLRubyClientOps(block, "app/services/finder.rb"); len(ff) != 0 {
		t.Fatalf("Ruby block syntax read as an operation: %+v", ff)
	}
	server := []byte("class Types::QueryType < Types::BaseObject\n  field :me\nend\nDOC = \"query { me }\"\n")
	if ff := extractGraphQLRubyClientOps(server, "app/models/query_type.rb"); len(ff) != 0 {
		t.Fatalf("a root-type server file emitted client ops: %+v", ff)
	}
	closing := []byte("return query if value == \"not_started\"\n\n    query.where(fastly_status: BUCKETS.fetch(value, []))\n  end\n\n  def options\n    {\n")
	if ff := extractGraphQLRubyClientOps(closing, "app/avo/filters/fastly_status.rb"); len(ff) != 0 {
		t.Fatalf("a closing quote followed by Ruby code read as an operation: %+v", ff)
	}
	schemaDir := []byte("DESC = \"query { example }\"\n")
	if ff := extractGraphQLRubyClientOps(schemaDir, "app/graphql/types/thing_type.rb"); len(ff) != 0 {
		t.Fatalf("a graphql/ tree file emitted client ops: %+v", ff)
	}
}

func TestGraphQLRubyRoutes_NamespaceQualifiedRootClass(t *testing.T) {
	src := []byte("class Types::QueryType < Types::BaseObject\n  field :event_query, Types::EventQuery, null: false\nend\n")
	ff := extractGraphQLRubyRoutes(src, "app/graphql/types/query_type.rb")
	if len(ff) != 1 || ff[0].Name != "Query.eventQuery" {
		t.Fatalf("namespace-qualified root class = %+v, want exactly Query.eventQuery", ff)
	}
}
