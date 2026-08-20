// Package eslintscaffold turns mined constraint candidates whose statement is
// expressible as a file-local syntactic check into ESLint rule scaffolds: a
// rule module and a RuleTester test, shaped like the rules of a repository's
// own ESLint plugin so each file moves in unchanged. A candidate that needs the graph (a call edge
// resolved through the linker, a prop implication, a method presence) stays a
// constraint proposal and is reported here with the reason it was left.
//
// Two families qualify. A naming regularity over symbols declared in
// JavaScript or TypeScript files becomes a rule over top-level class and
// function declarations under the cluster's directory. A forbidden import edge
// between two clusters becomes a rule over ImportDeclaration sources in files
// under the source cluster. Both are file-local: the rule reads one file and
// its own path, which is exactly what ESLint can see.
package eslintscaffold

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/enola-labs/enola/internal/facts"
	"github.com/enola-labs/enola/internal/mining"
)

// Scaffold is one rendered rule: its ESLint id, the rule module and the test.
type Scaffold struct {
	RuleID    string
	RuleFile  string
	TestFile  string
	Candidate mining.Candidate
}

// Skip names a candidate the scaffolder left as a constraint proposal and why.
type Skip struct {
	Identity string
	Reason   string
}

// Result is the outcome of scaffolding a report: what was rendered and what was
// left, in report order.
type Result struct {
	Scaffolds []Scaffold
	Skipped   []Skip
}

var (
	scriptExtensions = map[string]bool{".js": true, ".mjs": true, ".cjs": true, ".ts": true, ".mts": true, ".cts": true, ".gjs": true, ".gts": true, ".jsx": true, ".tsx": true}
	plainIdentifier  = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)
)

// Render decides for every candidate and renders the eligible ones.
func Render(candidates []mining.Candidate) Result {
	var res Result
	for _, c := range candidates {
		if reason := ineligible(c); reason != "" {
			res.Skipped = append(res.Skipped, Skip{Identity: c.Identity, Reason: reason})
			continue
		}
		sc := Scaffold{RuleID: c.Rule.ID, Candidate: c}
		switch c.Family {
		case mining.FamilyNaming:
			sc.RuleFile = renderNamingRule(c)
			sc.TestFile = renderNamingTest(c)
		case mining.FamilyForbidEdge:
			sc.RuleFile = renderForbidImportRule(c)
			sc.TestFile = renderForbidImportTest(c)
		}
		res.Scaffolds = append(res.Scaffolds, sc)
	}
	return res
}

// Write renders and writes the scaffolds under dir: rules at the top level, tests
// under tests/, and an index.js that registers every rule written, so the
// directory loads as a plugin on its own and each file moves into a real plugin
// unchanged. Returns the paths written.
func Write(dir string, candidates []mining.Candidate) (Result, []string, error) {
	res := Render(candidates)
	if len(res.Scaffolds) == 0 {
		return res, nil, nil
	}
	if err := os.MkdirAll(filepath.Join(dir, "tests"), 0o755); err != nil {
		return res, nil, err
	}
	var written []string
	for _, sc := range res.Scaffolds {
		rulePath := filepath.Join(dir, sc.RuleID+".js")
		testPath := filepath.Join(dir, "tests", sc.RuleID+".test.js")
		if err := os.WriteFile(rulePath, []byte(sc.RuleFile), 0o644); err != nil {
			return res, written, err
		}
		written = append(written, rulePath)
		if err := os.WriteFile(testPath, []byte(sc.TestFile), 0o644); err != nil {
			return res, written, err
		}
		written = append(written, testPath)
	}
	indexPath := filepath.Join(dir, "index.js")
	if err := os.WriteFile(indexPath, []byte(renderIndex(res.Scaffolds)), 0o644); err != nil {
		return res, written, err
	}
	written = append(written, indexPath)
	return res, written, nil
}

func ineligible(c mining.Candidate) string {
	switch c.Family {
	case mining.FamilyNaming:
		if c.Kind != facts.KindSymbol {
			return "naming over " + c.Kind + " facts is a path convention, not a declaration ESLint sees"
		}
		if len(c.Components) == 0 || len(c.Components[0].Match) == 0 {
			return "no cluster to scope the rule to"
		}
		if ext := nonScriptExtension(c); ext != "" {
			return "witness files are not JavaScript or TypeScript (" + ext + ")"
		}
		if _, _, reason := localPattern(c); reason != "" {
			return reason
		}
		for _, w := range c.Witnesses {
			if _, reason := localName(c, w.Name); reason != "" {
				return reason
			}
		}
		for _, e := range c.Exceptions {
			if _, reason := localName(c, e.Name); reason != "" {
				return reason
			}
		}
	case mining.FamilyForbidEdge:
		if c.Rule.Via != facts.RelImports {
			return "a " + c.Rule.Via + " edge is resolved through the graph, not read from an import statement"
		}
	default:
		return "the " + c.Family + " family needs the graph"
	}
	if len(c.Witnesses) == 0 {
		return "no conforming witness to build a valid test case from"
	}
	if ext := nonScriptExtension(c); ext != "" {
		return "witness files are not JavaScript or TypeScript (" + ext + ")"
	}
	if len(c.Components) == 0 || len(c.Components[0].Match) == 0 {
		return "no cluster to scope the rule to"
	}
	return ""
}

func nonScriptExtension(c mining.Candidate) string {
	for _, w := range c.Witnesses {
		if ext := strings.ToLower(path.Ext(w.File)); !scriptExtensions[ext] {
			return orNoExtension(ext)
		}
	}
	for _, e := range c.Exceptions {
		if ext := strings.ToLower(path.Ext(e.File)); !scriptExtensions[ext] {
			return orNoExtension(ext)
		}
	}
	return ""
}

func orNoExtension(ext string) string {
	if ext == "" {
		return "no extension"
	}
	return ext
}

func clusterDir(c mining.Candidate, i int) string {
	return strings.TrimSuffix(c.Components[i].Match[0], "/**")
}

// qualifier is what the TypeScript extractor puts in front of a symbol's own
// name: its module path and a dot, so a class Foo in src/services/api.ts is the
// fact "src/services.Foo". A file-local rule sees only "Foo", so the pattern and
// every witness are cut down to the part after the cluster's qualifier.
func qualifier(c mining.Candidate) string {
	return clusterDir(c, 0) + "."
}

// localName is the declaration a symbol fact belongs to: the part after the
// cluster's module qualifier, cut at the first dot, so a class and its members
// ("src/commands/repo.RepoClone" and "src/commands/repo.RepoClone.description")
// both name the declaration RepoClone, which is what the rule can see.
func localName(c mining.Candidate, name string) (string, string) {
	local := name
	switch {
	case strings.HasPrefix(name, qualifier(c)):
		local = name[len(qualifier(c)):]
	case strings.HasPrefix(name, clusterDir(c, 0)+"/"):
		if i := strings.Index(name, "."); i >= 0 {
			local = name[i+1:]
		}
	}
	if i := strings.Index(local, "."); i >= 0 {
		local = local[:i]
	}
	if !plainIdentifier.MatchString(local) {
		return "", "symbol names are qualified (" + name + "), not declarations a file-local rule can name"
	}
	return local, ""
}

// localPattern turns the mined pattern over qualified names into a prefix and
// suffix over local names, or explains why it cannot: a prefix that is the
// module path itself says nothing about the names under it.
func localPattern(c mining.Candidate) (prefix, suffix, reason string) {
	pattern := c.Rule.Pattern
	switch {
	case strings.HasPrefix(pattern, "*"):
		suffix = pattern[1:]
		if strings.ContainsAny(suffix, "./") {
			return "", "", "pattern " + pattern + " reaches into the module path, not the declared name"
		}
		return "", suffix, ""
	case strings.HasSuffix(pattern, "*"):
		prefix = pattern[:len(pattern)-1]
		q := qualifier(c)
		if strings.HasPrefix(prefix, q) {
			prefix = prefix[len(q):]
		} else if strings.HasPrefix(q, prefix) || strings.ContainsAny(prefix, "./") {
			return "", "", "pattern " + pattern + " is the module path, which every symbol under " + clusterDir(c, 0) + "/ carries by construction"
		}
		if prefix == "" {
			return "", "", "pattern " + pattern + " is the module path, which every symbol under " + clusterDir(c, 0) + "/ carries by construction"
		}
		return prefix, "", ""
	}
	return "", "", "pattern " + pattern + " is not a prefix or suffix"
}

func importOf(specifier string) string {
	return "import dep from \"" + strings.ReplaceAll(specifier, "\"", "\\\"") + "\";"
}

func edgeTarget(name string) string {
	if i := strings.LastIndex(name, " -> "); i >= 0 {
		return name[i+len(" -> "):]
	}
	return name
}

func jsString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `'`, `\'`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	return "'" + s + "'"
}

func renderNamingRule(c mining.Candidate) string {
	prefix, suffix, _ := localPattern(c)
	return fmt.Sprintf(`const path = require('path');

const CLUSTER = %s;
const PREFIX = %s;
const SUFFIX = %s;
const PATTERN = %s;

function underCluster(filename) {
  const rel = filename.split(path.sep).join('/');
  return rel.includes('/' + CLUSTER + '/') || rel.startsWith(CLUSTER + '/');
}

function matchesPattern(name) {
  return name.startsWith(PREFIX) && name.endsWith(SUFFIX);
}

function isTopLevel(node) {
  let parent = node.parent;
  if (parent.type === 'VariableDeclaration') {
    parent = parent.parent;
  }
  return (
    parent.type === 'Program' ||
    parent.type === 'ExportNamedDeclaration' ||
    parent.type === 'ExportDefaultDeclaration'
  );
}

module.exports = {
  meta: {
    type: 'suggestion',
    docs: {
      description: %s,
    },
    schema: [],
    messages: {
      nameOutsidePattern:
        "'{{name}}' is declared under " + CLUSTER + "/ but is not named " + PATTERN + '.',
    },
  },

  create(context) {
    const filename = context.filename ?? context.getFilename();
    if (!underCluster(filename)) {
      return {};
    }

    function check(node) {
      if (!node.id || node.id.type !== 'Identifier' || !isTopLevel(node)) {
        return;
      }
      const name = node.id.name;
      if (!matchesPattern(name)) {
        context.report({
          node: node.id,
          messageId: 'nameOutsidePattern',
          data: { name },
        });
      }
    }

    return {
      ClassDeclaration: check,
      FunctionDeclaration: check,
      VariableDeclarator: check,
    };
  },
};
`, jsString(clusterDir(c, 0)), jsString(prefix), jsString(suffix), jsString(localPatternText(prefix, suffix)), jsString(c.Statement))
}

func localPatternText(prefix, suffix string) string {
	if prefix != "" {
		return prefix + "*"
	}
	return "*" + suffix
}

func renderNamingTest(c mining.Candidate) string {
	var valid, invalid []string
	seen := map[string]bool{}
	for _, w := range c.Witnesses {
		local, _ := localName(c, w.Name)
		if seen[local] {
			continue
		}
		seen[local] = true
		valid = append(valid, fmt.Sprintf(`    {
      name: %s,
      filename: repoFile(%s),
      code: %s,
    },`, jsString(local+" conforms"), jsString(w.File), jsString("export default class "+local+" {}")))
	}
	example := exampleName(c)
	valid = append(valid, fmt.Sprintf(`    {
      name: 'a file outside %s/ is not checked',
      filename: repoFile('elsewhere/%s.js'),
      code: %s,
    },`, clusterDir(c, 0), strings.ToLower(example), jsString("export default class "+example+" {}")))
	for _, e := range c.Exceptions {
		local, _ := localName(c, e.Name)
		if seen[local] {
			continue
		}
		seen[local] = true
		invalid = append(invalid, fmt.Sprintf(`    {
      name: %s,
      filename: repoFile(%s),
      code: %s,
      errors: [{ messageId: 'nameOutsidePattern', data: { name: %s } }],
    },`, jsString(local+" is the recorded exception"), jsString(e.File), jsString("export default class "+local+" {}"), jsString(local)))
	}
	if len(invalid) == 0 {
		prefix, suffix, _ := localPattern(c)
		offender := "Outsider"
		if prefix != "" {
			offender = "Not" + prefix
		} else if suffix != "" {
			offender = suffix + "Not"
		}
		invalid = append(invalid, fmt.Sprintf(`    {
      name: 'a name outside the pattern',
      filename: repoFile(%s),
      code: %s,
      errors: [{ messageId: 'nameOutsidePattern', data: { name: %s } }],
    },`, jsString(clusterDir(c, 0)+"/offender.js"), jsString("export default class "+offender+" {}"), jsString(offender)))
	}
	return renderTest(c.Rule.ID, valid, invalid)
}

func exampleName(c mining.Candidate) string {
	if len(c.Witnesses) > 0 {
		if local, reason := localName(c, c.Witnesses[0].Name); reason == "" {
			return local
		}
	}
	return "Example"
}

func renderForbidImportRule(c mining.Candidate) string {
	return fmt.Sprintf(`const path = require('path');

const SOURCE_CLUSTER = %s;
const TARGET_CLUSTER = %s;
const PATH_ALIASES = {};

function repoRelative(filename) {
  return filename.split(path.sep).join('/');
}

function underCluster(rel, cluster) {
  return rel.includes('/' + cluster + '/') || rel.startsWith(cluster + '/');
}

function resolveSpecifier(specifier, rel) {
  for (const [alias, replacement] of Object.entries(PATH_ALIASES)) {
    if (specifier === alias || specifier.startsWith(alias + '/')) {
      return replacement + specifier.slice(alias.length);
    }
  }
  if (specifier.startsWith('.')) {
    return path.posix.normalize(path.posix.join(path.posix.dirname(rel), specifier));
  }
  return specifier;
}

function importsInto(specifier, rel, cluster) {
  const resolved = resolveSpecifier(specifier, rel);
  return resolved === cluster || underCluster(resolved, cluster);
}

module.exports = {
  meta: {
    type: 'problem',
    docs: {
      description: %s,
    },
    schema: [],
    messages: {
      forbiddenImport:
        'Files under ' + SOURCE_CLUSTER + '/ do not import from ' + TARGET_CLUSTER + "/ ('{{specifier}}').",
    },
  },

  create(context) {
    const rel = repoRelative(context.filename ?? context.getFilename());
    if (!underCluster(rel, SOURCE_CLUSTER)) {
      return {};
    }

    return {
      ImportDeclaration(node) {
        const specifier = node.source.value;
        if (importsInto(specifier, rel, TARGET_CLUSTER)) {
          context.report({
            node: node.source,
            messageId: 'forbiddenImport',
            data: { specifier },
          });
        }
      },
    };
  },
};
`, jsString(clusterDir(c, 0)), jsString(clusterDir(c, 1)), jsString(c.Statement))
}

func renderForbidImportTest(c mining.Candidate) string {
	var valid, invalid []string
	for _, w := range c.Witnesses {
		valid = append(valid, fmt.Sprintf(`    {
      name: %s,
      filename: repoFile(%s),
      code: %s,
    },`, jsString(w.File+" imports "+w.Target+", outside "+clusterDir(c, 1)), jsString(w.File), jsString(importOf(w.Target))))
	}
	valid = append(valid, fmt.Sprintf(`    {
      name: 'a file outside %s/ is not checked',
      filename: repoFile('elsewhere/file.js'),
      code: %s,
    },`, clusterDir(c, 0), jsString(importOf(clusterDir(c, 1)+"/anything"))))
	for _, e := range c.Exceptions {
		target := edgeTarget(e.Name)
		invalid = append(invalid, fmt.Sprintf(`    {
      name: %s,
      filename: repoFile(%s),
      code: %s,
      errors: [{ messageId: 'forbiddenImport', data: { specifier: %s } }],
    },`, jsString(e.File+" imports "+target+", the recorded exception"), jsString(e.File), jsString(importOf(target)), jsString(target)))
	}
	if len(invalid) == 0 {
		target := clusterDir(c, 1) + "/anything"
		invalid = append(invalid, fmt.Sprintf(`    {
      name: 'an import into %s/',
      filename: repoFile(%s),
      code: %s,
      errors: [{ messageId: 'forbiddenImport', data: { specifier: %s } }],
    },`, clusterDir(c, 1), jsString(clusterDir(c, 0)+"/offender.js"), jsString(importOf(target)), jsString(target)))
	}
	return renderTest(c.Rule.ID, valid, invalid)
}

func renderTest(ruleID string, valid, invalid []string) string {
	return fmt.Sprintf(`const path = require('path');
const { RuleTester } = require('eslint');
const rule = require('../%s');

const ruleTester = new RuleTester({
  languageOptions: {
    ecmaVersion: 2022,
    sourceType: 'module',
  },
});

function repoFile(relPath) {
  return path.join(path.sep, 'repo', relPath);
}

ruleTester.run(%s, rule, {
  valid: [
%s
  ],
  invalid: [
%s
  ],
});
`, ruleID, jsString(ruleID), strings.Join(valid, "\n"), strings.Join(invalid, "\n"))
}

func renderIndex(scaffolds []Scaffold) string {
	ids := make([]string, 0, len(scaffolds))
	for _, sc := range scaffolds {
		ids = append(ids, sc.RuleID)
	}
	sort.Strings(ids)
	var b strings.Builder
	b.WriteString("module.exports = {\n  rules: {\n")
	for _, id := range ids {
		fmt.Fprintf(&b, "    %s: require('./%s.js'),\n", jsString(id), id)
	}
	b.WriteString("  },\n};\n")
	return b.String()
}
