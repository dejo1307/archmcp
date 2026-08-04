package rubyextractor

import "testing"

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
