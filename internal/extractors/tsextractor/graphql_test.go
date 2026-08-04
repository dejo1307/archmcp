package tsextractor

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

func TestGraphQLTag_ClientRootFields(t *testing.T) {
	src := []byte("import gql from 'graphql-tag';\nconst Q = gql`\n  query PageStats($id: ID!) {\n    pageViews(companyId: $id) {\n      total\n    }\n    visitors: uniqueVisitors {\n      count\n    }\n  }\n`;\nconst M = gql`\n  mutation {\n    trackEvent(input: {}) {\n      ok\n    }\n  }\n`;\n")
	ff := extractGraphQLTagFacts(src, "src/queries/stats.js")
	var names []string
	for _, f := range ff {
		names = append(names, f.Name)
		if f.Props[facts.PropRole] != facts.RoleClient || f.Props[facts.PropRouteType] != facts.RouteTypeGraphQL {
			t.Errorf("props = %v", f.Props)
		}
	}
	want := []string{"Query.pageViews", "Query.uniqueVisitors", "Mutation.trackEvent"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("names = %v, want %v — aliases resolve to the FIELD, nested fields are not roots", names, want)
	}
}

func TestGraphQLDoc_OperationFileAndSchemaCopy(t *testing.T) {
	ops := extractGraphQLClientOps("query Profile {\n  me {\n    name\n  }\n}\n", "graphql/Profile.graphql", facts.RouteSourceGraphQLOperation)
	if len(ops) != 1 || ops[0].Name != "Query.me" {
		t.Fatalf("ops = %+v", ops)
	}
	schema := extractGraphQLClientOps("type Query {\n  me: User\n}\n", "graphql/schema.graphql", facts.RouteSourceGraphQLOperation)
	if len(schema) != 0 {
		t.Errorf("a schema COPY emitted %v — type-definition blocks are codegen inputs, not client operations", schema)
	}
}

func TestGraphQLTag_FragmentInterpolation(t *testing.T) {
	src := []byte("const Q = gql`\n  query Feed {\n    stories {\n      ...StoryFields\n    }\n  }\n  ${STORY_FIELDS}\n`;\n")
	ff := extractGraphQLTagFacts(src, "src/feed.js")
	if len(ff) != 1 || ff[0].Name != "Query.stories" {
		t.Fatalf("interpolated-fragment operation = %+v, want exactly Query.stories", ff)
	}
}

func TestReactNavDetect_MonorepoExampleApp(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "example"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"framework","workspaces":["example"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "example", "package.json"), []byte(`{"dependencies":{"@react-navigation/native":"^6.0.0"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if !detectReactNavigation(dir) {
		t.Error("a monorepo example app one level down must trigger detection")
	}
}

func TestGraphQLDocDetect_NoTSRoot(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "graphql"), 0o755); err != nil {
		t.Fatal(err)
	}
	doc := filepath.Join(dir, "graphql", "CurrentProfile.graphql")
	if err := os.WriteFile(doc, []byte("query CurrentProfile {\n  currentProfile {\n    id\n  }\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e := New()
	found, err := e.Detect(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("a repo carrying .graphql operation documents with no TS root must detect — the Swift Apollo case")
	}
	bare := t.TempDir()
	if err := os.WriteFile(filepath.Join(bare, "main.swift"), []byte("print(1)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	found, err = e.Detect(bare)
	if err != nil {
		t.Fatal(err)
	}
	if found {
		t.Fatal("a repo with neither TS markers nor GraphQL documents must not detect")
	}
}

func TestGraphQLDocDetect_GradleNestedDocuments(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "build.gradle"), []byte("plugins {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(dir, "app", "src", "main", "graphql")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "Profile.graphql"), []byte("query Profile {\n  me {\n    id\n  }\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e := New()
	found, err := e.Detect(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("Gradle-nested Apollo documents (app/src/main/graphql/) must detect — the aboard-android case")
	}
}
