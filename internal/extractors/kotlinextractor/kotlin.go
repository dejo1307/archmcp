package kotlinextractor

import (
	"bufio"
	"context"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/dejo1307/archmcp/internal/facts"
)

// KotlinExtractor extracts architectural facts from Kotlin source code using line-based regex parsing.
type KotlinExtractor struct{}

// New creates a new KotlinExtractor.
func New() *KotlinExtractor {
	return &KotlinExtractor{}
}

func (e *KotlinExtractor) Name() string {
	return "kotlin"
}

// Detect returns true if the repository looks like a Kotlin or Android project.
func (e *KotlinExtractor) Detect(repoPath string) (bool, error) {
	for _, name := range []string{"build.gradle.kts", "build.gradle"} {
		path := filepath.Join(repoPath, name)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		content := string(data)
		if strings.Contains(content, "kotlin") || strings.Contains(content, "android") {
			return true, nil
		}
	}
	return false, nil
}

// Extract parses Kotlin files and emits architectural facts.
func (e *KotlinExtractor) Extract(ctx context.Context, repoPath string, files []string) ([]facts.Fact, error) {
	var allFacts []facts.Fact

	isAndroid := detectAndroidProject(repoPath)

	// Detect source root and base package for import resolution
	sourceRoot := detectKotlinSourceRoot(repoPath, files)
	basePackage := detectKotlinBasePackage(repoPath)

	modules := make(map[string]bool)

	for _, relFile := range files {
		select {
		case <-ctx.Done():
			return allFacts, ctx.Err()
		default:
		}

		if !isKotlinFile(relFile) {
			continue
		}

		absFile := filepath.Join(repoPath, relFile)
		f, err := os.Open(absFile)
		if err != nil {
			log.Printf("[kotlin-extractor] error reading %s: %v", relFile, err)
			continue
		}

		fileFacts := extractFile(f, relFile, isAndroid, sourceRoot, basePackage)
		f.Close()
		allFacts = append(allFacts, fileFacts...)

		dir := filepath.Dir(relFile)
		modules[dir] = true
	}

	for dir := range modules {
		allFacts = append(allFacts, facts.Fact{
			Kind: facts.KindModule,
			Name: dir,
			File: dir,
			Props: map[string]any{
				"language": "kotlin",
			},
		})
	}

	return allFacts, nil
}

// --- Regex patterns ---

var (
	packageRe   = regexp.MustCompile(`^\s*package\s+([\w.]+)`)
	importRe    = regexp.MustCompile(`^\s*import\s+([\w.*]+)`)
	annotationRe = regexp.MustCompile(`^\s*@(\w+)`)

	// Class / interface declarations.
	// Captures: modifiers (group 1), keyword (group 2), name (group 3).
	// Supertypes are extracted separately via extractSupertypesFromLine.
	classRe = regexp.MustCompile(
		`^\s*((?:(?:public|private|internal|protected|open|abstract|sealed|data|enum|inner|annotation|value)\s+)*)` +
			`(class|interface)\s+(\w+)`)

	objectRe = regexp.MustCompile(
		`^\s*(?:((?:(?:public|private|internal|protected)\s+)*)` +
			`object\s+(\w+))` +
			`(?:\s*:\s*(.+?))?\s*\{?\s*$`)

	// Function declarations.
	funcRe = regexp.MustCompile(
		`^\s*(?:(?:public|private|internal|protected|open|abstract|override|inline|suspend|operator|infix|tailrec|external)\s+)*` +
			`fun\s+(?:<[^>]*>\s+)?(\w+)\s*\(`)

	// Top-level property declarations.
	propRe = regexp.MustCompile(
		`^\s*(?:(?:public|private|internal|protected|open|abstract|override|lateinit|const|lazy)\s+)*(val|var)\s+(\w+)`)

	typealiasRe = regexp.MustCompile(`^\s*(?:(?:public|private|internal|protected)\s+)*typealias\s+(\w+)`)

	// Visibility check — private or internal means not exported.
	privateOrInternalRe = regexp.MustCompile(`\b(private|internal)\b`)
)

// pendingClass tracks a class declaration that spans multiple lines (e.g., multi-line constructor).
type pendingClass struct {
	modifiers   string
	keyword     string // "class" or "interface"
	name        string
	line        int
	annotations []string
	parenDepth  int    // tracks unclosed parentheses
	lines       string // accumulated text after class name for supertype extraction
}

// extractFile parses a single Kotlin file and returns facts.
func extractFile(f *os.File, relFile string, isAndroid bool, sourceRoot, basePackage string) []facts.Fact {
	var result []facts.Fact
	dir := filepath.Dir(relFile)

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)

	var (
		lineNum            int
		braceDepth         int
		pendingAnnotations []string
		pending            *pendingClass
	)

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()

		// Track brace depth for top-level detection.
		braceDepth += strings.Count(line, "{") - strings.Count(line, "}")

		// If we have a pending multi-line class declaration, accumulate lines.
		if pending != nil {
			pending.parenDepth += strings.Count(line, "(") - strings.Count(line, ")")
			pending.lines += " " + strings.TrimSpace(line)

			// Once parentheses are balanced and we see { or end of declaration, emit the fact.
			if pending.parenDepth <= 0 || strings.Contains(line, "{") {
				supertypes := extractSupertypesFromText(pending.lines)
				fact := buildClassFact(dir, relFile, pending, supertypes, isAndroid)
				if isAndroid {
					if sf := detectRoomStorage(pending.name, pending.annotations, relFile, pending.line, dir); sf != nil {
						result = append(result, *sf)
					}
				}
				result = append(result, fact)
				pending = nil
			}
			continue
		}

		// Collect annotations (apply to the next declaration).
		if m := annotationRe.FindStringSubmatch(line); m != nil {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "@") && !funcRe.MatchString(line) && !classRe.MatchString(line) && !objectRe.MatchString(line) {
				pendingAnnotations = append(pendingAnnotations, m[1])
				continue
			}
		}

		effectiveDepth := braceDepth - strings.Count(line, "{")

		inlineAnnotations := collectInlineAnnotations(line)
		allAnnotations := append(pendingAnnotations, inlineAnnotations...)

		if effectiveDepth == 0 {
			// Import statements.
			if m := importRe.FindStringSubmatch(line); m != nil {
				importPath := m[1]

				// Resolve internal imports to filesystem-relative paths
				resolved, isExternal := resolveKotlinImport(importPath, sourceRoot, basePackage)

				importSource := "internal"
				if isExternal {
					importSource = "external"
				}

				result = append(result, facts.Fact{
					Kind: facts.KindDependency,
					Name: dir + " -> " + resolved,
					File: relFile,
					Line: lineNum,
					Props: map[string]any{
						"language": "kotlin",
						"source":   importSource,
					},
					Relations: []facts.Relation{
						{Kind: facts.RelImports, Target: resolved},
					},
				})
				pendingAnnotations = nil
				continue
			}

			// Class / interface declarations.
			if m := classRe.FindStringSubmatch(line); m != nil {
				modifiers := m[1]
				keyword := m[2]
				name := m[3]

				// Get the text after "class Name" for supertype extraction.
				nameIdx := strings.Index(line, keyword+" "+name)
				restOfLine := line[nameIdx+len(keyword)+1+len(name):]

				// Check if parentheses are balanced on this line.
				parenDepth := strings.Count(restOfLine, "(") - strings.Count(restOfLine, ")")

				if parenDepth > 0 && !strings.Contains(line, "{") {
					// Multi-line constructor — save and continue.
					pending = &pendingClass{
						modifiers:   modifiers,
						keyword:     keyword,
						name:        name,
						line:        lineNum,
						annotations: append([]string{}, allAnnotations...),
						parenDepth:  parenDepth,
						lines:       restOfLine,
					}
					pendingAnnotations = nil
					continue
				}

				// Single-line declaration — extract supertypes from the rest of the line.
				supertypes := extractSupertypesFromText(restOfLine)
				pc := &pendingClass{
					modifiers:   modifiers,
					keyword:     keyword,
					name:        name,
					line:        lineNum,
					annotations: allAnnotations,
				}
				fact := buildClassFact(dir, relFile, pc, supertypes, isAndroid)
				if isAndroid {
					if sf := detectRoomStorage(name, allAnnotations, relFile, lineNum, dir); sf != nil {
						result = append(result, *sf)
					}
				}
				result = append(result, fact)
				pendingAnnotations = nil
				continue
			}

			// Object declarations.
			if m := objectRe.FindStringSubmatch(line); m != nil {
				modifiers := m[1]
				name := m[2]
				supertypes := m[3]

				exported := !privateOrInternalRe.MatchString(modifiers)

				of := facts.Fact{
					Kind: facts.KindSymbol,
					Name: dir + "." + name,
					File: relFile,
					Line: lineNum,
					Props: map[string]any{
						"symbol_kind": facts.SymbolClass,
						"exported":    exported,
						"language":    "kotlin",
						"object":      true,
					},
					Relations: []facts.Relation{
						{Kind: facts.RelDeclares, Target: dir},
					},
				}

				if supertypes != "" {
					for _, st := range parseSupertypes(supertypes) {
						of.Relations = append(of.Relations, facts.Relation{
							Kind:   facts.RelImplements,
							Target: st,
						})
					}
				}

				result = append(result, of)
				pendingAnnotations = nil
				continue
			}

			// Function declarations.
			if m := funcRe.FindStringSubmatch(line); m != nil {
				name := m[1]

				exported := !privateOrInternalRe.MatchString(line)

				ff := facts.Fact{
					Kind: facts.KindSymbol,
					Name: dir + "." + name,
					File: relFile,
					Line: lineNum,
					Props: map[string]any{
						"symbol_kind": facts.SymbolFunc,
						"exported":    exported,
						"language":    "kotlin",
					},
					Relations: []facts.Relation{
						{Kind: facts.RelDeclares, Target: dir},
					},
				}

				if strings.Contains(line, "suspend ") {
					ff.Props["suspend"] = true
				}

				if isAndroid && containsAnnotation(allAnnotations, "Composable") {
					ff.Props["android_component"] = "composable"
					ff.Props["framework"] = "android"
				}

				result = append(result, ff)
				pendingAnnotations = nil
				continue
			}

			// Top-level property declarations.
			if m := propRe.FindStringSubmatch(line); m != nil {
				valOrVar := m[1]
				name := m[2]

				if name == "_" {
					pendingAnnotations = nil
					continue
				}

				exported := !privateOrInternalRe.MatchString(line)

				symbolKind := facts.SymbolVariable
				if valOrVar == "val" {
					symbolKind = facts.SymbolConstant
				}

				result = append(result, facts.Fact{
					Kind: facts.KindSymbol,
					Name: dir + "." + name,
					File: relFile,
					Line: lineNum,
					Props: map[string]any{
						"symbol_kind": symbolKind,
						"exported":    exported,
						"language":    "kotlin",
					},
					Relations: []facts.Relation{
						{Kind: facts.RelDeclares, Target: dir},
					},
				})
				pendingAnnotations = nil
				continue
			}

			// Type alias declarations.
			if m := typealiasRe.FindStringSubmatch(line); m != nil {
				name := m[1]
				exported := !privateOrInternalRe.MatchString(line)

				result = append(result, facts.Fact{
					Kind: facts.KindSymbol,
					Name: dir + "." + name,
					File: relFile,
					Line: lineNum,
					Props: map[string]any{
						"symbol_kind": facts.SymbolType,
						"exported":    exported,
						"language":    "kotlin",
					},
					Relations: []facts.Relation{
						{Kind: facts.RelDeclares, Target: dir},
					},
				})
				pendingAnnotations = nil
				continue
			}
		}

		// Reset pending annotations if we hit a non-annotation, non-blank line that wasn't a declaration.
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !strings.HasPrefix(trimmed, "//") && !strings.HasPrefix(trimmed, "*") && !strings.HasPrefix(trimmed, "/*") {
			pendingAnnotations = nil
		}
	}

	return result
}

// buildClassFact creates a symbol fact for a class/interface declaration.
func buildClassFact(dir, relFile string, pc *pendingClass, supertypes string, isAndroid bool) facts.Fact {
	symbolKind := facts.SymbolClass
	if pc.keyword == "interface" {
		symbolKind = facts.SymbolInterface
	}

	exported := !privateOrInternalRe.MatchString(pc.modifiers)

	f := facts.Fact{
		Kind: facts.KindSymbol,
		Name: dir + "." + pc.name,
		File: relFile,
		Line: pc.line,
		Props: map[string]any{
			"symbol_kind": symbolKind,
			"exported":    exported,
			"language":    "kotlin",
		},
		Relations: []facts.Relation{
			{Kind: facts.RelDeclares, Target: dir},
		},
	}

	if strings.Contains(pc.modifiers, "data") {
		f.Props["data_class"] = true
	}
	if strings.Contains(pc.modifiers, "sealed") {
		f.Props["sealed"] = true
	}
	if strings.Contains(pc.modifiers, "enum") {
		f.Props["enum"] = true
	}
	if strings.Contains(pc.modifiers, "abstract") {
		f.Props["abstract"] = true
	}
	if strings.Contains(pc.modifiers, "annotation") {
		f.Props["annotation_class"] = true
	}

	if supertypes != "" {
		for _, st := range parseSupertypes(supertypes) {
			f.Relations = append(f.Relations, facts.Relation{
				Kind:   facts.RelImplements,
				Target: st,
			})
		}
	}

	if isAndroid {
		addAndroidProps(&f, pc.name, pc.annotations, supertypes)
	}

	return f
}

// extractSupertypesFromText finds the supertype clause after ":" in text that may
// contain constructor parameters. It skips content inside balanced parentheses.
func extractSupertypesFromText(text string) string {
	depth := 0
	for i, ch := range text {
		switch ch {
		case '(', '<':
			depth++
		case ')', '>':
			depth--
		case ':':
			if depth <= 0 {
				// Found the inheritance colon — everything after it until { or end.
				rest := text[i+1:]
				if braceIdx := strings.Index(rest, "{"); braceIdx >= 0 {
					rest = rest[:braceIdx]
				}
				return strings.TrimSpace(rest)
			}
		}
	}
	return ""
}

// --- Android detection helpers ---

// detectAndroidProject checks for AndroidManifest.xml.
func detectAndroidProject(repoPath string) bool {
	candidates := []string{
		filepath.Join(repoPath, "app", "src", "main", "AndroidManifest.xml"),
		filepath.Join(repoPath, "src", "main", "AndroidManifest.xml"),
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}

// addAndroidProps classifies a class/interface declaration as an Android component.
func addAndroidProps(f *facts.Fact, name string, annotations []string, supertypes string) {
	// Annotation-based classification.
	if containsAnnotation(annotations, "HiltAndroidApp") {
		f.Props["android_component"] = "application"
		f.Props["framework"] = "android"
		return
	}
	if containsAnnotation(annotations, "HiltViewModel") {
		f.Props["android_component"] = "viewmodel"
		f.Props["framework"] = "android"
		return
	}
	if containsAnnotation(annotations, "AndroidEntryPoint") {
		f.Props["framework"] = "android"
		// Refine based on supertype.
		if supertypeMatches(supertypes, "Activity", "ComponentActivity", "AppCompatActivity", "FragmentActivity") {
			f.Props["android_component"] = "activity"
		} else if supertypeMatches(supertypes, "Fragment") {
			f.Props["android_component"] = "fragment"
		} else if supertypeMatches(supertypes, "Service") {
			f.Props["android_component"] = "service"
		} else if supertypeMatches(supertypes, "BroadcastReceiver") {
			f.Props["android_component"] = "broadcast_receiver"
		}
		return
	}
	if containsAnnotation(annotations, "Module") {
		f.Props["android_component"] = "di_module"
		f.Props["framework"] = "android"
		return
	}

	// Name/supertype-based classification.
	if strings.HasSuffix(name, "ViewModel") || supertypeMatches(supertypes, "ViewModel") {
		f.Props["android_component"] = "viewmodel"
		f.Props["framework"] = "android"
		return
	}
	if supertypeMatches(supertypes, "Application") {
		f.Props["android_component"] = "application"
		f.Props["framework"] = "android"
		return
	}
	if supertypeMatches(supertypes, "Activity", "ComponentActivity", "AppCompatActivity") {
		f.Props["android_component"] = "activity"
		f.Props["framework"] = "android"
		return
	}
	if supertypeMatches(supertypes, "Fragment") {
		f.Props["android_component"] = "fragment"
		f.Props["framework"] = "android"
		return
	}
	if supertypeMatches(supertypes, "Service", "FirebaseMessagingService", "IntentService", "JobIntentService") {
		f.Props["android_component"] = "service"
		f.Props["framework"] = "android"
		return
	}
	if supertypeMatches(supertypes, "BroadcastReceiver") {
		f.Props["android_component"] = "broadcast_receiver"
		f.Props["framework"] = "android"
		return
	}
	if supertypeMatches(supertypes, "ContentProvider") {
		f.Props["android_component"] = "content_provider"
		f.Props["framework"] = "android"
		return
	}
	if supertypeMatches(supertypes, "Worker", "CoroutineWorker", "ListenableWorker") {
		f.Props["android_component"] = "worker"
		f.Props["framework"] = "android"
		return
	}
	if strings.HasSuffix(name, "Repository") || strings.HasSuffix(name, "RepositoryImpl") {
		f.Props["android_component"] = "repository"
		f.Props["framework"] = "android"
		return
	}
	if strings.HasSuffix(name, "UseCase") {
		f.Props["android_component"] = "usecase"
		f.Props["framework"] = "android"
		return
	}
}

// detectRoomStorage emits a storage fact for Room-annotated classes/interfaces.
func detectRoomStorage(name string, annotations []string, relFile string, line int, dir string) *facts.Fact {
	var storageKind string
	if containsAnnotation(annotations, "Entity") {
		storageKind = "entity"
	} else if containsAnnotation(annotations, "Dao") {
		storageKind = "dao"
	} else if containsAnnotation(annotations, "Database") {
		storageKind = "database"
	}
	if storageKind == "" {
		return nil
	}
	return &facts.Fact{
		Kind: facts.KindStorage,
		Name: dir + "." + name,
		File: relFile,
		Line: line,
		Props: map[string]any{
			"storage_kind": storageKind,
			"language":     "kotlin",
			"framework":    "room",
		},
		Relations: []facts.Relation{
			{Kind: facts.RelDeclares, Target: dir},
		},
	}
}

// --- Parsing helpers ---

// parseSupertypes splits a supertype clause like "Foo(), Bar, Baz<T>" into type names.
func parseSupertypes(clause string) []string {
	var result []string
	depth := 0
	start := 0
	for i, ch := range clause {
		switch ch {
		case '<', '(':
			depth++
		case '>', ')':
			depth--
		case ',':
			if depth == 0 {
				if t := extractTypeName(clause[start:i]); t != "" {
					result = append(result, t)
				}
				start = i + 1
			}
		}
	}
	if t := extractTypeName(clause[start:]); t != "" {
		result = append(result, t)
	}
	return result
}

// extractTypeName extracts the simple type name from a supertype entry like "Foo()" or "Bar<T>".
func extractTypeName(s string) string {
	s = strings.TrimSpace(s)
	// Strip generic parameters and constructor calls.
	for i, ch := range s {
		if ch == '<' || ch == '(' || ch == ' ' {
			s = s[:i]
			break
		}
	}
	s = strings.TrimSpace(s)
	// Take the last segment if it's a qualified name.
	if idx := strings.LastIndex(s, "."); idx >= 0 {
		s = s[idx+1:]
	}
	if s == "" {
		return ""
	}
	return s
}

// collectInlineAnnotations extracts annotation names from a line that also contains a declaration.
func collectInlineAnnotations(line string) []string {
	var result []string
	re := regexp.MustCompile(`@(\w+)`)
	matches := re.FindAllStringSubmatch(line, -1)
	for _, m := range matches {
		result = append(result, m[1])
	}
	return result
}

func containsAnnotation(annotations []string, name string) bool {
	for _, a := range annotations {
		if a == name {
			return true
		}
	}
	return false
}

func supertypeMatches(supertypes string, names ...string) bool {
	if supertypes == "" {
		return false
	}
	parsed := parseSupertypes(supertypes)
	for _, st := range parsed {
		for _, name := range names {
			if st == name {
				return true
			}
		}
	}
	return false
}

func isKotlinFile(path string) bool {
	return strings.HasSuffix(strings.ToLower(path), ".kt")
}

// detectKotlinSourceRoot examines Kotlin files to determine the source root directory.
// It reads the package declaration from the first Kotlin file and derives the source root
// by removing the package-as-path suffix from the file's directory.
// For example, file "app/src/main/java/com/foo/bar/MyFile.kt" with package "com.foo.bar"
// yields source root "app/src/main/java/".
func detectKotlinSourceRoot(repoPath string, files []string) string {
	for _, relFile := range files {
		if !isKotlinFile(relFile) {
			continue
		}
		absFile := filepath.Join(repoPath, relFile)
		f, err := os.Open(absFile)
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := scanner.Text()
			if m := packageRe.FindStringSubmatch(line); m != nil {
				pkg := m[1]
				pkgPath := strings.ReplaceAll(pkg, ".", "/")
				dir := filepath.ToSlash(filepath.Dir(relFile))
				if strings.HasSuffix(dir, pkgPath) {
					root := strings.TrimSuffix(dir, pkgPath)
					f.Close()
					return root
				}
				f.Close()
				return ""
			}
		}
		f.Close()
	}
	return ""
}

// detectKotlinBasePackage reads the Android namespace from build.gradle.kts
// to determine the project's base package (e.g., "com.fairwayhub.fairway").
// This is used to distinguish internal imports from external library imports.
func detectKotlinBasePackage(repoPath string) string {
	// Try build.gradle.kts and build.gradle in app/ and root
	candidates := []string{
		filepath.Join(repoPath, "app", "build.gradle.kts"),
		filepath.Join(repoPath, "app", "build.gradle"),
		filepath.Join(repoPath, "build.gradle.kts"),
		filepath.Join(repoPath, "build.gradle"),
	}
	nsRe := regexp.MustCompile(`namespace\s*=?\s*"([^"]+)"`)
	for _, path := range candidates {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if m := nsRe.FindSubmatch(data); m != nil {
			return string(m[1])
		}
	}
	return ""
}

// resolveKotlinImport normalizes a Kotlin import path.
// Internal imports (matching the base package) are converted from dotted package names
// to filesystem-relative paths so the graph can match them to module facts.
// External imports are left as-is.
func resolveKotlinImport(importPath, sourceRoot, basePackage string) (string, bool) {
	// If we have a base package and the import matches it, it's internal
	if basePackage != "" && sourceRoot != "" && strings.HasPrefix(importPath, basePackage+".") {
		asPath := strings.ReplaceAll(importPath, ".", "/")
		return filepath.ToSlash(filepath.Clean(sourceRoot + asPath)), false
	}

	// Everything else is external
	return importPath, true
}
