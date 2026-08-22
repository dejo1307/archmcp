package tsextractor

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// setupAngularProject writes an Angular workspace. angular=false writes the same
// files with no @angular/core dependency, which is the control every gate below is
// measured against.
func setupAngularProject(t *testing.T, files map[string]string, angular bool) string {
	t.Helper()
	dir := t.TempDir()

	pkgJSON := `{"dependencies": {"rxjs": "^7.0.0"}}`
	if angular {
		pkgJSON = `{"dependencies": {"@angular/core": "^19.0.0", "rxjs": "^7.0.0"}}`
	}
	for name, content := range map[string]string{
		"package.json":  pkgJSON,
		"tsconfig.json": `{}`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
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

// writeAngularWorkspace writes a workspace whose root tsconfig carries the given
// compilerOptions, for the alias cases a single-package fixture cannot express.
func writeAngularWorkspace(t *testing.T, dir, tsconfig string, files map[string]string) {
	t.Helper()
	for name, content := range map[string]string{
		"package.json":  `{"dependencies": {"@angular/core": "^19.0.0"}}`,
		"tsconfig.json": tsconfig,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
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
}

// extractDir runs the extractor over every TypeScript file under dir.
func extractDir(t *testing.T, dir string) []facts.Fact {
	t.Helper()
	var rel []string
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		r, rerr := filepath.Rel(dir, path)
		if rerr == nil && isTypeScriptFile(r) {
			rel = append(rel, filepath.ToSlash(r))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(rel)
	out, err := New().Extract(context.Background(), dir, rel)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	return out
}

func extractAngular(t *testing.T, files map[string]string, angular bool) []facts.Fact {
	t.Helper()
	dir := setupAngularProject(t, files, angular)

	relFiles := make([]string, 0, len(files))
	for f := range files {
		relFiles = append(relFiles, f)
	}
	sort.Strings(relFiles)

	result, err := New().Extract(context.Background(), dir, relFiles)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	return result
}

func symbolNamed(t *testing.T, fs []facts.Fact, name string) facts.Fact {
	t.Helper()
	for _, f := range fs {
		if f.Kind == facts.KindSymbol && f.Name == name {
			return f
		}
	}
	t.Fatalf("no symbol named %q in %d facts", name, len(fs))
	return facts.Fact{}
}

const userService = `import { Injectable } from '@angular/core';

@Injectable({ providedIn: 'root' })
export class UserService {
  load(): void {}
}
`

func TestAngularComponentIsClassified(t *testing.T) {
	fs := extractAngular(t, map[string]string{
		"src/app/user-card.component.ts": `import { Component } from '@angular/core';

@Component({
  selector: 'app-user-card',
  templateUrl: './user-card.component.html',
  standalone: true,
})
export class UserCardComponent {
  total = 0;
}
`,
	}, true)

	f := symbolNamed(t, fs, "src/app.UserCardComponent")
	for key, want := range map[string]any{
		facts.PropFramework:    AngularFramework,
		"web_component":        "component",
		"framework_registered": true,
		"angular_selector":     "app-user-card",
		"angular_standalone":   true,
		"angular_template_url": "src/app/user-card.component.html",
	} {
		if got := f.Props[key]; got != want {
			t.Errorf("props[%s] = %v, want %v", key, got, want)
		}
	}
	if _, ok := f.Props["angular_inline_template"]; ok {
		t.Error("a templateUrl component must not be marked as carrying an inline template")
	}
}

func TestAngularRolesAreDistinguished(t *testing.T) {
	fs := extractAngular(t, map[string]string{
		"src/app/thing.ts": `import { Directive, Pipe, Injectable, NgModule } from '@angular/core';

@Directive({ selector: '[appHighlight]' })
export class HighlightDirective {}

@Pipe({ name: 'truncate' })
export class TruncatePipe {}

@Injectable()
export class ThingService {}

@NgModule({})
export class ThingModule {}
`,
	}, true)

	for name, want := range map[string]string{
		"src/app.HighlightDirective": "directive",
		"src/app.TruncatePipe":       "pipe",
		"src/app.ThingService":       "service",
		"src/app.ThingModule":        "ng_module",
	} {
		f := symbolNamed(t, fs, name)
		if got := f.Props["web_component"]; got != want {
			t.Errorf("%s: web_component = %v, want %q", name, got, want)
		}
		if f.Props["framework_registered"] != true {
			t.Errorf("%s: not marked framework_registered", name)
		}
	}
	if got := symbolNamed(t, fs, "src/app.HighlightDirective").Props["angular_selector"]; got != "[appHighlight]" {
		t.Errorf("directive selector = %v", got)
	}
	if got := symbolNamed(t, fs, "src/app.TruncatePipe").Props["angular_pipe_name"]; got != "truncate" {
		t.Errorf("pipe name = %v", got)
	}
}

// The gate that keeps a decorator from being a framework: the same source in a
// repository that does not depend on @angular/core models nothing.
func TestAngularIsGatedOnTheDependency(t *testing.T) {
	files := map[string]string{
		"src/app/user-card.component.ts": `import { Component } from '@angular/core';

@Component({ selector: 'app-user-card' })
export class UserCardComponent {}
`,
	}
	f := symbolNamed(t, extractAngular(t, files, false), "src/app.UserCardComponent")
	if _, ok := f.Props["web_component"]; ok {
		t.Error("classified a component in a repository with no Angular dependency")
	}
	if got := f.Props[facts.PropFramework]; got != nil {
		t.Errorf("framework = %v in a non-Angular repository", got)
	}
}

func TestAngularConstructorInjectionResolvesThroughImports(t *testing.T) {
	fs := extractAngular(t, map[string]string{
		"src/app/services/user.service.ts": userService,
		"src/app/user-card.component.ts": `import { Component } from '@angular/core';
import { UserService } from './services/user.service';

@Component({ selector: 'app-user-card' })
export class UserCardComponent {
  constructor(private readonly users: UserService) {}
}
`,
	}, true)

	f := symbolNamed(t, fs, "src/app.UserCardComponent")
	if !hasRelation(f, facts.RelInjects, "src/app/services.UserService") {
		t.Errorf("no injects edge to the declaring module; relations: %+v", f.Relations)
	}
}

func TestAngularInjectFunctionIsRead(t *testing.T) {
	fs := extractAngular(t, map[string]string{
		"src/app/services/user.service.ts": userService,
		"src/app/user-card.component.ts": `import { Component, inject } from '@angular/core';
import { UserService } from './services/user.service';

@Component({ selector: 'app-user-card', template: '<p></p>' })
export class UserCardComponent {
  private readonly users = inject(UserService);
}
`,
	}, true)

	f := symbolNamed(t, fs, "src/app.UserCardComponent")
	if !hasRelation(f, facts.RelInjects, "src/app/services.UserService") {
		t.Errorf("inject() field not read; relations: %+v", f.Relations)
	}
	if f.Props["angular_inline_template"] != true {
		t.Error("an inline template: was not recorded")
	}
}

// A collaborator declared beside its consumer needs no import to resolve.
func TestAngularInjectionResolvesALocalClass(t *testing.T) {
	fs := extractAngular(t, map[string]string{
		"src/app/thing.ts": `import { Component, Injectable } from '@angular/core';

@Injectable()
export class LocalService {}

@Component({ selector: 'app-thing' })
export class ThingComponent {
  constructor(private local: LocalService) {}
}
`,
	}, true)

	if f := symbolNamed(t, fs, "src/app.ThingComponent"); !hasRelation(f, facts.RelInjects, "src/app.LocalService") {
		t.Errorf("local class not resolved; relations: %+v", f.Relations)
	}
}

// The rule the package exists to hold: an unresolvable token produces no edge, and
// is counted instead.
func TestAngularUnresolvedInjectionIsCountedNotGuessed(t *testing.T) {
	fs := extractAngular(t, map[string]string{
		"src/app/thing.ts": `import { Component, Inject } from '@angular/core';
import { HttpClient } from '@angular/common/http';

@Component({ selector: 'app-thing' })
export class ThingComponent {
  constructor(private http: HttpClient, @Inject(APP_CONFIG) private cfg: unknown) {}
}
`,
	}, true)

	f := symbolNamed(t, fs, "src/app.ThingComponent")
	for _, r := range f.Relations {
		if r.Kind == facts.RelInjects {
			t.Errorf("guessed an injects edge to %q", r.Target)
		}
	}

	var cov *facts.Fact
	for i := range fs {
		if fs[i].Kind == facts.KindExtraction && fs[i].Name == "typescript:angular-di" {
			cov = &fs[i]
		}
	}
	if cov == nil {
		t.Fatal("no typescript:angular-di coverage fact; an unresolved site must still be reported")
	}
	got, ok := cov.Props["unresolved_macros"].(string)
	if !ok || got == "" {
		t.Errorf("coverage fact names no cause: %+v", cov.Props)
	}
	// Both causes are read off the source, not guessed from the identifier: the
	// HttpClient came from a package, the token did not.
	if !strings.Contains(got, "external_package=1") {
		t.Errorf("a type imported from a package was not counted as external: %q", got)
	}
	if !strings.Contains(got, "injection_token=1") {
		t.Errorf("an injection token was not counted as one: %q", got)
	}
}

// Angular 19 flipped the default, so absence is not a value: a component that does
// not state `standalone` must not be recorded either way.
func TestAngularStandaloneIsRecordedOnlyWhenStated(t *testing.T) {
	fs := extractAngular(t, map[string]string{
		"src/app/a.component.ts": `import { Component } from '@angular/core';

@Component({ selector: 'app-a' })
export class AComponent {}
`,
		"src/app/b.component.ts": `import { Component } from '@angular/core';

@Component({ selector: 'app-b', standalone: false })
export class BComponent {}
`,
	}, true)

	if _, ok := symbolNamed(t, fs, "src/app.AComponent").Props["angular_standalone"]; ok {
		t.Error("recorded standalone for a component that does not state it")
	}
	if got := symbolNamed(t, fs, "src/app.BComponent").Props["angular_standalone"]; got != false {
		t.Errorf("standalone: false = %v, want false", got)
	}
}

// An Angular application nested inside a backend repository: the shape that made
// every Ember gate silently produce nothing until detectEmber learned to search.
func TestAngularDetectionFindsANestedApplication(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"dependencies":{"express":"^4.0.0"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "frontend"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "frontend", "package.json"), []byte(`{"dependencies":{"@angular/core":"^19.0.0"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if !detectAngular(dir) {
		t.Error("an Angular application one level down was not detected")
	}
}

// A barrel import must name the directory that declares the symbol, not the
// barrel's parent — the shape that made 76% of one corpus repository's injection
// edges point at a node that does not exist.
func TestAngularInjectionResolvesThroughABarrel(t *testing.T) {
	fs := extractAngular(t, map[string]string{
		"src/app/core/services/user.service.ts": userService,
		"src/app/core/services/index.ts":        `export { UserService } from './user.service';`,
		"src/app/widgets/user-card.component.ts": `import { Component } from '@angular/core';
import { UserService } from '../core/services';

@Component({ selector: 'app-user-card' })
export class UserCardComponent {
  constructor(private users: UserService) {}
}
`,
	}, true)

	f := symbolNamed(t, fs, "src/app/widgets.UserCardComponent")
	if !hasRelation(f, facts.RelInjects, "src/app/core/services.UserService") {
		t.Errorf("barrel import did not resolve to the declaring module; relations: %+v", f.Relations)
	}
	for _, r := range f.Relations {
		if r.Kind == facts.RelInjects && r.Target == "src/app/core.UserService" {
			t.Error("edge names the barrel's PARENT directory")
		}
	}
}

// Every injects edge must name a symbol the snapshot holds. An edge that survives
// resolution but matches no declaration is removed rather than shipped.
func TestAngularInjectsEdgesNeverDangle(t *testing.T) {
	fs := extractAngular(t, map[string]string{
		"src/app/core/services/user.service.ts": userService,
		"src/app/core/services/index.ts":        `export { UserService } from './user.service';`,
		"src/app/core/index.ts":                 `export * from './services';`,
		"src/app/widgets/user-card.component.ts": `import { Component } from '@angular/core';
import { UserService } from '../core';

@Component({ selector: 'app-user-card' })
export class UserCardComponent {
  constructor(private users: UserService) {}
}
`,
	}, true)

	declared := map[string]bool{}
	for _, f := range fs {
		if f.Kind == facts.KindSymbol {
			declared[f.Name] = true
		}
	}
	for _, f := range fs {
		for _, r := range f.Relations {
			if r.Kind == facts.RelInjects && !declared[r.Target] {
				t.Errorf("%s has a dangling injects edge to %q", f.Name, r.Target)
			}
		}
	}
}

// An @Inject(TOKEN) parameter names the token, not the type beside it — the
// annotation on such a parameter is routinely `unknown` and names nothing.
func TestAngularInjectTokenParameterIsRead(t *testing.T) {
	fs := extractAngular(t, map[string]string{
		"src/app/tokens.ts": `import { InjectionToken } from '@angular/core';

export const APP_CONFIG = new InjectionToken<string>('app.config');
`,
		"src/app/thing.ts": `import { Component, Inject } from '@angular/core';
import { APP_CONFIG } from './tokens';

@Component({ selector: 'app-thing' })
export class ThingComponent {
  constructor(@Inject(APP_CONFIG) private cfg: unknown) {}
}
`,
	}, true)

	if f := symbolNamed(t, fs, "src/app.ThingComponent"); !hasRelation(f, facts.RelInjects, "src/app.APP_CONFIG") {
		t.Errorf("the injected token produced no edge; relations: %+v", f.Relations)
	}
}
