package rubyextractor

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/enola-labs/enola/internal/facts"
)

// schemaRBPath is where Rails keeps the Ruby-format schema dump. It is the
// default format — structure.sql exists only where a project opted out of it —
// so a reader that only knows the SQL dump is silent on half the Rails world.
const schemaRBPath = "db/schema.rb"

// literalArg matches the two literal forms the DSL accepts for a name — a
// double-quoted string as the dumper writes it, or a symbol as a hand-written
// schema does. Anything else (a variable, an interpolation, an array) is not a
// literal and names nothing.
const literalArg = `(?:"([^"]+)"|:([a-z_][a-z0-9_]*))`

// The recognized ActiveRecord::SchemaDumper shapes, and nothing else. Like the
// pg_dump reader beside it this is deliberately not a Ruby grammar: schema.rb
// is machine-written one statement per line in a handful of stable shapes, and
// a line outside them contributes nothing rather than being guessed at.
var (
	reCreateTableRB = regexp.MustCompile(`^create_table ` + literalArg + `(.*)\bdo \|([a-z_][a-z0-9_]*)\|$`)
	reAddForeignKey = regexp.MustCompile(`^add_foreign_key ` + literalArg + `, ` + literalArg + `(.*)$`)
	reMemberCall    = regexp.MustCompile(`^([a-z_][a-z0-9_]*)\.([a-z_][a-z0-9_]*)\b(.*)$`)
	reFirstLiteral  = regexp.MustCompile(`^[\s(]+` + literalArg)
	reColumnOpt     = regexp.MustCompile(`\bcolumn: ` + literalArg)
	reToTableOpt    = regexp.MustCompile(`\bto_table: ` + literalArg)
	rePrimaryKeyOpt = regexp.MustCompile(`\bprimary_key: ` + literalArg)
	reIDFalse       = regexp.MustCompile(`\bid: false\b`)
	reForeignKeyOn  = regexp.MustCompile(`\bforeign_key: (?:true|\{)`)
)

// Members of a create_table block whose first literal is not a column name: an
// index names the columns it covers, and the constraint family names an
// expression.
var rbNonColumnMembers = map[string]bool{
	"index":                true,
	"check_constraint":     true,
	"exclusion_constraint": true,
	"unique_constraint":    true,
	"foreign_key":          true,
}

// rbTable accumulates one create_table's census as sets, because the DSL can
// state the same column or constraint twice — a t.references carrying its own
// foreign_key beside the add_foreign_key for it — where a pg_dump cannot.
type rbTable struct {
	line int
	cols map[string]bool
	fks  map[string]bool
}

// rbForeignKey is one add_foreign_key awaiting the whole file: its column may
// have to be chosen from what the source table declares, which is not known
// until every create_table has been read.
type rbForeignKey struct {
	from   string
	to     string
	column string
}

// rbReference is one t.references awaiting the whole file: a foreign_key: true
// names its table by inflection, and an inflected name that no create_table
// declares is a failed derivation rather than a constraint.
type rbReference struct {
	from   string
	column string
	assoc  string
	to     string
}

// applySchemaRB parses db/schema.rb when present and returns storage facts for
// the tables it declares, in the same shape the structure.sql reader produces
// for the same database — the two dumps describe one thing, so a rule written
// against the census must not be able to tell which format declared it.
func applySchemaRB(repoPath string, allFacts []facts.Fact) []facts.Fact {
	data, err := os.ReadFile(filepath.Join(repoPath, filepath.FromSlash(schemaRBPath)))
	if err != nil {
		return nil
	}
	return foldTables(parseSchemaRB(string(data)), allFacts, schemaRBPath)
}

// applySchemaDump folds in the database's own account of the schema from
// whichever dump format the project keeps. structure.sql wins outright where
// both files exist: opting into the SQL format is what makes it the
// authoritative dump, and one database read twice would fold two censuses onto
// one storage identity.
func applySchemaDump(repoPath string, allFacts []facts.Fact) []facts.Fact {
	if _, err := os.Stat(filepath.Join(repoPath, filepath.FromSlash(structureSQLPath))); err == nil {
		return applyStructureSQL(repoPath, allFacts)
	}
	return applySchemaRB(repoPath, allFacts)
}

// parseSchemaRB walks the dump line by line: a create_table opens a member scan
// that runs to its `end`, add_foreign_key statements are collected and resolved
// once every table's columns are known, and everything unrecognized falls
// through untouched. A block holding a construct with an `end` of its own — the
// dumper writes none — closes the scan early, which loses columns rather than
// inventing any.
func parseSchemaRB(src string) map[string]*parsedTable {
	tables := map[string]*rbTable{}
	table := func(name string) *rbTable {
		if tables[name] == nil {
			tables[name] = &rbTable{cols: map[string]bool{}, fks: map[string]bool{}}
		}
		return tables[name]
	}

	var (
		inTable    *rbTable
		blockVar   string
		foreignKey []rbForeignKey
		references []rbReference
		tableName  string
	)
	for i, raw := range strings.Split(src, "\n") {
		trimmed := strings.TrimSpace(strings.TrimRight(raw, "\r"))

		if inTable != nil {
			if trimmed == "end" {
				inTable = nil
				continue
			}
			if ref, ok := readTableMember(inTable, tableName, blockVar, trimmed); ok {
				references = append(references, ref)
			}
			continue
		}

		if m := reCreateTableRB.FindStringSubmatch(trimmed); m != nil {
			name, ok := sqlTableName(literalOf(m[1], m[2]))
			if !ok {
				continue
			}
			t := table(name)
			t.line = i + 1
			if col, ok := primaryKeyColumn(m[3]); ok {
				t.cols[col] = true
			}
			inTable, blockVar, tableName = t, m[4], name
			continue
		}

		if m := reAddForeignKey.FindStringSubmatch(trimmed); m != nil {
			from, fromOK := sqlTableName(literalOf(m[1], m[2]))
			to, toOK := sqlTableName(literalOf(m[3], m[4]))
			if !fromOK || !toOK {
				continue
			}
			foreignKey = append(foreignKey, rbForeignKey{from: from, to: to, column: optLiteral(m[5], reColumnOpt)})
		}
	}

	for _, fk := range foreignKey {
		column := fk.column
		if column == "" {
			var ok bool
			if column, ok = derivedFKColumn(tables[fk.from], fk.to); !ok {
				continue
			}
		}
		table(fk.from).fks[column+"->"+fk.to] = true
	}
	for _, ref := range references {
		to := ref.to
		if to == "" {
			if to = pluralize(ref.assoc); tables[to] == nil {
				continue
			}
		}
		table(ref.from).fks[ref.column+"->"+to] = true
	}
	return censusOf(tables)
}

// readTableMember records what one line of a create_table block declares,
// reporting a t.references whose foreign_key option needs the whole file to
// resolve. A member call on any receiver other than the block's own parameter
// is not this table speaking.
func readTableMember(t *rbTable, tableName, blockVar, trimmed string) (rbReference, bool) {
	m := reMemberCall.FindStringSubmatch(trimmed)
	if m == nil || m[1] != blockVar {
		return rbReference{}, false
	}
	member, rest := m[2], m[3]
	if rbNonColumnMembers[member] {
		return rbReference{}, false
	}
	if member == "timestamps" {
		t.cols["created_at"] = true
		t.cols["updated_at"] = true
		return rbReference{}, false
	}
	lit := reFirstLiteral.FindStringSubmatch(rest)
	if lit == nil {
		return rbReference{}, false
	}
	name := literalOf(lit[1], lit[2])
	if name == "" {
		return rbReference{}, false
	}
	if member != "references" && member != "belongs_to" {
		// Only the first literal: t.column states the type as its second, so
		// reading further would record a type as a column.
		t.cols[name] = true
		return rbReference{}, false
	}
	column := name + "_id"
	t.cols[column] = true
	if strings.Contains(rest, "polymorphic: true") {
		t.cols[name+"_type"] = true
	}
	if !reForeignKeyOn.MatchString(rest) {
		return rbReference{}, false
	}
	ref := rbReference{from: tableName, column: column, assoc: name}
	if stated := optLiteral(rest, reToTableOpt); stated != "" {
		normalized, ok := sqlTableName(stated)
		if !ok {
			return rbReference{}, false
		}
		ref.to = normalized
	}
	return ref, true
}

// primaryKeyColumn reads the primary key a create_table declares. The dumper
// names the column only where it is not the implicit `id`, so an options list
// with neither id: false nor primary_key: still declares one — the column
// pg_dump would have written out. A composite primary_key: [...] names columns
// the block always writes as members of its own, so the array form adds none.
func primaryKeyColumn(opts string) (string, bool) {
	if reIDFalse.MatchString(opts) {
		return "", false
	}
	if m := rePrimaryKeyOpt.FindStringSubmatch(opts); m != nil {
		return literalOf(m[1], m[2]), true
	}
	if strings.Contains(opts, "primary_key: [") {
		return "", false
	}
	return "id", true
}

// derivedFKColumn picks the column an add_foreign_key without an explicit
// column: names. Rails derives it as "#{to_table.singularize}_id" through
// ActiveSupport's full inflector, which this package does not have — so the
// column is chosen from the ones the table declares rather than invented: the
// single "<stem>_id" column the to_table is a plural of. None or several is an
// ambiguity and yields no constraint, the silence a composite key gets on the
// pg_dump side.
func derivedFKColumn(t *rbTable, toTable string) (string, bool) {
	if t == nil {
		return "", false
	}
	found := ""
	for col := range t.cols {
		stem, ok := strings.CutSuffix(col, "_id")
		if !ok || stem == "" || !isPluralOf(toTable, stem) {
			continue
		}
		if found != "" {
			return "", false
		}
		found = col
	}
	return found, found != ""
}

// irregularPlurals holds the English plurals no ending rule reaches. It can
// only ever admit a column the table already declares, so an entry missing from
// it costs a constraint and none of them can invent one.
var irregularPlurals = map[string]string{
	"analysis":  "analyses",
	"child":     "children",
	"criterion": "criteria",
	"datum":     "data",
	"index":     "indices",
	"man":       "men",
	"medium":    "media",
	"mouse":     "mice",
	"person":    "people",
	"woman":     "women",
}

// isPluralOf reports whether plural is an English plural of singular. It is a
// predicate over the pair rather than an inflector: it never produces a name,
// it only decides whether a declared column could be the one Rails derived.
func isPluralOf(plural, singular string) bool {
	if plural == singular || plural == singular+"s" || plural == singular+"es" {
		return true
	}
	if irregularPlurals[singular] == plural {
		return true
	}
	if stem, ok := strings.CutSuffix(singular, "y"); ok && plural == stem+"ies" {
		return true
	}
	if stem, ok := strings.CutSuffix(singular, "fe"); ok && plural == stem+"ves" {
		return true
	}
	if stem, ok := strings.CutSuffix(singular, "f"); ok && plural == stem+"ves" {
		return true
	}
	return false
}

// censusOf turns the accumulated sets into the sorted census the storage facts
// carry, so both dump formats reach the shared fold in one shape.
func censusOf(tables map[string]*rbTable) map[string]*parsedTable {
	out := make(map[string]*parsedTable, len(tables))
	for name, t := range tables {
		out[name] = &parsedTable{line: t.line, columns: sortedKeys(t.cols), fks: sortedKeys(t.fks)}
	}
	return out
}

// literalOf reads whichever of the two literal forms matched.
func literalOf(quoted, symbol string) string {
	if quoted != "" {
		return quoted
	}
	return symbol
}

// optLiteral reads one named option's literal out of an options list, empty
// when the option is absent or its value is not a literal.
func optLiteral(opts string, re *regexp.Regexp) string {
	m := re.FindStringSubmatch(opts)
	if m == nil {
		return ""
	}
	return literalOf(m[1], m[2])
}
