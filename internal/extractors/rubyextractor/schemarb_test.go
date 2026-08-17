package rubyextractor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// fixtureSchemaRB describes exactly the database fixtureStructureSQL dumps, in
// the Rails default format: the same three tables, the same columns, the same
// single foreign key. The two must produce the same census, which is what
// TestSchemaRB_MatchesStructureSQLForTheSameDatabase asserts.
const fixtureSchemaRB = `# This file is auto-generated from the current state of the database.

ActiveRecord::Schema[7.1].define(version: 2026_08_12_000000) do
  enable_extension "plpgsql"

  create_table "companies", force: :cascade do |t|
    t.string "name"
    t.boolean "check", default: false
    t.datetime "created_at", precision: 6, null: false
    t.check_constraint "char_length((name)::text) > 0", name: "companies_name_check"
  end

  create_table "employments", force: :cascade do |t|
    t.bigint "company_id", null: false
    t.string "title"
    t.index ["company_id"], name: "index_employments_on_company_id"
  end

  create_table "audit_rows", force: :cascade do |t|
    t.bigint "company_id"
  end

  add_foreign_key "employments", "companies"
end
`

func writeSchemaRB(t *testing.T, repo, src string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(repo, "db"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "db", "schema.rb"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestExtractSchemaRB_TablesColumnsAndForeignKeys(t *testing.T) {
	repo := t.TempDir()
	writeSchemaRB(t, repo, fixtureSchemaRB)
	got := applySchemaDump(repo, nil)
	if len(got) != 3 {
		t.Fatalf("facts = %d, want the three declared tables: %+v", len(got), got)
	}

	companies := factByName(got, facts.KindStorage, "companies")
	if companies == nil || companies.File != schemaRBPath {
		t.Fatalf("companies fact = %+v", companies)
	}
	// The implicit primary key is part of the table pg_dump would have written
	// out; the check_constraint expression is not a column.
	if cols, _ := companies.Props["columns"].(string); cols != "check created_at id name" {
		t.Errorf("companies columns = %q", cols)
	}
	if _, hasFK := companies.Props["fk_constraints"]; hasFK {
		t.Errorf("companies must carry no fk census: %+v", companies.Props)
	}

	employments := factByName(got, facts.KindStorage, "employments")
	if cols, _ := employments.Props["columns"].(string); cols != "company_id id title" {
		t.Errorf("employments columns = %q", cols)
	}
	// The column is not stated, so it is chosen from the ones the table
	// declares: companies is the plural of company.
	if fks, _ := employments.Props["fk_constraints"].(string); fks != "company_id->companies" {
		t.Errorf("employments fk_constraints = %q", fks)
	}

	audit := factByName(got, facts.KindStorage, "audit_rows")
	if cols, _ := audit.Props["columns"].(string); cols != "company_id id" {
		t.Errorf("audit_rows columns = %q", cols)
	}
	if _, hasFK := audit.Props["fk_constraints"]; hasFK {
		t.Errorf("audit_rows must carry no fk census: %+v", audit.Props)
	}
}

// The identity that makes the capability worth having: a constraint written
// against columns or fk_constraints must verdict the same on either dump
// format, so the same database read from either file produces the same props.
func TestSchemaRB_MatchesStructureSQLForTheSameDatabase(t *testing.T) {
	sqlRepo := t.TempDir()
	writeStructureSQL(t, sqlRepo)
	rbRepo := t.TempDir()
	writeSchemaRB(t, rbRepo, fixtureSchemaRB)

	fromSQL := applySchemaDump(sqlRepo, nil)
	fromRB := applySchemaDump(rbRepo, nil)
	if len(fromSQL) != len(fromRB) {
		t.Fatalf("fact counts differ: sql %d, rb %d", len(fromSQL), len(fromRB))
	}
	for _, want := range fromSQL {
		got := factByName(fromRB, want.Kind, want.Name)
		if got == nil {
			t.Fatalf("%s is missing from the schema.rb census", want.Name)
		}
		for _, prop := range []string{"columns", "fk_constraints", "storage_kind", "table", "language"} {
			if got.Props[prop] != want.Props[prop] {
				t.Errorf("%s prop %s: schema.rb %v, structure.sql %v", want.Name, prop, got.Props[prop], want.Props[prop])
			}
		}
	}
}

// A table an ActiveRecord model already claims gets its census folded onto the
// model's storage fact — one table, one storage identity.
func TestExtractSchemaRB_ModelClaimedTableGetsCensusNotASecondFact(t *testing.T) {
	repo := t.TempDir()
	writeSchemaRB(t, repo, fixtureSchemaRB)
	allFacts := []facts.Fact{{
		Kind: facts.KindStorage,
		Name: "Employment",
		File: "app/models/employment.rb",
		Props: map[string]any{
			"storage_kind": "model",
			"table":        "employments",
			"table_source": "derived",
		},
	}}
	got := applySchemaDump(repo, allFacts)
	if factByName(got, facts.KindStorage, "employments") != nil {
		t.Fatal("a model-claimed table must not become a second storage fact")
	}
	model := allFacts[0]
	if cols, _ := model.Props["columns"].(string); cols != "company_id id title" {
		t.Errorf("model fact columns = %q, want the folded census", cols)
	}
	if fks, _ := model.Props["fk_constraints"].(string); fks != "company_id->companies" {
		t.Errorf("model fact fk_constraints = %q", fks)
	}
}

func TestExtractSchemaRB_MissingFileIsSilence(t *testing.T) {
	if got := applySchemaDump(t.TempDir(), nil); got != nil {
		t.Fatalf("no dump must mean no facts, got %+v", got)
	}
}

// Both dumps in one tree is one database written twice, and the SQL format is
// the one a project opted into. Reading both would fold two censuses onto one
// storage identity.
func TestSchemaDump_StructureSQLWinsWhenBothExist(t *testing.T) {
	repo := t.TempDir()
	writeStructureSQL(t, repo)
	writeSchemaRB(t, repo, `ActiveRecord::Schema[7.1].define(version: 1) do
  create_table "only_in_the_ruby_dump", force: :cascade do |t|
    t.string "name"
  end
end
`)
	got := applySchemaDump(repo, nil)
	if factByName(got, facts.KindStorage, "only_in_the_ruby_dump") != nil {
		t.Error("schema.rb must not be read where structure.sql exists")
	}
	if factByName(got, facts.KindStorage, "employments") == nil {
		t.Error("structure.sql must still be read")
	}
}

// The primary key the dumper does not write out is still a column, and the
// forms that say otherwise are read rather than assumed.
func TestExtractSchemaRB_PrimaryKeyForms(t *testing.T) {
	repo := t.TempDir()
	writeSchemaRB(t, repo, `ActiveRecord::Schema[7.1].define(version: 1) do
  create_table "users_roles", id: false, force: :cascade do |t|
    t.bigint "role_id"
    t.bigint "user_id"
  end

  create_table "suspended_usernames", primary_key: "username_hash", id: :string, force: :cascade do |t|
    t.datetime "created_at", null: false
  end

  create_table "uuid_rows", id: :uuid, default: -> { "gen_random_uuid()" }, force: :cascade do |t|
    t.string "name"
  end
end
`)
	got := applySchemaDump(repo, nil)
	for _, tc := range []struct{ table, columns string }{
		{"users_roles", "role_id user_id"},
		{"suspended_usernames", "created_at username_hash"},
		{"uuid_rows", "id name"},
	} {
		fact := factByName(got, facts.KindStorage, tc.table)
		if fact == nil {
			t.Fatalf("%s is missing", tc.table)
		}
		if cols, _ := fact.Props["columns"].(string); cols != tc.columns {
			t.Errorf("%s columns = %q, want %q", tc.table, cols, tc.columns)
		}
	}
}

// The shapes a hand-maintained schema uses that the dumper never writes:
// symbol names, t.references with its derived column and optional constraint,
// and t.timestamps.
func TestExtractSchemaRB_ReferencesAndTimestamps(t *testing.T) {
	repo := t.TempDir()
	writeSchemaRB(t, repo, `ActiveRecord::Schema[7.1].define(version: 1) do
  create_table :companies do |t|
    t.string :name
  end

  create_table :comments do |t|
    t.references :company, foreign_key: true
    t.references :author, foreign_key: { to_table: "companies" }
    t.references :subject, polymorphic: true
    t.references :absent_table, foreign_key: true
    t.timestamps
  end
end
`)
	got := applySchemaDump(repo, nil)
	comments := factByName(got, facts.KindStorage, "comments")
	if comments == nil {
		t.Fatal("comments is missing")
	}
	wantCols := "absent_table_id author_id company_id created_at id subject_id subject_type updated_at"
	if cols, _ := comments.Props["columns"].(string); cols != wantCols {
		t.Errorf("comments columns = %q, want %q", cols, wantCols)
	}
	// The inflected name of absent_table names no declared table, so its
	// constraint is a failed derivation and states nothing.
	if fks, _ := comments.Props["fk_constraints"].(string); fks != "author_id->companies company_id->companies" {
		t.Errorf("comments fk_constraints = %q", fks)
	}
}

// Unrecognized shapes are skipped, never guessed: a foreign key whose column
// cannot be chosen from the ones the table declares states nothing, and neither
// does a create_table whose name is not a literal.
func TestExtractSchemaRB_UnrecognizedShapesAreSkipped(t *testing.T) {
	repo := t.TempDir()
	writeSchemaRB(t, repo, `ActiveRecord::Schema[7.1].define(version: 1) do
  create_table "leaves", force: :cascade do |t|
    t.bigint "book_id"
  end

  create_table "edits", force: :cascade do |t|
    t.bigint "leaf_id"
    t.bigint "other_id"
  end

  create_table "pairs", force: :cascade do |t|
    t.bigint "left_id"
    t.bigint "right_id"
  end

  create_table TABLE_NAME, force: :cascade do |t|
    t.string "name"
  end

  add_foreign_key "edits", "leaves"
  add_foreign_key "pairs", "things"
  add_foreign_key "edits", "books", column: "other_id"
end
`)
	got := applySchemaDump(repo, nil)
	edits := factByName(got, facts.KindStorage, "edits")
	if edits == nil {
		t.Fatal("edits is missing")
	}
	// leaves resolves to leaf_id; the stated column is taken as stated.
	if fks, _ := edits.Props["fk_constraints"].(string); fks != "leaf_id->leaves other_id->books" {
		t.Errorf("edits fk_constraints = %q", fks)
	}
	pairs := factByName(got, facts.KindStorage, "pairs")
	if _, hasFK := pairs.Props["fk_constraints"]; hasFK {
		t.Errorf("no declared column is a plural of things, so nothing is stated: %+v", pairs.Props)
	}
	if factByName(got, facts.KindStorage, "TABLE_NAME") != nil {
		t.Error("a non-literal table name declares nothing")
	}
}

// A schema-qualified name normalizes to the bare table exactly as the SQL
// dump's public.orders does: the two formats must not disagree about which
// table a census belongs to.
func TestExtractSchemaRB_SchemaQualifiedTableName(t *testing.T) {
	repo := t.TempDir()
	writeSchemaRB(t, repo, `ActiveRecord::Schema[7.1].define(version: 1) do
  create_table "archive.orders", force: :cascade do |t|
    t.bigint "customer_id"
  end

  create_table "customers", force: :cascade do |t|
    t.string "name"
  end

  add_foreign_key "archive.orders", "public.customers"
end
`)
	got := applySchemaDump(repo, nil)
	orders := factByName(got, facts.KindStorage, "orders")
	if orders == nil {
		t.Fatalf("the schema-qualified table must be named by its bare name: %+v", got)
	}
	if fks, _ := orders.Props["fk_constraints"].(string); fks != "customer_id->customers" {
		t.Errorf("orders fk_constraints = %q", fks)
	}
}

func TestIsPluralOf(t *testing.T) {
	for _, tc := range []struct {
		plural, singular string
		want             bool
	}{
		{"companies", "company", true},
		{"knives", "knife", true},
		{"leaves", "leaf", true},
		{"people", "person", true},
		{"series", "series", true},
		{"statuses", "status", true},
		{"users", "user", true},
		// Two singulars can share a plural, which is why the predicate only
		// ever filters declared columns and a second candidate is silence.
		{"leaves", "leave", true},
		{"books", "leaf", false},
		{"companies", "compan", false},
		{"users", "use", false},
	} {
		if got := isPluralOf(tc.plural, tc.singular); got != tc.want {
			t.Errorf("isPluralOf(%q, %q) = %v, want %v", tc.plural, tc.singular, got, tc.want)
		}
	}
}
