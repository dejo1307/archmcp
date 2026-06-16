package rubyextractor

import (
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/enola-labs/enola/internal/facts"
)

// symbolListRe extracts symbol names from a string like ":index, :show, :create".
var symbolListRe = regexp.MustCompile(`:(\w+)`)

// extractAllRoutes finds and parses all Rails route files in the repository.
func extractAllRoutes(repoPath string, files []string) []facts.Fact {
	var allFacts []facts.Fact

	// Collect route files: config/routes.rb, config/routes/*.rb, packages/*/config/routes/*.rb
	var routeFiles []string
	for _, relFile := range files {
		if !isRubyFile(relFile) {
			continue
		}
		if isRouteFile(relFile) {
			routeFiles = append(routeFiles, relFile)
		}
	}

	for _, relFile := range routeFiles {
		absFile := filepath.Join(repoPath, relFile)
		src, err := os.ReadFile(absFile)
		if err != nil {
			log.Printf("[ruby-extractor] error reading route file %s: %v", relFile, err)
			continue
		}
		allFacts = append(allFacts, parseRouteFileAST(src, relFile)...)
	}

	return allFacts
}

// isRouteFile returns true if the file path looks like a Rails route file.
func isRouteFile(relFile string) bool {
	// config/routes.rb
	if relFile == filepath.Join("config", "routes.rb") {
		return true
	}
	// config/routes/*.rb
	if strings.HasPrefix(relFile, filepath.Join("config", "routes")+string(filepath.Separator)) {
		return true
	}
	// packages/*/config/routes/*.rb (packwerk pattern)
	parts := strings.Split(filepath.ToSlash(relFile), "/")
	for i := 0; i+3 < len(parts); i++ {
		if parts[i] == "packages" && parts[i+2] == "config" && parts[i+3] == "routes" {
			return true
		}
	}
	return false
}

// routeScope tracks the current route scope prefix.
type routeScope struct {
	pathPrefix string
	module     string
}

// buildPrefix constructs the current URL prefix from the scope stack.
func buildPrefix(stack []routeScope) string {
	var parts []string
	for _, s := range stack {
		if s.pathPrefix != "" {
			parts = append(parts, s.pathPrefix)
		}
	}
	return strings.Join(parts, "")
}

// restAction describes a single RESTful action.
type restAction struct {
	name   string
	method string
	suffix string
}

// restfulActions returns the set of REST actions for a resources declaration,
// honoring only:/except: filters parsed from the declaration's arguments.
func restfulActions(only, except map[string]bool) []restAction {
	all := []restAction{
		{name: "index", method: "GET", suffix: ""},
		{name: "create", method: "POST", suffix: ""},
		{name: "new", method: "GET", suffix: "/new"},
		{name: "show", method: "GET", suffix: "/:id"},
		{name: "update", method: "PATCH", suffix: "/:id"},
		{name: "edit", method: "GET", suffix: "/:id/edit"},
		{name: "destroy", method: "DELETE", suffix: "/:id"},
	}

	if len(only) > 0 {
		return filterActions(all, only, true)
	}
	if len(except) > 0 {
		return filterActions(all, except, false)
	}
	return all
}

// parseSymbolList extracts symbol names from a string like ":index, :show, :create".
func parseSymbolList(s string) map[string]bool {
	result := make(map[string]bool)
	for _, m := range symbolListRe.FindAllStringSubmatch(s, -1) {
		result[m[1]] = true
	}
	return result
}

// filterActions returns actions filtered by an allow or deny list.
func filterActions(all []restAction, names map[string]bool, isAllow bool) []restAction {
	var result []restAction
	for _, a := range all {
		if isAllow {
			if names[a.name] {
				result = append(result, a)
			}
		} else {
			if !names[a.name] {
				result = append(result, a)
			}
		}
	}
	return result
}
