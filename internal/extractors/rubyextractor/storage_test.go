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
