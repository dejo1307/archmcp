package tsextractor

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

func setupEmberProject(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "tsconfig.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	pkgJSON := `{"devDependencies": {"ember-source": "^6.0.0"}}`
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkgJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	for relPath, content := range files {
		absPath := filepath.Join(dir, relPath)
		if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(absPath, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func extractEmber(t *testing.T, files map[string]string) []facts.Fact {
	t.Helper()
	dir := setupEmberProject(t, files)
	var relFiles []string
	for f := range files {
		relFiles = append(relFiles, f)
	}
	ext := New()
	result, err := ext.Extract(context.Background(), dir, relFiles)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	return result
}

func findEmberFact(ff []facts.Fact, kind, name string) *facts.Fact {
	for i := range ff {
		if ff[i].Kind == kind && ff[i].Name == name {
			return &ff[i]
		}
	}
	return nil
}

// --- template blanking ---

func TestBlankEmberTemplates_PreservesLinesAndBytes(t *testing.T) {
	src := []byte("import X from './x';\n<template>\n  <X />\n</template>\nconst a = 1;\n")
	blanked, segments := blankEmberTemplates(src)
	if len(blanked) != len(src) {
		t.Fatalf("blanked length %d, want %d", len(blanked), len(src))
	}
	if strings.Count(string(blanked), "\n") != strings.Count(string(src), "\n") {
		t.Fatal("newline count changed")
	}
	if strings.Contains(string(blanked), "template") {
		t.Fatal("template tags survived blanking")
	}
	if !strings.Contains(string(blanked), "import X from './x';") {
		t.Fatal("code outside the template block was disturbed")
	}
	if len(segments) != 1 || !strings.Contains(segments[0].Content, "<X />") {
		t.Fatalf("segments = %+v, want one segment containing the invocation", segments)
	}
}

func TestBlankEmberTemplates_UnclosedLeftAlone(t *testing.T) {
	src := []byte("const a = 1;\n<template>\nnever closed\n")
	blanked, segments := blankEmberTemplates(src)
	if string(blanked) != string(src) {
		t.Fatal("unclosed template block was modified")
	}
	if len(segments) != 0 {
		t.Fatal("unclosed template block produced a segment")
	}
}

// --- .gts class components ---

func TestEmberTemplateTag_ClassComponent(t *testing.T) {
	result := extractEmber(t, map[string]string{
		"app/components/badge.ts": `import Component from '@glimmer/component';
export default class Badge extends Component {}
`,
		"app/components/user-card.gts": `import Component from '@glimmer/component';
import Badge from '../components/badge';

export default class UserCard extends Component {
  get label() {
    return 'hi';
  }

  <template>
    <Badge @label={{this.label}} />
  </template>
}
`,
	})

	card := findEmberFact(result, facts.KindSymbol, "app/components.UserCard")
	if card == nil {
		t.Fatal("UserCard symbol missing — .gts file was not parsed")
	}
	if card.Props["web_component"] != "component" || card.Props["framework"] != EmberFramework {
		t.Errorf("UserCard props = %v, want component/ember classification", card.Props)
	}
	if card.Line != 4 {
		t.Errorf("UserCard line = %d, want 4 (line numbers must survive blanking)", card.Line)
	}
	if !card.HasRelation(facts.RelCalls, "app/components.Badge") {
		t.Errorf("UserCard relations = %v, want template-scope calls edge to Badge", card.Relations)
	}
	if m := findEmberFact(result, facts.KindSymbol, "app/components.UserCard.label"); m == nil || m.Line != 5 {
		t.Errorf("method fact = %+v, want label method at line 5", m)
	}
}

func TestEmberTemplateTag_TemplateOnlyComponent(t *testing.T) {
	result := extractEmber(t, map[string]string{
		"app/components/icon.ts": `export default class Icon {}
`,
		"app/components/hello.gjs": `import Icon from './icon';

<template>
  <Icon @name="wave" />
</template>
`,
	})
	hello := findEmberFact(result, facts.KindSymbol, "app/components.Hello")
	if hello == nil {
		t.Fatal("template-only .gjs did not synthesize a component symbol")
	}
	if hello.Props["web_component"] != "component" {
		t.Errorf("props = %v, want component classification", hello.Props)
	}
	if !hello.HasRelation(facts.RelCalls, "app/components.Icon") {
		t.Errorf("relations = %v, want calls edge to Icon", hello.Relations)
	}
}

// --- services ---

func TestEmberServiceInjection_RecordedForBinder(t *testing.T) {
	result := extractEmber(t, map[string]string{
		"app/services/session.ts": `import Service from '@ember/service';
export default class Session extends Service {}
`,
		"app/components/toolbar.ts": `import Component from '@glimmer/component';
import { service } from '@ember/service';

export default class Toolbar extends Component {
  @service declare session: unknown;
  @service('analytics/tracker') tracker;
  @service declare currentUser: unknown;
}
`,
	})

	toolbar := findEmberFact(result, facts.KindSymbol, "app/components.Toolbar")
	if toolbar == nil {
		t.Fatal("Toolbar symbol missing")
	}
	got, _ := toolbar.Props[EmberServicesProp].([]string)
	want := []string{"analytics/tracker", "current-user", "session"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s = %v, want %v", EmberServicesProp, got, want)
	}

	session := findEmberFact(result, facts.KindSymbol, "app/services.Session")
	if session == nil || session.Props["ember_service"] != "session" {
		t.Errorf("Session service fact = %+v, want ember_service prop", session)
	}
}

// --- router map ---

func TestEmberRouterMap_ComposesNestedPaths(t *testing.T) {
	result := extractEmber(t, map[string]string{
		"app/router.ts": `import EmberRouter from '@ember/routing/router';

export default class Router extends EmberRouter {}

Router.map(function () {
  this.route('login');
  this.route('jobs', function () {
    this.route('job', { path: '/:job_id' }, function () {
      this.route('activity');
    });
  });
  this.route('settings', { path: '/preferences' });
});
`,
	})

	cases := map[string]string{
		"/login":                 "login",
		"/jobs":                  "jobs",
		"/jobs/:job_id":          "jobs.job",
		"/jobs/:job_id/activity": "jobs.job.activity",
		"/preferences":           "settings",
	}
	for path, routeName := range cases {
		r := findEmberFact(result, facts.KindRoute, path)
		if r == nil {
			t.Errorf("route %q missing", path)
			continue
		}
		if r.Props["framework"] != EmberFramework || r.Props["type"] != "page" {
			t.Errorf("route %q props = %v, want ember page route", path, r.Props)
		}
		if r.Props["ember_route_name"] != routeName {
			t.Errorf("route %q name = %v, want %q", path, r.Props["ember_route_name"], routeName)
		}
	}
}

// --- ember-data models ---

func TestEmberDataModel_StorageCompanion(t *testing.T) {
	result := extractEmber(t, map[string]string{
		"app/models/job-application.ts": `import Model from '@ember-data/model';
export default class JobApplicationModel extends Model {}
`,
	})
	sf := findEmberFact(result, facts.KindStorage, "app/models.JobApplicationModel")
	if sf == nil {
		t.Fatal("ember-data model produced no storage fact")
	}
	if sf.Props["storage_kind"] != "model" || sf.Props["framework"] != "ember-data" {
		t.Errorf("storage props = %v", sf.Props)
	}
	if sf.Props["table"] != "job-application" {
		t.Errorf("table = %v, want the dasherized model name job-application", sf.Props["table"])
	}
	if findEmberFact(result, facts.KindSymbol, "app/models.JobApplicationModel") == nil {
		t.Error("model class must keep its own symbol fact beside the storage companion")
	}
}

func TestEmberDataModel_RequiresEmberDataSuperclass(t *testing.T) {
	result := extractEmber(t, map[string]string{
		"app/models/plain.ts": `export default class Plain {}
`,
	})
	if sf := findEmberFact(result, facts.KindStorage, "app/models.Plain"); sf != nil {
		t.Fatalf("plain class produced a storage fact: %+v", sf)
	}
}

// --- .hbs templates ---

func TestEmberHbs_InvocationScanAndOwner(t *testing.T) {
	result := extractEmber(t, map[string]string{
		"app/components/star-ratings.hbs": `<div>
  {{#if @editable}}
    <Ui::Button @label={{format-date @date}} />
  {{/if}}
  {{star-icon}}
  {{title}}
</div>
`,
		"app/components/star-ratings.js": `import Component from '@glimmer/component';
export default class StarRatings extends Component {}
`,
	})

	ref := findEmberFact(result, facts.KindFileRef, "app/components/star-ratings.hbs")
	if ref == nil {
		t.Fatal(".hbs emitted no file_ref carrier")
	}
	if ref.Props[EmberOwnerFileProp] != "app/components/star-ratings.js" {
		t.Errorf("owner = %v, want the co-located class file", ref.Props[EmberOwnerFileProp])
	}
	got, _ := ref.Props[EmberInvocationsProp].([]string)
	want := []string{"Ui::Button", "format-date", "star-icon"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("invocations = %v, want %v (keywords and bare properties excluded)", got, want)
	}
	if findEmberFact(result, facts.KindSymbol, "app/components.StarRatings") == nil {
		t.Error("co-located class symbol missing")
	}
}

func TestEmberHbs_TemplateOnlyComponentSynthesized(t *testing.T) {
	result := extractEmber(t, map[string]string{
		"app/components/wysiwyg-editor.hbs": `<div class="editor">{{yield}}</div>
`,
	})
	sym := findEmberFact(result, facts.KindSymbol, "app/components.WysiwygEditor")
	if sym == nil {
		t.Fatal("template-only .hbs component did not synthesize a symbol")
	}
	if sym.Props["web_component"] != "component" || sym.Props["framework"] != EmberFramework {
		t.Errorf("props = %v", sym.Props)
	}
}

func TestEmberHbs_IgnoredOutsideEmberRepos(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "tsconfig.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"dependencies":{"typescript":"^5"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	hbs := filepath.Join(dir, "emails", "welcome.hbs")
	if err := os.MkdirAll(filepath.Dir(hbs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hbs, []byte(`{{name}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	ext := New()
	result, err := ext.Extract(context.Background(), dir, []string{"emails/welcome.hbs"})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	for _, f := range result {
		if f.File == "emails/welcome.hbs" {
			t.Fatalf("non-Ember repo modeled an .hbs file: %+v", f)
		}
	}
}

// --- helpers ---

func TestDasherize(t *testing.T) {
	cases := map[string]string{
		"CurrentUser": "current-user", "session": "session",
		"AboardApollo": "aboard-apollo", "a": "a",
	}
	for in, want := range cases {
		if got := dasherize(in); got != want {
			t.Errorf("dasherize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestScanHbsInvocations_DeterminismLine(t *testing.T) {
	got := scanHbsInvocations(`{{#each items as |item|}}
  <ItemRow @item={{item}} />
  {{format-currency item.price}}
  {{title}}
  {{#link-to "index"}}home{{/link-to}}
  {{if condition "a" "b"}}
{{/each}}`)
	want := []string{"ItemRow", "format-currency"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("scanHbsInvocations = %v, want %v", got, want)
	}
}
