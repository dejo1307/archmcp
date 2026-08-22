package tsextractor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

func TestAngularNgModuleComposesItsDeclarations(t *testing.T) {
	fs := extractAngular(t, map[string]string{
		"src/app/card.component.ts": cardComponent,
		"src/app/user.service.ts":   userService,
		"src/app/shared.module.ts": `import { NgModule } from '@angular/core';
import { CardComponent } from './card.component';
import { UserService } from './user.service';

@NgModule({
  declarations: [CardComponent],
  exports: [CardComponent],
  providers: [UserService],
})
export class SharedModule {}
`,
	}, true)

	f := symbolNamed(t, fs, "src/app.SharedModule")
	for _, want := range []string{"src/app.CardComponent", "src/app.UserService"} {
		if !hasRelation(f, facts.RelDependsOn, want) {
			t.Errorf("no composition edge to %s; relations: %+v", want, f.Relations)
		}
	}
	if got := f.PropString("angular_declarations"); got != "CardComponent" {
		t.Errorf("angular_declarations = %q", got)
	}
	if got := f.PropString("angular_providers"); got != "UserService" {
		t.Errorf("angular_providers = %q", got)
	}
}

// `RouterModule.forRoot(routes)` in an imports array is a dependency on
// RouterModule; the call is how it is configured, not what it names.
func TestAngularModuleCallImportNamesItsReceiver(t *testing.T) {
	fs := extractAngular(t, map[string]string{
		"src/app/feature.module.ts": `import { NgModule } from '@angular/core';
import { SharedModule } from './shared.module';

@NgModule({ imports: [SharedModule.forRoot()] })
export class FeatureModule {}
`,
		"src/app/shared.module.ts": `import { NgModule } from '@angular/core';

@NgModule({})
export class SharedModule {}
`,
	}, true)

	if f := symbolNamed(t, fs, "src/app.FeatureModule"); !hasRelation(f, facts.RelDependsOn, "src/app.SharedModule") {
		t.Errorf("a configured module import named nothing; relations: %+v", f.Relations)
	}
}

// A provider literal names a token and an implementation, and the module registers
// both.
func TestAngularProviderLiteralNamesBothHalves(t *testing.T) {
	fs := extractAngular(t, map[string]string{
		"src/app/user.service.ts": userService,
		"src/app/tokens.ts": `import { InjectionToken } from '@angular/core';

export const USER_SERVICE = new InjectionToken<string>('user');
`,
		"src/app/app.module.ts": `import { NgModule } from '@angular/core';
import { USER_SERVICE } from './tokens';
import { UserService } from './user.service';

@NgModule({ providers: [{ provide: USER_SERVICE, useClass: UserService }] })
export class AppModule {}
`,
	}, true)

	f := symbolNamed(t, fs, "src/app.AppModule")
	for _, want := range []string{"src/app.USER_SERVICE", "src/app.UserService"} {
		if !hasRelation(f, facts.RelDependsOn, want) {
			t.Errorf("provider literal lost %s; relations: %+v", want, f.Relations)
		}
	}
}

// A standalone component composes the same way, one level down.
func TestAngularStandaloneComponentComposesItsImports(t *testing.T) {
	fs := extractAngular(t, map[string]string{
		"src/app/card.component.ts": cardComponent,
		"src/app/page.component.ts": `import { Component } from '@angular/core';
import { CardComponent } from './card.component';

@Component({
  selector: 'app-page',
  standalone: true,
  imports: [CardComponent],
  template: '<app-card></app-card>',
})
export class PageComponent {}
`,
	}, true)

	if f := symbolNamed(t, fs, "src/app.PageComponent"); !hasRelation(f, facts.RelDependsOn, "src/app.CardComponent") {
		t.Errorf("standalone imports produced no edge; relations: %+v", f.Relations)
	}
}

// Every composition edge must name a symbol the snapshot holds — the same rule the
// injection edges are held to, and for the same reason.
func TestAngularModuleEdgesNeverDangle(t *testing.T) {
	fs := extractAngular(t, map[string]string{
		"src/app/card.component.ts": cardComponent,
		"src/app/shared/index.ts":   `export { CardComponent } from '../card.component';`,
		"src/app/app.module.ts": `import { NgModule } from '@angular/core';
import { CardComponent } from './shared';

@NgModule({ declarations: [CardComponent] })
export class AppModule {}
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
			if r.Kind == facts.RelDependsOn && !declared[r.Target] {
				t.Errorf("%s has a dangling composition edge to %q", f.Name, r.Target)
			}
		}
	}
}

// A workspace states which project owns a directory. Inferring it from the path is
// what every reading that groups by unit was doing instead.
func TestAngularWorkspaceProjectIsRead(t *testing.T) {
	dir := t.TempDir()
	writeAngularWorkspace(t, dir, `{"compilerOptions":{}}`, map[string]string{
		"angular.json": `{"projects":{"web":{"root":"apps/web"}}}`,
		"apps/web/src/app.component.ts": `import { Component } from '@angular/core';
@Component({ selector: 'app-root', template: '<i></i>' })
export class AppComponent {}
`,
		"libs/billing/project.json": `{"name":"billing"}`,
		"libs/billing/src/invoice.service.ts": `import { Injectable } from '@angular/core';
@Injectable()
export class InvoiceService {}
`,
	})
	fs := extractDir(t, dir)

	want := map[string]string{
		"apps/web/src":     "web",
		"libs/billing/src": "billing",
	}
	for _, f := range fs {
		if f.Kind != facts.KindModule {
			continue
		}
		if project, ok := want[f.Name]; ok {
			if got := f.PropString("workspace_project"); got != project {
				t.Errorf("module %s: workspace_project = %q, want %q", f.Name, got, project)
			}
			delete(want, f.Name)
		}
	}
	for name := range want {
		t.Errorf("no module fact for %s", name)
	}
}

// A .json workspace file in a repository with no Angular dependency states nothing
// this extractor reads.
func TestAngularWorkspaceProjectIsGated(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"dependencies":{"react":"^18.0.0"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "angular.json"), []byte(`{"projects":{"web":{"root":"src"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "a.ts"), []byte("export const a = 1;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, f := range extractDir(t, dir) {
		if f.Kind == facts.KindModule && f.PropString("workspace_project") != "" {
			t.Errorf("read a workspace project in a non-Angular repository: %s", f.PropString("workspace_project"))
		}
	}
}
