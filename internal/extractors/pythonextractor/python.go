package pythonextractor

import (
	"context"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/enola-labs/enola/internal/facts"
	"github.com/enola-labs/enola/internal/parallel"
)

// PythonExtractor extracts architectural facts from Python source code using
// line-based regex parsing with indentation-based scope tracking.
type PythonExtractor struct{}

// New creates a new PythonExtractor.
func New() *PythonExtractor {
	return &PythonExtractor{}
}

func (e *PythonExtractor) Name() string {
	return "python"
}

// Detect returns true if the repository looks like a Python project.
// It checks root-level markers first, then walks up to 3 subdirectory levels
// to support monorepos where Python code lives in a subdirectory (e.g. python/).
func (e *PythonExtractor) Detect(repoPath string) (bool, error) {
	// Root-level markers — fast path.
	rootMarkers := []string{
		"pyproject.toml", "setup.py", "requirements.txt", "Pipfile",
		"pytest.ini", "mypy.ini", "tox.ini", "setup.cfg",
	}
	for _, name := range rootMarkers {
		if _, err := os.Stat(filepath.Join(repoPath, name)); err == nil {
			return true, nil
		}
	}

	// Subdirectory search (up to 3 levels deep) — handles monorepos.
	subMarkers := map[string]bool{
		"pyproject.toml":   true,
		"setup.py":         true,
		"requirements.txt": true,
		"Pipfile":          true,
	}
	found := false
	_ = filepath.WalkDir(repoPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil || found {
			return nil
		}
		rel, _ := filepath.Rel(repoPath, path)
		depth := strings.Count(filepath.ToSlash(rel), "/")
		if d.IsDir() {
			if depth >= 3 {
				return filepath.SkipDir
			}
			return nil
		}
		if subMarkers[filepath.Base(path)] {
			found = true
		}
		return nil
	})
	return found, nil
}

// Extract parses Python files and emits architectural facts.
//
// Both passes parse files in parallel (bounded by GOMAXPROCS) but merge their
// results in stable file order, so the emitted facts are byte-for-byte identical
// regardless of how the work was scheduled. Python parsing dominates snapshot
// time on large polyglot repos, so this is the main throughput lever.
func (e *PythonExtractor) Extract(ctx context.Context, repoPath string, files []string) ([]facts.Fact, error) {
	isDjango := detectDjango(repoPath)

	// Restrict to Python files once; both passes iterate the same ordered set.
	var pyFiles []string
	for _, relFile := range files {
		if isPythonFile(relFile) {
			pyFiles = append(pyFiles, relFile)
		}
	}

	// Pass 1: build a global symbol index across all Python files. Each file is
	// indexed into a local table in parallel, then the tables are merged in file
	// order so duplicate-module last-write-wins stays deterministic.
	idx := &pySymbolIndex{classes: make(map[string]*pyClassInfo), moduleDefs: make(map[string]map[string]bool)}
	localIdxs := parallel.MapFiles(ctx, pyFiles, func(relFile string) *pySymbolIndex {
		src, err := os.ReadFile(filepath.Join(repoPath, relFile))
		if err != nil {
			return nil
		}
		local := &pySymbolIndex{classes: make(map[string]*pyClassInfo), moduleDefs: make(map[string]map[string]bool)}
		buildFileIndex(src, relFile, local)
		return local
	})
	for _, m := range localIdxs {
		if m == nil {
			continue
		}
		for qualName, info := range m.classes {
			idx.classes[qualName] = info
		}
		for module, defs := range m.moduleDefs {
			idx.moduleDefs[module] = defs
		}
	}
	finalizeImplMap(idx)

	// Pass 2: extract facts using the populated symbol index (read-only here, so
	// the per-file work is fully independent and parallel-safe).
	perFileFacts := parallel.MapFiles(ctx, pyFiles, func(relFile string) []facts.Fact {
		src, err := os.ReadFile(filepath.Join(repoPath, relFile))
		if err != nil {
			log.Printf("[python-extractor] error reading %s: %v", relFile, err)
			return nil
		}
		return extractFileAST(src, relFile, isDjango, idx)
	})

	var allFacts []facts.Fact
	modules := make(map[string]bool)
	for i, ff := range perFileFacts {
		allFacts = append(allFacts, ff...)
		modules[filepath.Dir(pyFiles[i])] = true
	}

	// Resolve dotted import targets to internal module slash paths (and classify
	// stdlib/external) now that the full module set is known. Without this,
	// Python imports never match module Names downstream.
	resolveImports(allFacts, modules)

	// Parse pyproject.toml entry-points (console_scripts, plugin groups) into
	// reference edges, so functions registered as entry points — loaded by name by
	// the framework, never called in-code — are not mis-reported as dead. Emitted
	// before resolveCallTargets so their dotted targets resolve to slash symbols.
	allFacts = append(allFacts, extractEntryPoints(repoPath, files)...)

	// Resolve the dotted call/instantiate targets emitted for absolute imports into
	// canonical slash symbol names (dropping stdlib/third-party edges) now that the
	// full file set is known. Without this, functions reached via absolute imports
	// have no incoming edge and read as dead code.
	fileModules := make(map[string]bool, len(pyFiles))
	for _, f := range pyFiles {
		fileModules[strings.TrimSuffix(f, ".py")] = true
	}
	resolveCallTargets(allFacts, fileModules)

	for dir := range modules {
		allFacts = append(allFacts, facts.Fact{
			Kind: facts.KindModule,
			Name: dir,
			File: dir,
			Props: map[string]any{
				"language": "python",
			},
		})
	}

	return allFacts, nil
}

// --- Regex patterns used by the AST walker ---

var (
	// routeDecoratorRe matches FastAPI/Starlette route decorators.
	// Groups: (object, method, path). The path may be positional (`@r.get("/x")`) or
	// the `path=` keyword (`@r.get(path="/x")`), and may be empty (`path=""` — the
	// collection endpoint under the router prefix); `\s*` spans the newline of a
	// multi-line decorator.
	routeDecoratorRe = regexp.MustCompile(`^\s*@([\w.]+)\.(get|post|put|delete|patch|head|options)\s*\(\s*(?:path\s*=\s*)?["']([^"']*)["']`)

	// routeMethodRe matches a route-method decorator in ANY path form (literal,
	// path= keyword, or a computed expression), used to tag the handler as a
	// framework-dispatched entry point even when the path isn't a parseable literal.
	routeMethodRe = regexp.MustCompile(`^\s*@[\w.]+\.(?:get|post|put|delete|patch|head|options)\s*\(`)

	// tableNameRe matches SQLAlchemy __tablename__ assignments. Group: (table).
	tableNameRe = regexp.MustCompile(`^\s*__tablename__\s*=\s*["']([^"']+)["']`)

	// decoratorRe captures the full decorator name for structural prop detection.
	// Group: (name) e.g. "staticmethod", "app.task".
	decoratorRe = regexp.MustCompile(`^\s*@([\w.]+)`)

	// apiViewRe matches Django REST Framework @api_view decorators.
	// Group: (methods_list) — bracket contents, e.g. "'GET', 'POST'"
	apiViewRe = regexp.MustCompile(`^\s*@(?:[\w.]*\.)?api_view\s*\(\s*\[([^\]]+)\]`)

	// httpMethodWordRe extracts uppercase HTTP method tokens from an api_view list.
	httpMethodWordRe = regexp.MustCompile(`[A-Z]+`)

	// urlPathRe matches Django path() and re_path() calls in urls.py.
	// Groups: (url_path, view_ref)
	urlPathRe = regexp.MustCompile(`(?:re_)?path\s*\(\s*r?["']([^"']+)["']\s*,\s*([\w.]+)`)

	// entryPointSectionRe matches pyproject.toml TOML section headers that declare
	// entry points: [project.scripts], [project.gui-scripts], and
	// [project.entry-points…] (incl. quoted group subtables).
	entryPointSectionRe = regexp.MustCompile(`^\s*\[\s*project\.(scripts|gui-scripts|entry-points)\b`)
	// anyTOMLSectionRe matches any TOML section header (ends an entry-point section).
	anyTOMLSectionRe = regexp.MustCompile(`^\s*\[`)
	// entryPointValueRe matches an entry-point line `name = "module.path:attr"`,
	// capturing the module path and the attribute (a function or Class.method).
	entryPointValueRe = regexp.MustCompile(`^\s*[^=\[\]#]+=\s*["']([A-Za-z_][\w.]*):([A-Za-z_][\w.]*)["']`)

	// registrationDecorators are decorator names (matched on the last dotted segment)
	// that REGISTER their function with a framework, which then dispatches it — so the
	// function has no in-code caller by construction (like a route handler or CLI
	// command). Covers SQLAlchemy (@compiles, @event.listens_for), functools
	// singledispatch (@base.register), signals (@sig.connect), and Flask app/blueprint
	// hooks. A function with any such decorator is marked used via a self file-ref edge.
	registrationDecorators = map[string]bool{
		"compiles": true, "listens_for": true, // SQLAlchemy
		"register": true, // functools.singledispatch (also ABCMeta/registries)
		"connect":  true, // blinker/Celery/Django signals
		"errorhandler": true, "app_errorhandler": true,
		"before_request": true, "after_request": true,
		"teardown_request": true, "teardown_appcontext": true,
		"context_processor": true,
		"template_filter":   true, "template_global": true, "template_test": true,
	}

	// dottedPathRe matches a string literal that is an identifier-dotted symbol path
	// of at least 3 segments (module.sub.symbol), e.g. a lazy_load_command target or
	// a provider "class-name". The ≥3-segment floor avoids crediting short dotted
	// strings (logger names, config keys); resolveCallTargets prunes non-internal ones.
	dottedPathRe = regexp.MustCompile(`^[A-Za-z_]\w*(?:\.[A-Za-z_]\w*){2,}$`)
)

// extractEntryPoints scans each pyproject.toml in files for console-script / plugin
// entry points and emits a KindFileRef fact per file carrying RelCalls edges to the
// referenced module.attr targets (colon rewritten to dot). Entry-point functions are
// loaded by name by the framework, so without these edges they read as dead code.
// The dotted targets are resolved to slash symbols by resolveCallTargets.
func extractEntryPoints(repoPath string, files []string) []facts.Fact {
	var out []facts.Fact
	for _, rel := range files {
		if filepath.Base(rel) != "pyproject.toml" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(repoPath, rel))
		if err != nil {
			continue
		}
		var rels []facts.Relation
		inEntryPoints := false
		for _, line := range strings.Split(string(data), "\n") {
			if anyTOMLSectionRe.MatchString(line) {
				inEntryPoints = entryPointSectionRe.MatchString(line)
				continue
			}
			if !inEntryPoints {
				continue
			}
			if m := entryPointValueRe.FindStringSubmatch(line); m != nil {
				rels = append(rels, facts.Relation{Kind: facts.RelCalls, Target: m[1] + "." + m[2]})
			}
		}
		if len(rels) > 0 {
			out = append(out, facts.Fact{
				Kind:      facts.KindFileRef,
				Name:      rel,
				File:      rel,
				Line:      1,
				Props:     map[string]any{"language": "python"},
				Relations: rels,
			})
		}
	}
	return out
}

// Django class base sets used to classify models, views, and serializers.
var (
	djangoModelBases = map[string]bool{
		"Model": true, "AbstractModel": true, "MPTTModel": true,
		"TimeStampedModel": true, "UUIDModel": true, "PolymorphicModel": true,
	}

	djangoCBVBases = map[string]bool{
		"View": true, "APIView": true, "GenericAPIView": true,
		"ListAPIView": true, "CreateAPIView": true, "RetrieveAPIView": true,
		"UpdateAPIView": true, "DestroyAPIView": true, "ListCreateAPIView": true,
		"RetrieveUpdateDestroyAPIView": true, "ViewSet": true, "ModelViewSet": true,
		"ReadOnlyModelViewSet": true, "TemplateView": true, "DetailView": true,
		"ListView": true, "CreateView": true, "UpdateView": true, "DeleteView": true,
		"FormView": true, "RedirectView": true,
	}

	djangoSerializerBases = map[string]bool{
		"Serializer": true, "ModelSerializer": true,
		"HyperlinkedModelSerializer": true, "ListSerializer": true,
	}

	// pyAbstractBases are base classes / metaclasses that make a class abstract
	// in Python's sense (no direct interface keyword). Used to set the "abstract"
	// prop so package-metrics abstractness (A) is meaningful for Python.
	pyAbstractBases = map[string]bool{
		"ABC": true, "ABCMeta": true, "Protocol": true,
	}
)

// applyDecoratorProps sets structural boolean props on a symbol based on a
// decorator name. Only well-known structural decorators produce props; unknown
// decorators are silently ignored.
func applyDecoratorProps(props map[string]any, decoratorName string) {
	// Use the last dot-separated component: "functools.cached_property" → "cached_property".
	last := decoratorName
	if idx := strings.LastIndex(decoratorName, "."); idx >= 0 {
		last = decoratorName[idx+1:]
	}
	switch last {
	case "property", "cached_property":
		props["property"] = true
	case "staticmethod":
		props["static"] = true
	case "classmethod":
		props["class_method"] = true
	case "abstractmethod":
		props["abstract"] = true
	case "task":
		props["task"] = true
	case "command", "group":
		// click/Typer CLI command or group (@cli.command, @app.group). The function
		// is registered with and dispatched by the CLI framework, never called by
		// name, so the dead-code detector treats it as an entry point.
		props["cli_command"] = true
	case "shared_task":
		// shared_task is Celery-specific; bare @task is used by Airflow, Prefect, Luigi, etc.
		props["task"] = true
		props["framework"] = "celery"
	}
}

// detectDjango returns true if the project at repoPath uses Django, by scanning
// common dependency files and checking for manage.py.
func detectDjango(repoPath string) bool {
	for _, name := range []string{"requirements.txt", "pyproject.toml", "setup.cfg", "setup.py"} {
		data, err := os.ReadFile(filepath.Join(repoPath, name))
		if err != nil {
			continue
		}
		if strings.Contains(strings.ToLower(string(data)), "django") {
			return true
		}
	}
	_, err := os.Stat(filepath.Join(repoPath, "manage.py"))
	return err == nil
}

// camelToSnake converts a PascalCase class name to the snake_case table name
// Django would auto-generate. e.g. "UserProfile" → "user_profile".
func camelToSnake(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if i > 0 && ch >= 'A' && ch <= 'Z' {
			b.WriteByte('_')
		}
		if ch >= 'A' && ch <= 'Z' {
			b.WriteByte(ch + 32) // ASCII lowercase
		} else {
			b.WriteByte(ch)
		}
	}
	return b.String()
}

// lastComponent returns the last dot-separated segment of a qualified name.
// e.g. "models.Model" → "Model", "Model" → "Model".
func lastComponent(name string) string {
	if idx := strings.LastIndex(name, "."); idx >= 0 {
		return name[idx+1:]
	}
	return name
}

// isPythonFile returns true if the file has a .py extension.
func isPythonFile(path string) bool {
	return strings.HasSuffix(strings.ToLower(path), ".py")
}

// OwnsFile implements plugin.FileOwner for incremental caching.
func (e *PythonExtractor) OwnsFile(relFile string) bool { return isPythonFile(relFile) }
