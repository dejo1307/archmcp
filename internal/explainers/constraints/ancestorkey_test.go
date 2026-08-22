package constraints

import (
	"context"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

func rubyClass(name, file, superclass string) facts.Fact {
	props := map[string]any{"symbol_kind": "class", "language": "ruby"}
	var rels []facts.Relation
	if superclass != "" {
		props["superclass"] = superclass
		rels = []facts.Relation{{Kind: facts.RelImplements, Target: superclass}}
	}
	return facts.Fact{Kind: facts.KindSymbol, Name: name, File: file, Repo: "app", Props: props, Relations: rels}
}

func resolvedAncestor(class, file, ancestor string, distance int) facts.Fact {
	return facts.Fact{Kind: facts.KindDependency, Name: "rubydex-ancestor: " + class + " -> " + ancestor, File: file, Repo: "app",
		Props:     map[string]any{"resolution_level": "resolved", "ancestor_distance": distance, "provider": "rubydex"},
		Relations: []facts.Relation{{Kind: facts.RelImplements, Target: ancestor}}}
}

func ancestorComponentIntent(name, ancestor string) facts.Fact {
	return predicateComponentIntent(name, nil, map[string]any{"ancestor": ancestor})
}

// `superclass:` reads one level of source text; `ancestor:` reads the chain a
// provider resolved. A component over `ApplicationRecord` therefore holds the
// grandchild that spelled its parent as `Base` inside a module, and leaves
// alone the class whose chain never reaches it.
func TestAncestorKey_SelectsTransitivelyThroughResolvedAncestry(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		ancestorComponentIntent("records", "ApplicationRecord"),
		componentIntent("jobs", "app/jobs/**"),
		ruleIntent("jobs-do-not-touch-records", "jobs", "records", "calls", "a job reaches records through a service"),
		rubyClass("ApplicationRecord", "app/models/application_record.rb", "ActiveRecord::Base"),
		rubyClass("Billing::Base", "app/models/billing/base.rb", "ApplicationRecord"),
		rubyClass("Billing::Invoice", "app/models/billing/invoice.rb", "Base"),
		rubyClass("Mailer", "app/mailers/mailer.rb", "ApplicationMailer"),
		resolvedAncestor("Billing::Base", "app/models/billing/base.rb", "ApplicationRecord", 1),
		resolvedAncestor("Billing::Invoice", "app/models/billing/invoice.rb", "Billing::Base", 1),
		resolvedAncestor("Billing::Invoice", "app/models/billing/invoice.rb", "ApplicationRecord", 2),
		resolvedAncestor("Mailer", "app/mailers/mailer.rb", "ApplicationMailer", 1),
		facts.Fact{Kind: facts.KindSymbol, Name: "Reminder#perform", File: "app/jobs/reminder.rb", Repo: "app",
			Props:     map[string]any{"symbol_kind": "method", "language": "ruby"},
			Relations: []facts.Relation{{Kind: facts.RelCalls, Target: "Billing::Invoice"}, {Kind: facts.RelCalls, Target: "Mailer"}}},
	)
	counts := MemberCounts(store)
	var records int
	for _, c := range counts {
		if c.Component == "records" {
			records = c.Members
		}
	}
	if records != 2 {
		t.Fatalf("records should hold what descends from ApplicationRecord (Billing::Base and the grandchild), not the root itself, got %d: %+v", records, counts)
	}
	got, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	var titles []string
	for _, i := range got {
		if strings.Contains(i.Title, "jobs-do-not-touch-records") {
			titles = append(titles, i.Title)
		}
	}
	if len(titles) != 1 || !strings.Contains(titles[0], "Billing::Invoice") {
		t.Fatalf("the grandchild spelled as Base is a member and the mailer is not, got %v", titles)
	}
	for _, u := range UnevaluableSelectors(store) {
		if u.Component == "records" {
			t.Fatalf("resolved ancestry is present, so the selector evaluates: %+v", u)
		}
	}
}

// Without a resolving provider the chain the selector walks does not exist in
// the snapshot. The component is unevaluable with a named cause, every rule
// naming it stays silent, and nothing reads that silence as compliance.
func TestAncestorKey_RefusedWithoutAResolvingProvider(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		ancestorComponentIntent("records", "ApplicationRecord"),
		componentIntent("jobs", "app/jobs/**"),
		ruleIntent("jobs-do-not-touch-records", "jobs", "records", "calls", "a job reaches records through a service"),
		rubyClass("ApplicationRecord", "app/models/application_record.rb", "ActiveRecord::Base"),
		rubyClass("Billing::Base", "app/models/billing/base.rb", "ApplicationRecord"),
		facts.Fact{Kind: facts.KindSymbol, Name: "Reminder#perform", File: "app/jobs/reminder.rb", Repo: "app",
			Props:     map[string]any{"symbol_kind": "method", "language": "ruby"},
			Relations: []facts.Relation{{Kind: facts.RelCalls, Target: "Billing::Base"}}},
	)
	unevaluable := UnevaluableSelectors(store)
	if len(unevaluable) != 1 || unevaluable[0].Component != "records" || unevaluable[0].Cause != CauseNoResolvedAncestry {
		t.Fatalf("want the ancestor selector refused for want of resolved ancestry, got %+v", unevaluable)
	}
	if !strings.Contains(unevaluable[0].Problem(), "rubydex") {
		t.Fatalf("the refusal names the remedy: %s", unevaluable[0].Problem())
	}
	got, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range got {
		if strings.Contains(i.Title, "jobs-do-not-touch-records") {
			t.Fatalf("a rule over an unevaluable selector emits no verdict, got %q", i.Title)
		}
	}
	var refused bool
	for _, i := range got {
		if strings.Contains(i.Title, "selects by ancestor ApplicationRecord without resolved ancestry") {
			refused = true
		}
	}
	if !refused {
		t.Fatal("the refusal must surface as a finding so silence cannot be read as compliance")
	}
}
