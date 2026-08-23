package engine_test

// The unseen census is the engine's one account of what a run could not see,
// assembled from counts it already holds. The fixture plants one of each
// cause the walk and the store can produce on their own: an ignored file, a
// dependency on a gem outside the graph, a class in a file that dispatches
// dynamically.

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/enola-labs/enola/internal/config"
	"github.com/enola-labs/enola/internal/engine"
	"github.com/enola-labs/enola/internal/extractors/rubyextractor"
	"github.com/enola-labs/enola/internal/facts"
)

func unseenFixture(t *testing.T) *facts.UnseenCensus {
	t.Helper()
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "Gemfile"), "source 'https://rubygems.org'\ngem 'nokogiri'\n")
	writeFile(t, filepath.Join(repo, "app", "models", "order.rb"), "require 'nokogiri'\n\nclass Order\n  def parse(doc)\n    Nokogiri::HTML(doc)\n  end\n\n  def call_it(name)\n    send(\"handle_#{name}\")\n  end\nend\n")
	writeFile(t, filepath.Join(repo, "app", "models", "invoice.rb"), "class Invoice\n  def total\n    1\n  end\nend\n")
	writeFile(t, filepath.Join(repo, "trace.log"), "noise\n")

	cfg := config.Default()
	cfg.Ignore = append(cfg.Ignore, "*.log")
	eng, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	eng.RegisterExtractor(rubyextractor.New())
	snap, err := eng.GenerateSnapshot(context.Background(), repo, false)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Meta.Unseen == nil {
		t.Fatal("snapshot carries no unseen census")
	}
	return snap.Meta.Unseen
}

func TestUnseenCensus_CountsWhatTheRunCouldNotSee(t *testing.T) {
	u := unseenFixture(t)
	if u.FilesExcludedByIgnore != 1 {
		t.Fatalf("ignored files: %d", u.FilesExcludedByIgnore)
	}
	if u.OutsideGraph[facts.RelImports] == 0 && u.OutsideGraph[facts.RelDependsOn] == 0 {
		t.Fatalf("the gem dependency must count as outside the graph: %v", u.OutsideGraph)
	}
	if u.DynamicFeatureClasses != 1 {
		t.Fatalf("one class dispatches dynamically, counted %d", u.DynamicFeatureClasses)
	}
	if u.DeadExemptions != 0 || len(u.ProviderSkips) != 0 {
		t.Fatalf("nothing else was planted: %+v", *u)
	}
}

func TestUnseenCensus_IsDeterministic(t *testing.T) {
	a, b := unseenFixture(t), unseenFixture(t)
	if a.FilesExcludedByIgnore != b.FilesExcludedByIgnore || a.DynamicFeatureClasses != b.DynamicFeatureClasses || len(a.OutsideGraph) != len(b.OutsideGraph) {
		t.Fatalf("two runs disagree: %+v vs %+v", *a, *b)
	}
}
