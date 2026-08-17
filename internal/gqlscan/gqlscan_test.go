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
	m := OperationHead.FindStringSubmatchIndex(doc)
	if m == nil {
		t.Fatal("the operation head should still match")
	}
	got := RootFields(doc[m[1]:])
	if len(got) != 1 || got[0] != "pageviewQuery" {
		t.Errorf("want [pageviewQuery], got %v", got)
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
