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
