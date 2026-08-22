package providers

import (
	"context"
	"fmt"
	"sort"

	"github.com/enola-labs/enola/internal/facts"
	"github.com/enola-labs/enola/internal/providers/rubydex"
)

// builtIns are the providers the binary carries itself, keyed by the name a
// `providers:` entry with no command uses. Each runs in-process and answers
// with facts that pass the same validation as JSONL from an external tool,
// a census, and the version of the engine it read through.
var builtIns = map[string]func(ctx context.Context, repoPath string) ([]facts.Fact, string, facts.ProviderCensus, string){
	"rubydex": runRubydex,
}

// BuiltInNames lists the providers a config may name without a command.
func BuiltInNames() []string {
	names := make([]string, 0, len(builtIns))
	for name := range builtIns {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func runBuiltIn(ctx context.Context, p Provider, repoPath string) ([]facts.Fact, facts.ProviderRecord) {
	record := facts.ProviderRecord{Name: p.Name}
	skip := func(format string, args ...any) ([]facts.Fact, facts.ProviderRecord) {
		record.Skipped = true
		record.Reason = fmt.Sprintf(format, args...)
		return nil, record
	}
	run, ok := builtIns[p.Name]
	if !ok {
		return skip("no command and no built-in provider named %q (built-ins: %v)", p.Name, BuiltInNames())
	}
	accepted, version, census, refusal := run(ctx, repoPath)
	if refusal != "" {
		return skip("%s", refusal)
	}
	record.Version = version
	if p.ExpectedVersion != "" && version != p.ExpectedVersion {
		return skip("version mismatch: this enola carries %s, expected %s", version, p.ExpectedVersion)
	}
	for i, f := range accepted {
		if err := validateFact(f); err != nil {
			return skip("invalid output: fact %d: %v", i, err)
		}
	}
	record.Census = &census
	for i := range accepted {
		accepted[i].Props[PropProvider] = p.Name
		accepted[i].Props[PropProviderVersion] = version
	}
	sort.Slice(accepted, func(i, j int) bool { return factOrder(accepted[i]) < factOrder(accepted[j]) })
	return accepted, record
}

func runRubydex(ctx context.Context, repoPath string) ([]facts.Fact, string, facts.ProviderCensus, string) {
	path, installed := rubydex.Installed()
	if !installed {
		return nil, "", facts.ProviderCensus{}, fmt.Sprintf("the Rubydex library is not installed at %s; run `%s`", path, rubydex.FetchHint)
	}
	lib, err := rubydex.Open(path)
	if err != nil {
		return nil, "", facts.ProviderCensus{}, err.Error()
	}
	result := rubydex.Collect(ctx, lib, repoPath)
	if result.Refusal != "" {
		return nil, "", facts.ProviderCensus{}, result.Refusal
	}
	return result.Facts, rubydex.Version, result.Census, ""
}
