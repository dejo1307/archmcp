package llmcontext

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/dejo1307/archmcp/internal/facts"
)

// LLMContextRenderer produces a compact markdown summary optimized for LLM consumption.
type LLMContextRenderer struct {
	maxTokens int
}

// New creates a new LLMContextRenderer with the given token budget.
func New(maxTokens int) *LLMContextRenderer {
	if maxTokens <= 0 {
		maxTokens = 4000
	}
	return &LLMContextRenderer{maxTokens: maxTokens}
}

func (r *LLMContextRenderer) Name() string {
	return "llm_context"
}

// Render produces the llm_context.md artifact.
func (r *LLMContextRenderer) Render(ctx context.Context, snapshot *facts.Snapshot) ([]facts.Artifact, error) {
	var sb strings.Builder

	sb.WriteString("# Architecture Snapshot\n\n")

	// 1. Repository Map
	r.writeRepoMap(&sb, snapshot)

	// 2. Architecture Pattern
	r.writeArchPattern(&sb, snapshot)

	// 3. Entry Points
	r.writeEntryPoints(&sb, snapshot)

	// 4. Dependency Rules
	r.writeDependencyRules(&sb, snapshot)

	// 5. Critical Modules
	r.writeCriticalModules(&sb, snapshot)

	// 6. Routes (if any)
	r.writeRoutes(&sb, snapshot)

	// 7. Risk Zones
	r.writeRiskZones(&sb, snapshot)

	// 8. How to Add a Feature
	r.writeFeatureGuide(&sb, snapshot)

	// 9. Meta
	r.writeMeta(&sb, snapshot)

	content := sb.String()

	// Enforce token budget (rough estimate: 1 token ~= 4 chars)
	maxChars := r.maxTokens * 4
	if len(content) > maxChars {
		content = content[:maxChars-100] + "\n\n---\n*[Truncated to fit token budget]*\n"
	}

	return []facts.Artifact{
		{
			Name:    "llm_context.md",
			Content: []byte(content),
			Type:    "text/markdown",
		},
	}, nil
}

func (r *LLMContextRenderer) writeRepoMap(sb *strings.Builder, snapshot *facts.Snapshot) {
	sb.WriteString("## Repository Map\n\n")

	modules := filterByKind(snapshot.Facts, facts.KindModule)
	if len(modules) == 0 {
		sb.WriteString("_No modules detected._\n\n")
		return
	}

	// Group symbols by module
	symbolCounts := make(map[string]int)
	exportedCounts := make(map[string]int)
	for _, f := range snapshot.Facts {
		if f.Kind != facts.KindSymbol {
			continue
		}
		for _, rel := range f.Relations {
			if rel.Kind == facts.RelDeclares {
				symbolCounts[rel.Target]++
				if exported, ok := f.Props["exported"].(bool); ok && exported {
					exportedCounts[rel.Target]++
				}
			}
		}
	}

	// Sort modules by name
	sort.Slice(modules, func(i, j int) bool {
		return modules[i].Name < modules[j].Name
	})

	sb.WriteString("| Module | Language | Symbols | Exported |\n")
	sb.WriteString("|--------|----------|---------|----------|\n")
	for _, mod := range modules {
		lang := "unknown"
		if l, ok := mod.Props["language"].(string); ok {
			lang = l
		}
		sb.WriteString(fmt.Sprintf("| `%s` | %s | %d | %d |\n",
			mod.Name, lang, symbolCounts[mod.Name], exportedCounts[mod.Name]))
	}
	sb.WriteString("\n")
}

func (r *LLMContextRenderer) writeArchPattern(sb *strings.Builder, snapshot *facts.Snapshot) {
	sb.WriteString("## Architecture Pattern\n\n")

	// Find architecture insights
	for _, insight := range snapshot.Insights {
		if strings.HasPrefix(insight.Title, "Architecture pattern:") {
			sb.WriteString(fmt.Sprintf("**%s** (confidence: %.0f%%)\n\n", insight.Title, insight.Confidence*100))
			sb.WriteString(insight.Description + "\n\n")

			if len(insight.Evidence) > 0 {
				sb.WriteString("Layer mapping:\n")
				for _, ev := range insight.Evidence {
					sb.WriteString(fmt.Sprintf("- %s\n", ev.Detail))
				}
				sb.WriteString("\n")
			}
			return
		}
	}

	sb.WriteString("_No specific architecture pattern detected._\n\n")
}

func (r *LLMContextRenderer) writeEntryPoints(sb *strings.Builder, snapshot *facts.Snapshot) {
	sb.WriteString("## Entry Points\n\n")

	var entryPoints []string

	for _, f := range snapshot.Facts {
		if f.Kind != facts.KindSymbol {
			continue
		}

		symbolKind, _ := f.Props["symbol_kind"].(string)
		exported, _ := f.Props["exported"].(bool)

		// Main functions
		if strings.HasSuffix(f.Name, ".main") && symbolKind == facts.SymbolFunc {
			entryPoints = append(entryPoints, fmt.Sprintf("- **main**: `%s` (%s)", f.Name, f.File))
		}

		// HTTP handlers (common patterns)
		if exported && symbolKind == facts.SymbolFunc {
			nameLower := strings.ToLower(f.Name)
			if strings.Contains(nameLower, "handler") || strings.Contains(nameLower, "handle") ||
				strings.Contains(nameLower, "serve") {
				entryPoints = append(entryPoints, fmt.Sprintf("- **handler**: `%s` (%s)", f.Name, f.File))
			}
		}
	}

	// Routes as entry points
	routes := filterByKind(snapshot.Facts, facts.KindRoute)
	for _, route := range routes {
		method, _ := route.Props["method"].(string)
		entryPoints = append(entryPoints, fmt.Sprintf("- **route** %s `%s` (%s)", method, route.Name, route.File))
	}

	if len(entryPoints) == 0 {
		sb.WriteString("_No entry points detected._\n\n")
		return
	}

	sort.Strings(entryPoints)
	for _, ep := range entryPoints {
		sb.WriteString(ep + "\n")
	}
	sb.WriteString("\n")
}

func (r *LLMContextRenderer) writeDependencyRules(sb *strings.Builder, snapshot *facts.Snapshot) {
	sb.WriteString("## Dependency Rules\n\n")

	// Collect unique module-to-module internal dependencies
	type depEdge struct{ from, to string }
	seen := make(map[depEdge]bool)

	deps := filterByKind(snapshot.Facts, facts.KindDependency)
	modules := make(map[string]bool)
	for _, f := range snapshot.Facts {
		if f.Kind == facts.KindModule {
			modules[f.Name] = true
		}
	}

	var edges []string
	for _, dep := range deps {
		sourceModule := fileDir(dep.File)
		for _, rel := range dep.Relations {
			if rel.Kind != facts.RelImports {
				continue
			}
			if !modules[rel.Target] {
				continue
			}
			edge := depEdge{sourceModule, rel.Target}
			if !seen[edge] {
				seen[edge] = true
				edges = append(edges, fmt.Sprintf("- `%s` -> `%s`", edge.from, edge.to))
			}
		}
	}

	if len(edges) == 0 {
		sb.WriteString("_No internal dependency rules detected._\n\n")
		return
	}

	sort.Strings(edges)
	for _, e := range edges {
		sb.WriteString(e + "\n")
	}
	sb.WriteString("\n")
}

func (r *LLMContextRenderer) writeCriticalModules(sb *strings.Builder, snapshot *facts.Snapshot) {
	sb.WriteString("## Critical Modules\n\n")

	// Compute fan-in (imported by others) and fan-out (imports others)
	fanIn := make(map[string]int)
	fanOut := make(map[string]int)

	modules := make(map[string]bool)
	for _, f := range snapshot.Facts {
		if f.Kind == facts.KindModule {
			modules[f.Name] = true
		}
	}

	deps := filterByKind(snapshot.Facts, facts.KindDependency)
	for _, dep := range deps {
		sourceModule := fileDir(dep.File)
		for _, rel := range dep.Relations {
			if rel.Kind == facts.RelImports && modules[rel.Target] {
				fanOut[sourceModule]++
				fanIn[rel.Target]++
			}
		}
	}

	type modScore struct {
		Name   string
		FanIn  int
		FanOut int
		Score  int
	}

	var scored []modScore
	for mod := range modules {
		s := modScore{
			Name:   mod,
			FanIn:  fanIn[mod],
			FanOut: fanOut[mod],
			Score:  fanIn[mod] + fanOut[mod],
		}
		if s.Score > 0 {
			scored = append(scored, s)
		}
	}

	sort.Slice(scored, func(i, j int) bool {
		return scored[i].Score > scored[j].Score
	})

	// Show top 10
	limit := 10
	if len(scored) < limit {
		limit = len(scored)
	}

	if limit == 0 {
		sb.WriteString("_No cross-module dependencies detected._\n\n")
		return
	}

	sb.WriteString("| Module | Fan-In | Fan-Out | Criticality |\n")
	sb.WriteString("|--------|--------|---------|-------------|\n")
	for _, s := range scored[:limit] {
		criticality := "low"
		if s.Score >= 10 {
			criticality = "high"
		} else if s.Score >= 5 {
			criticality = "medium"
		}
		sb.WriteString(fmt.Sprintf("| `%s` | %d | %d | %s |\n", s.Name, s.FanIn, s.FanOut, criticality))
	}
	sb.WriteString("\n")
}

func (r *LLMContextRenderer) writeRoutes(sb *strings.Builder, snapshot *facts.Snapshot) {
	routes := filterByKind(snapshot.Facts, facts.KindRoute)
	if len(routes) == 0 {
		return
	}

	sb.WriteString("## Routes\n\n")
	sb.WriteString("| Method | Path | File | Type |\n")
	sb.WriteString("|--------|------|------|------|\n")

	sort.Slice(routes, func(i, j int) bool {
		return routes[i].Name < routes[j].Name
	})

	for _, route := range routes {
		method, _ := route.Props["method"].(string)
		routeType, _ := route.Props["type"].(string)
		sb.WriteString(fmt.Sprintf("| %s | `%s` | `%s` | %s |\n", method, route.Name, route.File, routeType))
	}
	sb.WriteString("\n")
}

func (r *LLMContextRenderer) writeRiskZones(sb *strings.Builder, snapshot *facts.Snapshot) {
	var risks []string

	for _, insight := range snapshot.Insights {
		if strings.Contains(insight.Title, "Cyclic dependency") ||
			strings.Contains(insight.Title, "Layer violation") {
			risks = append(risks, fmt.Sprintf("- **%s** (confidence: %.0f%%): %s",
				insight.Title, insight.Confidence*100, insight.Description))
		}
	}

	if len(risks) == 0 {
		return
	}

	sb.WriteString("## Risk Zones\n\n")
	for _, risk := range risks {
		sb.WriteString(risk + "\n")
	}
	sb.WriteString("\n")
}

func (r *LLMContextRenderer) writeFeatureGuide(sb *strings.Builder, snapshot *facts.Snapshot) {
	sb.WriteString("## How to Add a Feature\n\n")

	// Determine guide based on detected architecture
	var archPattern string
	for _, insight := range snapshot.Insights {
		if strings.HasPrefix(insight.Title, "Architecture pattern:") {
			archPattern = strings.TrimPrefix(insight.Title, "Architecture pattern: ")
			break
		}
	}

	switch archPattern {
	case "hexagonal":
		sb.WriteString("This project follows a hexagonal/clean architecture:\n\n")
		sb.WriteString("1. **Define domain types** in the domain/model layer\n")
		sb.WriteString("2. **Define a port** (interface) in the port layer for external interactions\n")
		sb.WriteString("3. **Implement the use case** in the application/service layer\n")
		sb.WriteString("4. **Implement adapters** for infrastructure (DB, API clients, etc.)\n")
		sb.WriteString("5. **Add the handler** (HTTP/gRPC) in the handler layer\n")
		sb.WriteString("6. **Wire dependencies** in the main/cmd entry point\n")

	case "nextjs":
		sb.WriteString("This project follows a Next.js architecture:\n\n")
		sb.WriteString("1. **Create the page/route** in the `app/` or `pages/` directory\n")
		sb.WriteString("2. **Build UI components** in `components/`\n")
		sb.WriteString("3. **Add hooks** for client-side logic in `hooks/`\n")
		sb.WriteString("4. **Add server-side logic** as API routes or server actions\n")
		sb.WriteString("5. **Add shared types** in `types/`\n")
		sb.WriteString("6. **Add utility functions** in `lib/` or `utils/`\n")

	case "go-standard":
		sb.WriteString("This project follows Go standard project layout:\n\n")
		sb.WriteString("1. **Add the command** in `cmd/` if it's a new binary\n")
		sb.WriteString("2. **Implement business logic** in `internal/`\n")
		sb.WriteString("3. **Add shared libraries** in `pkg/` (if intended for external use)\n")
		sb.WriteString("4. **Define API contracts** in `api/`\n")
		sb.WriteString("5. **Wire the feature** in the appropriate `cmd/` main file\n")

	default:
		sb.WriteString("General guidance:\n\n")
		sb.WriteString("1. Identify the appropriate module/package for the feature\n")
		sb.WriteString("2. Follow existing patterns in the codebase\n")
		sb.WriteString("3. Keep dependencies flowing in one direction\n")
		sb.WriteString("4. Add appropriate exports for cross-module usage\n")
		sb.WriteString("5. Wire the feature in the entry point\n")
	}

	sb.WriteString("\n")
}

func (r *LLMContextRenderer) writeMeta(sb *strings.Builder, snapshot *facts.Snapshot) {
	sb.WriteString("---\n\n")
	sb.WriteString(fmt.Sprintf("*Generated at %s in %s. %d facts, %d insights.*\n",
		snapshot.Meta.GeneratedAt, snapshot.Meta.Duration,
		snapshot.Meta.FactCount, snapshot.Meta.InsightCount))
}

func filterByKind(ff []facts.Fact, kind string) []facts.Fact {
	var result []facts.Fact
	for _, f := range ff {
		if f.Kind == kind {
			result = append(result, f)
		}
	}
	return result
}

func fileDir(file string) string {
	parts := strings.Split(file, "/")
	if len(parts) <= 1 {
		return "."
	}
	return strings.Join(parts[:len(parts)-1], "/")
}
