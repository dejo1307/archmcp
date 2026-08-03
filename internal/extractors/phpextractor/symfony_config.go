package phpextractor

import (
	"encoding/xml"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/enola-labs/enola/internal/facts"
	"gopkg.in/yaml.v3"
)

// extractSymfonyConfigRoutes parses Symfony's YAML and XML route configuration
// (config/routes.yaml, config/routes/*.yaml, and matching *.xml) and emits a
// server-route fact per declared route. This complements the attribute/annotation
// detection for projects that wire routes through config files. It is a repo-level
// pass (not per-PHP-file) since the config lives outside the PHP source tree.
//
// Route config files are discovered directly on disk rather than from the engine's
// walked file list — mirroring the OpenAPI extractor, which scans for its YAML/JSON
// specs the same way. The global `**/*.yaml`/`**/*.json` ignore that the bundled
// configs apply (to suppress config/data noise) would otherwise hide them. Only
// Symfony's dedicated route-config locations are read, so this stays cheap and targeted.
func extractSymfonyConfigRoutes(repoPath string) []facts.Fact {
	var out []facts.Fact
	for _, rel := range discoverSymfonyRouteConfigs(repoPath) {
		data, err := os.ReadFile(filepath.Join(repoPath, rel))
		if err != nil {
			continue
		}
		switch strings.ToLower(filepath.Ext(rel)) {
		case ".yaml", ".yml":
			out = append(out, parseSymfonyYAMLRoutes(data, rel)...)
		case ".xml":
			out = append(out, parseSymfonyXMLRoutes(data, rel)...)
		}
	}
	return out
}

// discoverSymfonyRouteConfigs returns the relative paths of Symfony route-config
// files present on disk: config/routes.{yaml,yml,xml} and every .yaml/.yml/.xml file
// under config/routes/ (recursively). Results are sorted for deterministic output.
func discoverSymfonyRouteConfigs(repoPath string) []string {
	seen := map[string]bool{}
	add := func(rel string) {
		if _, err := os.Stat(filepath.Join(repoPath, rel)); err == nil {
			seen[rel] = true
		}
	}
	for _, name := range []string{"routes.yaml", "routes.yml", "routes.xml"} {
		add(filepath.ToSlash(filepath.Join("config", name)))
	}
	routesDir := filepath.Join(repoPath, "config", "routes")
	_ = filepath.WalkDir(routesDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		switch strings.ToLower(filepath.Ext(path)) {
		case ".yaml", ".yml", ".xml":
			if rel, err := filepath.Rel(repoPath, path); err == nil {
				seen[filepath.ToSlash(rel)] = true
			}
		}
		return nil
	})
	out := make([]string, 0, len(seen))
	for rel := range seen {
		out = append(out, rel)
	}
	sort.Strings(out)
	return out
}

// yamlRouteEntry is a single Symfony YAML route definition. `path` and `methods` may
// each be a scalar or a sequence in Symfony's schema, so they are decoded loosely.
type yamlRouteEntry struct {
	Path       any    `yaml:"path"`
	Controller string `yaml:"controller"`
	Methods    any    `yaml:"methods"`
}

// parseSymfonyYAMLRoutes decodes a route YAML file (map of name -> entry) and emits a
// fact per route/method. Entries without a scalar path (imports, localized paths) are
// skipped.
func parseSymfonyYAMLRoutes(data []byte, relFile string) []facts.Fact {
	var doc map[string]yamlRouteEntry
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil
	}
	names := make([]string, 0, len(doc))
	for name := range doc {
		names = append(names, name)
	}
	sort.Strings(names)

	dir := filepath.Dir(relFile)
	var out []facts.Fact
	for _, name := range names {
		entry := doc[name]
		path, ok := entry.Path.(string)
		if !ok || path == "" {
			continue
		}
		for _, verb := range methodsList(entry.Methods) {
			out = append(out, symfonyConfigFact(name, verb, path, entry.Controller, relFile, dir))
		}
	}
	return out
}

// xmlRoutesDoc mirrors Symfony's <routes><route .../></routes> XML schema.
type xmlRoutesDoc struct {
	Routes []struct {
		ID         string `xml:"id,attr"`
		Path       string `xml:"path,attr"`
		Controller string `xml:"controller,attr"`
		Methods    string `xml:"methods,attr"`
	} `xml:"route"`
}

// parseSymfonyXMLRoutes decodes a route XML file and emits a fact per route/method.
func parseSymfonyXMLRoutes(data []byte, relFile string) []facts.Fact {
	var doc xmlRoutesDoc
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil
	}
	dir := filepath.Dir(relFile)
	var out []facts.Fact
	for _, r := range doc.Routes {
		if r.Path == "" {
			continue
		}
		for _, verb := range methodsList(r.Methods) {
			out = append(out, symfonyConfigFact(r.ID, verb, r.Path, r.Controller, relFile, dir))
		}
	}
	return out
}

// symfonyConfigFact builds one server-route fact from a config-declared route.
func symfonyConfigFact(name, method, path, controller, relFile, dir string) facts.Fact {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	props := map[string]any{
		facts.PropRole:   facts.RoleServer,
		"method":         method,
		"framework":      "symfony",
		"language":       "php",
		facts.PropSource: facts.RouteSourceSymfonyConfig,
		"path":           path,
	}
	if controller != "" {
		props["handler"] = controller
	}
	if name != "" {
		props["name"] = name
	}
	return facts.Fact{
		Kind:      facts.KindRoute,
		Name:      path,
		File:      relFile,
		Props:     props,
		Relations: []facts.Relation{{Kind: facts.RelDeclares, Target: dir}},
	}
}

// methodsList normalizes a YAML/XML methods value (scalar, sequence, or a
// space/pipe/comma-separated string) into upper-case verbs, defaulting to ["ANY"]
// when none are specified.
func methodsList(v any) []string {
	var raw []string
	switch t := v.(type) {
	case nil:
		// none
	case string:
		raw = splitMethods(t)
	case []any:
		for _, e := range t {
			if s, ok := e.(string); ok {
				raw = append(raw, s)
			}
		}
	case []string:
		raw = t
	}
	if len(raw) == 0 {
		return []string{"ANY"}
	}
	out := make([]string, 0, len(raw))
	for _, m := range raw {
		m = strings.ToUpper(strings.TrimSpace(m))
		if m != "" {
			out = append(out, m)
		}
	}
	if len(out) == 0 {
		return []string{"ANY"}
	}
	return out
}

// splitMethods splits a methods string on pipes, commas, or whitespace.
func splitMethods(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		return r == '|' || r == ',' || r == ' ' || r == '\t'
	})
}
