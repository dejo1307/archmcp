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

func TestGraphQLServerSDL_RootFieldsBecomeServerRoutes(t *testing.T) {
	src := []byte(`import { ApolloServer } from '@apollo/server';
const typeDefs = gql` + "`" + `
  type Query {
    books: [Book]
    book(id: ID!): Book
  }
  type Mutation {
    addBook(title: String!): Book
  }
` + "`" + `;
const server = new ApolloServer({ typeDefs, resolvers });
`)
	ff := extractGraphQLServerSDL(src, "src/server.ts")
	var names []string
	for _, f := range ff {
		names = append(names, f.Name)
		if f.Props[facts.PropRole] != facts.RoleServer || f.Props[facts.PropRouteType] != facts.RouteTypeGraphQL ||
			f.Props[facts.PropSource] != facts.RouteSourceGraphQLSDL {
			t.Errorf("props = %v", f.Props)
		}
	}
	want := []string{"Query.books", "Query.book", "Mutation.addBook"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("names = %v, want %v", names, want)
	}
}

func TestGraphQLServerSDL_PlainStringTypeDefs(t *testing.T) {
	// Apollo Server accepts a plain (non-gql-tagged) template literal too.
	src := []byte(`import { ApolloServer } from '@apollo/server';
const typeDefs = ` + "`" + `
  type Query {
    ping: String
  }
` + "`" + `;
new ApolloServer({ typeDefs });
`)
	ff := extractGraphQLServerSDL(src, "src/server.ts")
	if len(ff) != 1 || ff[0].Name != "Query.ping" {
		t.Fatalf("ff = %+v, want exactly Query.ping", ff)
	}
}

func TestGraphQLServerSDL_InlineObjectShorthand(t *testing.T) {
	// typeDefs declared directly inside the ApolloServer constructor call.
	src := []byte(`new ApolloServer({
  typeDefs: gql` + "`" + `
    type Query {
      viewer: User
    }
  ` + "`" + `,
  resolvers,
});
`)
	ff := extractGraphQLServerSDL(src, "src/server.ts")
	if len(ff) != 1 || ff[0].Name != "Query.viewer" {
		t.Fatalf("ff = %+v, want exactly Query.viewer", ff)
	}
}

func TestGraphQLServerSDL_ExtendedType(t *testing.T) {
	// A modular schema splits its root fields across files with "extend type".
	src := []byte(`new ApolloServer({ typeDefs: gql` + "`" + `
  extend type Query {
    reviews: [Review]
  }
` + "`" + ` });
`)
	ff := extractGraphQLServerSDL(src, "src/reviews-schema.ts")
	if len(ff) != 1 || ff[0].Name != "Query.reviews" {
		t.Fatalf("ff = %+v, want exactly Query.reviews", ff)
	}
}

func TestDetectGraphQLServerUsage_NoServerEmitsNothing(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "schema.ts"), []byte("const typeDefs = gql`type Query { unrelated: String }`;"), 0o644); err != nil {
		t.Fatal(err)
	}
	if detectGraphQLServerUsage(dir, []string{"schema.ts"}) {
		t.Fatal("schema-only client repository was identified as a GraphQL server")
	}
}

func TestGraphQLServerSDL_ArgsAndDirectivesDoNotProduceExtraFields(t *testing.T) {
	src := []byte(`new ApolloServer({ typeDefs: gql` + "`" + `
  type Query {
    book(id: ID!, format: String = "hardcover"): Book @deprecated(reason: "use books")
  }
` + "`" + ` });
`)
	ff := extractGraphQLServerSDL(src, "src/server.ts")
	if len(ff) != 1 || ff[0].Name != "Query.book" {
		t.Fatalf("ff = %+v, want exactly one field Query.book (args/directives must not surface as fields)", ff)
	}
}

func TestGraphQLServerSDL_MultilineArgumentsDoNotBecomeFields(t *testing.T) {
	src := []byte("const typeDefs = gql`\n  type Query {\n    search(\n      query: String!\n      limit: Int\n    ): Results\n  }\n`;")
	ff := extractGraphQLServerSDL(src, "src/schema.ts")
	if len(ff) != 1 || ff[0].Name != "Query.search" {
		t.Fatalf("ff = %+v, want exactly Query.search", ff)
	}
}

func TestGraphQLServerSDL_BlockDescriptionsAndBracesInStrings(t *testing.T) {
	src := []byte("const typeDefs = gql`\n  type Query {\n    \"\"\"Description: with { braces }\"\"\"\n    greeting(format: String = \"{name}\"): String\n    goodbye: String\n  }\n`;")
	ff := extractGraphQLServerSDL(src, "src/schema.ts")
	var names []string
	for _, f := range ff {
		names = append(names, f.Name)
	}
	want := []string{"Query.greeting", "Query.goodbye"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("names = %v, want %v", names, want)
	}
}

func TestDetectGraphQLServerUsage_ModularAndGenericConstructor(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "server.ts"), []byte("new ApolloServer<MyContext>({ typeDefs });"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "schema.ts"), []byte("export const typeDefs = gql`type Query { ping: String }`;"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !detectGraphQLServerUsage(dir, []string{"server.ts", "schema.ts"}) {
		t.Fatal("generic ApolloServer constructor was not detected repo-wide")
	}
	ff := extractGraphQLServerSDL([]byte("export const typeDefs = gql`type Query { ping: String }`;"), "schema.ts")
	if len(ff) != 1 || ff[0].Name != "Query.ping" {
		t.Fatalf("modular schema facts = %+v, want Query.ping", ff)
	}
}

func TestGraphQLServerSDL_FrameworkNeutralForms(t *testing.T) {
	src := []byte("const schema = gql`type Query { yoga: String }`;\nconst other = buildSchema(`type Mutation { publish: Boolean }`);")
	ff := extractGraphQLServerSDL(src, "src/schema.ts")
	var names []string
	for _, f := range ff {
		names = append(names, f.Name)
		if f.Props["framework"] != "graphql-sdl" || f.Props[facts.PropSource] != facts.RouteSourceGraphQLSDL {
			t.Fatalf("framework-neutral props = %v", f.Props)
		}
	}
	want := []string{"Query.yoga", "Mutation.publish"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("names = %v, want %v", names, want)
	}
}

func TestDetectGraphQLServerUsage_CommonFrameworks(t *testing.T) {
	for _, source := range []string{
		`import { createYoga } from "graphql-yoga"`,
		`import mercurius from "mercurius"`,
		`import { graphqlHTTP } from "express-graphql"`,
		`import { createHandler } from "graphql-http/lib/use/express"`,
		`makeExecutableSchema({ typeDefs, resolvers })`,
		`buildSchema(typeDefs)`,
	} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "server.ts"), []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
		if !detectGraphQLServerUsage(dir, []string{"server.ts"}) {
			t.Errorf("server signal not detected in %q", source)
		}
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
		t.Fatal("Gradle-nested Apollo documents (app/src/main/graphql/) must detect — the Gradle-layout case")
	}
}

// A gql tag whose interpolation carries its own template literal. The old
// pattern ended the body at the first inner backtick, which cut the document in
// half: the visible cost was a JavaScript variable read as the first root
// field, the quiet one was every real field after the cut never being seen.
func TestGraphQLTag_InterpolatedTemplateInsideTheDocument(t *testing.T) {
	src := []byte("const q = (inOverview = false) => gql`\n" +
		"  query DeviceQuery($dateRange: DateRangeAttributes!) {\n" +
		"    deviceQuery: ${\n" +
		"      inOverview\n" +
		"        ? `sessionPageviewQuery(dateRange: $dateRange) { total }`\n" +
		"        : `combinedQuery(dateRange: $dateRange) { total }`\n" +
		"    }\n" +
		"    candidates {\n" +
		"      id\n" +
		"    }\n" +
		"  }\n" +
		"`;\n")

	var names []string
	for _, f := range extractGraphQLTagFacts(src, "q.ts") {
		names = append(names, f.Name)
	}
	if len(names) != 1 || names[0] != "Query.candidates" {
		t.Errorf("want only the statically named root field, got %v", names)
	}
}

// The word `graphql` followed by a backtick is not always a tag: here the
// backtick CLOSES an ordinary template literal. Reading it as an opening one
// ran the scan to end of file and turned an Apollo options object into a
// document — Query.variables and Query.network, from a file with no GraphQL in
// it at all.
func TestGraphQLTag_WordInAPathIsNotATag(t *testing.T) {
	src := []byte("const link = () => ({\n" +
		"  uri: `${this.config.railsURL}/graphql`,\n" +
		"  defaultOptions: {\n" +
		"    query: {\n" +
		"      fetchPolicy: 'network-only',\n" +
		"      variables,\n" +
		"    },\n" +
		"  },\n" +
		"});\n")

	if got := extractGraphQLTagFacts(src, "apollo.ts"); len(got) != 0 {
		t.Errorf("no tag here, so no facts; got %+v", got)
	}
}
