package command

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/enola-labs/enola/internal/explainers/constraints"
	"github.com/enola-labs/enola/internal/facts"
	"github.com/enola-labs/enola/internal/intent"
	"github.com/enola-labs/enola/pkg/bootstrap"
	"github.com/enola-labs/enola/pkg/check"
)

// ConstraintsExplain is `enola constraints explain <path> [repo_path]`: why
// a file belongs where it belongs. It names every component whose selector
// admits a fact in the file, the selector that did it, and the edges the
// file's facts make, read off the same membership the evaluator verdicts on.
func (r *Runner) ConstraintsExplain(args []string) {
	fs := flag.NewFlagSet("constraints explain", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	asJSON := fs.Bool("json", false, "print the explanation as JSON")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, "Usage: "+r.name()+" constraints explain [--json] <path> [repo_path]\n\n"+
			"Names the components a file's facts belong to, the selector that admitted each,\n"+
			"and the edges the file makes, from the current snapshot.\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil || fs.NArg() < 1 {
		fs.Usage()
		os.Exit(check.StatusUsageError.ExitCode())
	}
	target := fs.Arg(0)
	repoPath := "."
	if fs.NArg() > 1 {
		repoPath = fs.Arg(1)
	}
	repoPath, _ = filepath.Abs(repoPath)
	rel := target
	if filepath.IsAbs(target) {
		if p, err := filepath.Rel(repoPath, target); err == nil {
			rel = p
		}
	}
	tgt := r.resolveTarget(repoPath)
	tgt.engine.SetPersistCache(false)
	snap, err := bootstrap.LoadSnapshotDir(tgt.engine.OutputDir(repoPath))
	if err != nil {
		fmt.Fprintf(os.Stderr, "constraints explain: no snapshot for %s; generate one first\n", repoPath)
		os.Exit(check.StatusUsageError.ExitCode())
	}
	decl, err := intent.LoadRepoFile(repoPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "constraints explain: %v\n", err)
		os.Exit(check.StatusUsageError.ExitCode())
	}
	store := facts.NewStore()
	for _, f := range snap.Facts {
		if f.Kind != facts.KindIntent {
			store.Add(f)
		}
	}
	label := filepath.Base(repoPath)
	if decl != nil {
		for _, f := range intent.CompileFacts(decl) {
			if f.Repo == "" {
				f.Repo = label
			}
			store.Add(f)
		}
	}
	explanation := constraints.ExplainFile(store, rel)
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(explanation)
		return
	}
	fmt.Print(RenderFileExplanation(explanation))
}

// RenderFileExplanation is the text form, one fact per line, one component
// per line with its selector, one edge per line with its line number.
func RenderFileExplanation(e constraints.FileExplanation) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", e.File)
	if len(e.Facts) == 0 {
		b.WriteString("  no measured fact lives in this file\n")
		return b.String()
	}
	b.WriteString("  measured facts:\n")
	for _, f := range e.Facts {
		fmt.Fprintf(&b, "    %s\n", f)
	}
	if len(e.Memberships) == 0 {
		b.WriteString("  components: none; no declared selector admits a fact in this file\n")
	} else {
		b.WriteString("  components:\n")
		for _, m := range e.Memberships {
			fmt.Fprintf(&b, "    %s: %s (declared in %s)\n", m.Component, m.Selector, m.Source)
			for _, member := range m.Members {
				fmt.Fprintf(&b, "      %s\n", member)
			}
		}
	}
	if len(e.Outgoing) > 0 {
		b.WriteString("  outgoing edges:\n")
		for _, edge := range e.Outgoing {
			fmt.Fprintf(&b, "    %4d  %s --%s--> %s\n", edge.Line, edge.From, edge.Kind, edge.Target)
		}
	}
	return b.String()
}
