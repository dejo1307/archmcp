package gqlscan

import "testing"

// A gql tag is a JavaScript template literal, and `${…}` inside it is not
// GraphQL. Both live cases: an interpolated type name inside a variable list,
// and an interpolated selection fragment inside the body. Neither is a field,
// and the second one's braces are not a selection set.
func TestRootFields_SkipsTemplateInterpolations(t *testing.T) {
	body := `
    pageviewQuery(filters: [` + "${filterType}" + `!]) {
      total
    }
    ` + "${inOverview ? '' : 'numberOfApplications'}" + `
    candidates {
      id
    }
  }`

	got := RootFields(body)
	want := []string{"pageviewQuery", "candidates"}
	if len(got) != len(want) {
		t.Fatalf("want %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("want %v, got %v", want, got)
			break
		}
	}
}

// The damage worth more than the two false facts. A fragment spread at depth 1
// opens a brace the scanner counted, so everything after it read one level too
// deep — and a nested field came back as a root one.
func TestRootFields_InterpolationDoesNotDesyncDepth(t *testing.T) {
	body := `
    candidate(id: $id) {
      ...candidateFields
    }
  }
  ` + "${CANDIDATE_FRAGMENT}" + `
`
	got := RootFields(body)
	if len(got) != 1 || got[0] != "candidate" {
		t.Errorf("only the root field is a root field, got %v", got)
	}
}

// The head has to skip interpolations too. An interpolated type in the variable
// list carries a brace, and stopping there starts the body inside it — which is
// how Query.filterType became a fact.
func TestOperationHead_SkipsInterpolationInVariableList(t *testing.T) {
	doc := "query KpisQuery(\n  $dateRange: DateRangeAttributes!\n  $filters: [${filterType}!]\n) {\n  pageviewQuery {\n    total\n  }\n}"
	heads := OperationHeads(doc)
	if len(heads) == 0 {
		t.Fatal("the operation head should still match")
	}
	m := heads[0]
	got := RootFields(doc[m[1]:])
	if len(got) != 1 || got[0] != "pageviewQuery" {
		t.Errorf("want [pageviewQuery], got %v", got)
	}
}

func TestOperationHeads_RejectsSDLFieldsAndDescriptionProse(t *testing.T) {
	doc := `type SearchInput {
  query: String
}

func TestOperationHeads_RejectsOperationExampleInBlockDescription(t *testing.T) {
	doc := "type Query {\n  viewer: User\n}\n\"\"\"Example:\nquery Fake { viewer }\n\"\"\"\ntype User { id: ID! }"
	if got := OperationHeads(doc); len(got) != 0 {
		t.Fatalf("operation example in block description produced heads: %v", got)
	}
}

"""
query is going to be used by a client.
"""
type Bicycle {
  wheels: Int
}`
	if got := OperationHeads(doc); len(got) != 0 {
		t.Fatalf("SDL text produced operation heads: %v", got)
	}
}

func TestOperationHeads_AcceptsNamedAnonymousVariablesAndDirectives(t *testing.T) {
	doc := `query Named($id: ID!) @relay_test_operation { node(id: $id) { id } }
mutation { save { id } }
subscription Events @live { events { id } }`
	if got := OperationHeads(doc); len(got) != 3 {
		t.Fatalf("want three operation heads, got %d: %v", len(got), got)
	}
}

func TestOperationHeads_DoesNotLetInvalidCandidateConsumeNextOperation(t *testing.T) {
	doc := "type Input {\n  query: String\n}\nquery Real { viewer }"
	heads := OperationHeads(doc)
	if len(heads) != 1 || doc[heads[0][2]:heads[0][3]] != "query" {
		t.Fatalf("want the real operation after an SDL field, got %v", heads)
	}
	if got := RootFields(doc[heads[0][1]:]); len(got) != 1 || got[0] != "viewer" {
		t.Fatalf("want [viewer], got %v", got)
	}
}

func TestOperationScanner_CommentsAndDelimiterStrings(t *testing.T) {
	doc := "query Real # comment\n($value: String = \"{\") {\n  search(value: \") }\") { id }\n  viewer { id }\n}"
	heads := OperationHeads(doc)
	if len(heads) != 1 {
		t.Fatalf("want one operation, got %v", heads)
	}
	got := RootFields(doc[heads[0][1]:])
	want := []string{"search", "viewer"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("want %v, got %v", want, got)
	}
}

// A directive is not a field, and a fragment spread's name is not either. Both
// are reached after a newline, which is exactly what resets the scanner to
// expect a field — so `@connection(key: …)` on the line after its field came
// back as a root field named connection, on seven mobile-client documents.
func TestRootFields_DirectivesAndSpreadsAreNotFields(t *testing.T) {
	body := `
    candidatesConnection(first: $first)
      @connection(key: "CandidateList", filter: ["userScope"]) {
      pageInfo { endCursor }
    }
    ...candidateListFields
  }`

	got := RootFields(body)
	if len(got) != 1 || got[0] != "candidatesConnection" {
		t.Errorf("want [candidatesConnection], got %v", got)
	}
}
