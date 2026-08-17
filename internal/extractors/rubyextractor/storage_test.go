package rubyextractor

import (
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

func TestSequelModelBase(t *testing.T) {
	cases := []struct {
		super, table string
		ok           bool
	}{
		{"Sequel::Model", "", true},
		{"Sequel::Model(:customers)", "customers", true},
		{"Sequel::Model(db[:x])", "", true},
		{"ApplicationRecord", "", false},
	}
	for _, c := range cases {
		table, ok := sequelModelBase(c.super)
		if table != c.table || ok != c.ok {
			t.Errorf("sequelModelBase(%q) = %q,%v want %q,%v", c.super, table, ok, c.table, c.ok)
		}
	}
}

// TestExplicitTableNameCorrectsModelFact: `self.table_name = "…"` must become
// the model's own storage truth. The defect this pins: the declared name was
// extracted as a second, standalone table fact while the model's fact kept the
// convention-derived name — a table that does not exist — so every join from
// model to physical table silently resolved to the wrong one, with the
// correction sitting beside it looking like additional information.
func TestExplicitTableNameCorrectsModelFact(t *testing.T) {
	src := []byte("class LegacyThing < ApplicationRecord\n  self.table_name = \"old_things\"\nend\n")
	ff := extractFileAST(src, "app/models/legacy_thing.rb", true, true)
	var model *facts.Fact
	for i := range ff {
		if ff[i].Kind != facts.KindStorage {
			continue
		}
		if ff[i].Name == "LegacyThing" {
			model = &ff[i]
			continue
		}
		t.Errorf("unexpected standalone storage fact %q — the declared table corrects the model's fact, it never becomes a fact of its own", ff[i].Name)
	}
	if model == nil {
		t.Fatal("model emitted no storage fact")
	}
	if model.Props["table"] != "old_things" {
		t.Fatalf("model table = %v, want the declared old_things, not the derived legacy_things", model.Props["table"])
	}
	if model.Props["table_source"] != "declared" {
		t.Fatalf("table_source = %v, want declared — a stated name is not a convention holding", model.Props["table_source"])
	}
}

// TestTableNamePrefix_CorrectsDerivedNotDeclared: a namespace declaring
// `def self.table_name_prefix` moves every table its models DERIVE, and moves
// none that a model STATES. The defect this pins: `Engagement::Survey` was
// reported on `surveys` — a table that belongs to a different model — so the
// prefixed relation it actually reads had no model, and the one it was given
// had two.
func TestTableNamePrefix_CorrectsDerivedNotDeclared(t *testing.T) {
	namespace := []byte("module Engagement\n  def self.table_name_prefix\n    \"engagement_\"\n  end\nend\n")
	derived := []byte("module Engagement\n  class Survey < ApplicationRecord\n  end\nend\n")
	declared := []byte("module Engagement\n  class Legacy < ApplicationRecord\n    self.table_name = \"old_surveys\"\n  end\nend\n")

	all := extractFileAST(namespace, "app/models/engagement.rb", true, true)
	all = append(all, extractFileAST(derived, "app/models/engagement/survey.rb", true, true)...)
	all = append(all, extractFileAST(declared, "app/models/engagement/legacy.rb", true, true)...)

	var module *facts.Fact
	for i := range all {
		if all[i].Kind == facts.KindSymbol && all[i].Name == "Engagement" && all[i].File == "app/models/engagement.rb" {
			module = &all[i]
		}
	}
	if module == nil {
		t.Fatal("the namespace emitted no symbol fact")
	}
	if module.Props["table_name_prefix"] != "engagement_" {
		t.Fatalf("module table_name_prefix = %v, want the declared literal", module.Props["table_name_prefix"])
	}

	applyTableNamePrefixes(all)
	tables := map[string]string{}
	sources := map[string]string{}
	for _, f := range all {
		if f.Kind != facts.KindStorage {
			continue
		}
		tables[f.Name], _ = f.Props["table"].(string)
		sources[f.Name], _ = f.Props["table_source"].(string)
	}
	if tables["Engagement::Survey"] != "engagement_surveys" {
		t.Errorf("derived table = %q, want engagement_surveys", tables["Engagement::Survey"])
	}
	if sources["Engagement::Survey"] != "derived" {
		t.Errorf("a prefixed name is still derived from the class, got table_source %q", sources["Engagement::Survey"])
	}
	if tables["Engagement::Legacy"] != "old_surveys" {
		t.Errorf("declared table = %q, want the stated old_surveys — Rails does not prefix a declared name", tables["Engagement::Legacy"])
	}
}

// TestTableNamePrefix_InnermostNamespaceWins: when both an outer and an inner
// namespace declare a prefix, the inner one decides — Rails takes the first of
// the model's module parents that declares one, and gitlabhq's
// Packages::Debian::Publication is packages_debian_publications, never
// packages_publications.
func TestTableNamePrefix_InnermostNamespaceWins(t *testing.T) {
	outer := []byte("module Packages\n  def self.table_name_prefix\n    \"packages_\"\n  end\nend\n")
	inner := []byte("module Packages\n  module Debian\n    def self.table_name_prefix\n      \"packages_debian_\"\n    end\n  end\nend\n")
	nested := []byte("module Packages\n  module Debian\n    class Publication < ApplicationRecord\n    end\n  end\nend\n")
	shallow := []byte("module Packages\n  class BuildInfo < ApplicationRecord\n  end\nend\n")

	all := extractFileAST(outer, "app/models/packages.rb", true, true)
	all = append(all, extractFileAST(inner, "app/models/packages/debian.rb", true, true)...)
	all = append(all, extractFileAST(nested, "app/models/packages/debian/publication.rb", true, true)...)
	all = append(all, extractFileAST(shallow, "app/models/packages/build_info.rb", true, true)...)
	applyTableNamePrefixes(all)

	want := map[string]string{
		"Packages::Debian::Publication": "packages_debian_publications",
		"Packages::BuildInfo":           "packages_build_infos",
	}
	for _, f := range all {
		if f.Kind != facts.KindStorage {
			continue
		}
		if expected, ok := want[f.Name]; ok && f.Props["table"] != expected {
			t.Errorf("%s table = %v, want %s", f.Name, f.Props["table"], expected)
		}
	}
}

// TestTableNamePrefix_FailsClosed: a prefix this pass cannot read is not
// invented, and a namespace that declares none moves nothing.
func TestTableNamePrefix_FailsClosed(t *testing.T) {
	computed := []byte("module Engagement\n  def self.table_name_prefix\n    \"#{name.underscore}_\"\n  end\nend\n")
	if ff := extractFileAST(computed, "app/models/engagement.rb", true, true); factProp(ff, "Engagement", "table_name_prefix") != nil {
		t.Error("an interpolated prefix is not a literal and must not be recorded")
	}

	onAClass := []byte("class Engagement < ApplicationRecord\n  def self.table_name_prefix\n    \"engagement_\"\n  end\nend\n")
	if ff := extractFileAST(onAClass, "app/models/engagement.rb", true, true); factProp(ff, "Engagement", "table_name_prefix") != nil {
		t.Error("Rails reads the prefix off the namespace a model is nested in, never off a class")
	}

	plain := extractFileAST([]byte("module Reporting\nend\n"), "app/models/reporting.rb", true, true)
	plain = append(plain, extractFileAST([]byte("module Reporting\n  class Run < ApplicationRecord\n  end\nend\n"), "app/models/reporting/run.rb", true, true)...)
	applyTableNamePrefixes(plain)
	for _, f := range plain {
		if f.Kind == facts.KindStorage && f.Name == "Reporting::Run" && f.Props["table"] != "runs" {
			t.Errorf("a namespace declaring no prefix must leave its models alone, got %v", f.Props["table"])
		}
	}
}

// factProp returns one prop of the named symbol fact, or nil when the fact or
// the prop is absent.
func factProp(ff []facts.Fact, name, prop string) any {
	for _, f := range ff {
		if f.Kind == facts.KindSymbol && f.Name == name {
			return f.Props[prop]
		}
	}
	return nil
}

func TestSequelModelDatasetForm_ThroughAST(t *testing.T) {
	src := []byte("class CustomerRecord < Sequel::Model(:customers)\n  def display_name\n    name.upcase\n  end\nend\n")
	ff := extractFileAST(src, "app/models/customer_record.rb", true, true)
	var storage *facts.Fact
	for i := range ff {
		if ff[i].Kind == facts.KindStorage && ff[i].Name == "CustomerRecord" {
			storage = &ff[i]
		}
		if ff[i].Kind == facts.KindSymbol && ff[i].Name == "CustomerRecord" {
			if ff[i].Props["superclass"] != "Sequel::Model" {
				t.Fatalf("superclass prop = %v, want the base name without call arguments", ff[i].Props["superclass"])
			}
		}
	}
	if storage == nil {
		t.Fatal("the dataset-form Sequel model emitted no storage fact — the call-form superclass was dropped (the miss the ruby_sample golden pinned until v154)")
	}
	if storage.Props["table"] != "customers" || storage.Props["framework"] != "sequel" {
		t.Fatalf("storage fact = %+v, want table=customers framework=sequel", storage.Props)
	}
}
