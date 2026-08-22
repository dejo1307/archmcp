// Package providers runs configured external fact providers at snapshot time —
// the seam through which a tool enola does not ship can contribute measured
// facts to the graph without becoming an extractor.
//
// A provider is an executable named in the engine config's `providers:` block.
// The seam runs it twice: once with --version to learn what build it is
// talking to, once with the repository path to collect facts, printed as JSONL
// on stdout in the store's own fact schema. Everything about the exchange is
// fail-closed: output is validated strictly (an unknown kind, an unknown
// relation, an unknown field, a missing resolution_level — any invalid line
// rejects the provider's WHOLE output, never just the line), and a provider
// fact may not share a name+kind identity with an extractor fact — colliding
// facts are skipped with a logged count, never overwritten, because the
// extractor's account of a fact is the one the rest of the graph was built
// against. A configured provider whose command is missing degrades to a named
// skip in the census rather than an error: the machine not having a tool
// installed must not fail the snapshot, it must be visible in the receipt.
//
// Determinism: providers run concurrently and are merged in name order, each
// provider's accepted facts are sorted before merging, and every accepted fact
// is stamped with the provider's name and reported version — so two runs over
// the same tree with the same providers produce byte-identical fact sets, and a
// fact can always answer who put it in the graph.
package providers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os/exec"
	"sort"
	"strings"
	"sync"

	"github.com/enola-labs/enola/internal/facts"
)

// Provider is one configured fact source: the census name its facts are
// stamped with, the command (argv) the seam runs, and the version the
// operator expects the installed build to report. An entry with no command
// names a provider the binary carries itself (see builtin.go), run
// in-process under the same validation and census. A reported version that
// disagrees with the expected one is a skip, not a merge — facts from a build
// the config did not pin would drift the graph for reasons no code change made.
type Provider struct {
	Name            string   `yaml:"name"`
	Command         []string `yaml:"command"`
	ExpectedVersion string   `yaml:"expected_version"`
}

// Reserved prop keys on provider facts. The provenance pair is stamped by the
// seam — a provider claiming them itself is invalid output — and the
// resolution level is the provider's own honesty declaration: how it resolved
// what it emitted, required on every fact so a consumer can weigh a
// constant-receiver call edge differently from a name-only one.
const (
	PropProvider          = "provider"
	PropProviderVersion   = "provider_version"
	PropResolutionLevel   = "resolution_level"
	PropObservedVia       = "observed_via"
	PropRuntimeObserved   = "runtime_observed"
	PropDeclaredIn        = "declared_in"
	PropTyped             = "typed"
	PropDeclaredSignature = "declared_signature"
)

const (
	LevelConstantReceiver = "constant-receiver"
	LevelLexicalSelf      = "lexical-self"
	LevelNameOnly         = "name-only"
	LevelLiteralDeclared  = "literal-declared"
	LevelMarkupDeclared   = "markup-declared"
	// LevelToolReported is a lint provider's honesty declaration: the fact is
	// what another tool reported, file and line and rule, and the provider
	// resolved nothing about it.
	LevelToolReported      = "tool-reported"
	LevelConventionDerived = "convention-derived"
	LevelRuntimeObserved   = "runtime-observed"
	LevelDeclared          = "declared"
	// LevelResolved states that the provider resolved the name through the
	// language's own lookup rules (nesting, inheritance, the locked gems) to
	// one declaration. It is neither a receiver typing nor a signature-file
	// claim nor a path convention, which is why it is its own word.
	LevelResolved = "resolved"
)

const CensusPrefix = "enola-provider-census: "

var allowedResolutionLevels = map[string]bool{
	LevelConstantReceiver:  true,
	LevelLexicalSelf:       true,
	LevelNameOnly:          true,
	LevelLiteralDeclared:   true,
	LevelMarkupDeclared:    true,
	LevelConventionDerived: true,
	LevelRuntimeObserved:   true,
	LevelDeclared:          true,
	LevelToolReported:      true,
	LevelResolved:          true,
}

// allowedFactKinds is the closed set a provider may emit: the measured kinds.
// The engine-owned kinds (intent, service, extraction) are deliberately
// absent — declarations and cross-repo synthesis have exactly one producer
// each, and a provider minting them would forge provenance.
var allowedFactKinds = map[string]bool{
	facts.KindModule:      true,
	facts.KindSymbol:      true,
	facts.KindRoute:       true,
	facts.KindStorage:     true,
	facts.KindDependency:  true,
	facts.KindAssociation: true,
	facts.KindTestRef:     true,
	facts.KindFileRef:     true,
	facts.KindLint:        true,
}

// allowedRelationKinds is the closed relation vocabulary, mirroring the
// constants in internal/facts — an edge kind nothing traverses is an edge
// nothing can act on, so it is rejected at the seam instead of aging silently.
var allowedRelationKinds = map[string]bool{
	facts.RelDeclares:     true,
	facts.RelImports:      true,
	facts.RelCalls:        true,
	facts.RelImplements:   true,
	facts.RelDependsOn:    true,
	facts.RelInstantiates: true,
	facts.RelInjects:      true,
	facts.RelHasMethod:    true,
	facts.RelHandledBy:    true,
}

// Validate checks the configured provider list's shape at config load, with
// the same contract every other vocabulary gets: named errors, nothing silent.
func Validate(providers []Provider) error {
	seen := map[string]bool{}
	for i, p := range providers {
		if strings.TrimSpace(p.Name) == "" {
			return fmt.Errorf("providers[%d]: missing name", i)
		}
		if seen[p.Name] {
			return fmt.Errorf("providers[%d]: name %q is declared twice", i, p.Name)
		}
		seen[p.Name] = true
		if len(p.Command) == 0 {
			if _, builtIn := builtIns[p.Name]; !builtIn {
				return fmt.Errorf("providers[%d] (%s): missing command, and no built-in provider has that name (built-ins: %s)", i, p.Name, strings.Join(BuiltInNames(), ", "))
			}
			continue
		}
		if strings.TrimSpace(p.Command[0]) == "" {
			return fmt.Errorf("providers[%d] (%s): missing command", i, p.Name)
		}
	}
	return nil
}

// Run executes every configured provider against repoPath and returns the
// merged, stamped, sorted facts plus one census record per provider — the
// receipt's account of who contributed what, including the providers that
// contributed nothing and why. taken reports whether an extractor already owns
// a kind+name identity; colliding provider facts are skipped, never merged.
// ignored reports whether a repo-relative file is excluded by the repository's
// ignore globs, and a fact about such a file is dropped: a provider walks the
// tree itself, so it cannot know what the configuration excludes, and a
// vendored dependency the extractors never read must not enter the graph
// through the seam instead. Run never fails the snapshot: every per-provider
// failure mode is a named skip in the census.
func Run(ctx context.Context, providers []Provider, repoPath string, taken func(kind, name string) bool, ignored func(file string) bool) ([]facts.Fact, []facts.ProviderRecord) {
	sorted := append([]Provider(nil), providers...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })

	// Each provider is an independent process, so they run concurrently; what
	// keeps the output deterministic is everything after: results are taken in
	// name order, each provider's facts are already sorted by runOne, and the
	// merge appends in that order. Wall-clock drops from the sum of the
	// providers to the slowest one, which on a monolith with prism beside a
	// lint reader is most of the difference.
	type outcome struct {
		accepted []facts.Fact
		record   facts.ProviderRecord
	}
	outcomes := make([]outcome, len(sorted))
	var wg sync.WaitGroup
	for i, p := range sorted {
		wg.Add(1)
		go func(i int, p Provider) {
			defer wg.Done()
			accepted, record := runOne(ctx, p, repoPath)
			outcomes[i] = outcome{accepted: accepted, record: record}
		}(i, p)
	}
	wg.Wait()

	var merged []facts.Fact
	records := make([]facts.ProviderRecord, 0, len(sorted))
	for i, p := range sorted {
		accepted, record := outcomes[i].accepted, outcomes[i].record
		if record.Skipped {
			log.Printf("[providers] %s skipped: %s", p.Name, record.Reason)
			records = append(records, record)
			continue
		}
		kept := accepted[:0]
		collided, excluded := 0, 0
		for _, f := range accepted {
			if ignored != nil && f.File != "" && ignored(f.File) {
				excluded++
				continue
			}
			if taken != nil && taken(f.Kind, f.Name) {
				collided++
				continue
			}
			kept = append(kept, f)
		}
		if collided > 0 {
			log.Printf("[providers] %s: skipped %d fact(s) whose name+kind identity an extractor already owns", p.Name, collided)
		}
		if excluded > 0 {
			log.Printf("[providers] %s: dropped %d fact(s) about files this repository's ignore globs exclude", p.Name, excluded)
		}
		record.ExcludedByIgnore = excluded
		record.FactCount = len(kept)
		records = append(records, record)
		merged = append(merged, kept...)
		log.Printf("[providers] %s (%s): merged %d facts", p.Name, record.Version, len(kept))
	}
	return merged, records
}

// runOne probes one provider's version, runs it, and strictly validates its
// output. Any failure is returned as a skipped census record.
func runOne(ctx context.Context, p Provider, repoPath string) ([]facts.Fact, facts.ProviderRecord) {
	record := facts.ProviderRecord{Name: p.Name}
	skip := func(format string, args ...any) ([]facts.Fact, facts.ProviderRecord) {
		record.Skipped = true
		record.Reason = fmt.Sprintf(format, args...)
		return nil, record
	}

	if len(p.Command) == 0 {
		return runBuiltIn(ctx, p, repoPath)
	}

	versionOut, err := exec.CommandContext(ctx, p.Command[0], append(p.Command[1:], "--version")...).Output()
	if err != nil {
		// Both spellings of a missing tool: exec.ErrNotFound for a bare name the
		// PATH lookup missed, fs.ErrNotExist for an absolute path that is not there.
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
			return skip("command not found: %s", p.Command[0])
		}
		return skip("version probe failed: %v", err)
	}
	version := strings.TrimSpace(string(versionOut))
	if version == "" {
		return skip("provider reported no version")
	}
	record.Version = version
	if p.ExpectedVersion != "" && version != p.ExpectedVersion {
		return skip("version mismatch: reported %s, expected %s", version, p.ExpectedVersion)
	}

	cmd := exec.CommandContext(ctx, p.Command[0], append(p.Command[1:], repoPath)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return skip("provider run failed: %v (%s)", err, strings.TrimSpace(stderr.String()))
	}

	accepted, err := parseFacts(out)
	if err != nil {
		return skip("invalid output: %v", err)
	}
	census, err := parseCensus(stderr.Bytes())
	if err != nil {
		return skip("invalid census: %v", err)
	}
	record.Census = census
	for i := range accepted {
		accepted[i].Props[PropProvider] = p.Name
		accepted[i].Props[PropProviderVersion] = version
	}
	// Sorted before merge so the graph is a function of what the provider
	// emitted, never of the order it happened to emit it in.
	sort.Slice(accepted, func(i, j int) bool { return factOrder(accepted[i]) < factOrder(accepted[j]) })
	return accepted, record
}

// parseFacts decodes a provider's JSONL strictly. The first invalid line
// rejects the whole output: a provider that emits one line the schema does not
// admit cannot be trusted about the lines it got right.
func parseFacts(out []byte) ([]facts.Fact, error) {
	var accepted []facts.Fact
	scanner := bufio.NewScanner(bytes.NewReader(out))
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		f, err := parseFactLine(line)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", lineNo, err)
		}
		accepted = append(accepted, f)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return accepted, nil
}

// parseFactLine validates one JSONL line against the fact schema: only known
// fields, a known kind, known relation kinds with targets, no engine-assigned
// or seam-stamped fields claimed by the provider, and a resolution_level on
// every fact.
func parseFactLine(line string) (facts.Fact, error) {
	var f facts.Fact
	dec := json.NewDecoder(strings.NewReader(line))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return facts.Fact{}, err
	}
	// Decode stops at the end of the first JSON value, so a line carrying two
	// objects used to keep the first and silently drop the rest — content lost
	// under a contract that promises strictness. Anything but EOF after the
	// fact object rejects the line.
	var trailing json.RawMessage
	if err := dec.Decode(&trailing); err != io.EOF {
		return facts.Fact{}, fmt.Errorf("trailing data after the fact object on the same line")
	}
	if err := validateFact(f); err != nil {
		return facts.Fact{}, err
	}
	return f, nil
}

// validateFact is the schema check every provider fact passes, external or
// built-in: a known kind, known relations with targets, a resolution level
// from the vocabulary, and nothing the engine or the seam assigns.
func validateFact(f facts.Fact) error {
	if !allowedFactKinds[f.Kind] {
		return fmt.Errorf("kind %q is not a provider-emittable fact kind (allowed: %s)", f.Kind, joinSorted(allowedFactKinds))
	}
	if f.Name == "" {
		return fmt.Errorf("fact has no name")
	}
	if f.Repo != "" {
		return fmt.Errorf("repo is engine-assigned; a provider must not set it")
	}
	for _, rel := range f.Relations {
		if !allowedRelationKinds[rel.Kind] {
			return fmt.Errorf("relation kind %q is not in the vocabulary (allowed: %s)", rel.Kind, joinSorted(allowedRelationKinds))
		}
		if rel.Target == "" {
			return fmt.Errorf("relation %q has no target", rel.Kind)
		}
	}
	level, _ := f.Props[PropResolutionLevel].(string)
	if level == "" {
		return fmt.Errorf("fact %q carries no %s prop — a provider must say how it resolved what it emitted", f.Name, PropResolutionLevel)
	}
	if !allowedResolutionLevels[level] {
		return fmt.Errorf("fact %q carries %s %q, which is not in the vocabulary (allowed: %s)", f.Name, PropResolutionLevel, level, joinSorted(allowedResolutionLevels))
	}
	if via, _ := f.Props[PropObservedVia].(string); level == LevelRuntimeObserved && via == "" {
		return fmt.Errorf("fact %q is %s but carries no %s prop — a runtime fact must name its observation channel", f.Name, LevelRuntimeObserved, PropObservedVia)
	}
	if in, _ := f.Props[PropDeclaredIn].(string); level == LevelDeclared && in == "" {
		return fmt.Errorf("fact %q is %s but carries no %s prop — a declared fact must name the signature file that claims it", f.Name, LevelDeclared, PropDeclaredIn)
	}
	for _, reserved := range []string{PropProvider, PropProviderVersion} {
		if _, claimed := f.Props[reserved]; claimed {
			return fmt.Errorf("prop %q is stamped by the seam; a provider must not set it", reserved)
		}
	}
	return nil
}

func parseCensus(stderr []byte) (*facts.ProviderCensus, error) {
	var census *facts.ProviderCensus
	scanner := bufio.NewScanner(bytes.NewReader(stderr))
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, CensusPrefix) {
			continue
		}
		if census != nil {
			return nil, fmt.Errorf("more than one %q line — a provider states its accounting exactly once", strings.TrimSpace(CensusPrefix))
		}
		var c facts.ProviderCensus
		dec := json.NewDecoder(strings.NewReader(strings.TrimPrefix(line, CensusPrefix)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&c); err != nil {
			return nil, err
		}
		census = &c
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return census, nil
}

// factOrder totally orders one provider's facts for the pre-merge sort.
func factOrder(f facts.Fact) string {
	return fmt.Sprintf("%s\x00%s\x00%s\x00%09d", f.Kind, f.Name, f.File, f.Line)
}

func joinSorted(set map[string]bool) string {
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}
