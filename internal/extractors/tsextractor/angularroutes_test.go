package tsextractor

import (
	"sort"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

func routePaths(fs []facts.Fact) []string {
	var out []string
	for _, f := range fs {
		if f.Kind == facts.KindRoute && f.PropString(facts.PropFramework) == AngularFramework {
			out = append(out, f.Name)
		}
	}
	sort.Strings(out)
	return out
}

func findRoute(fs []facts.Fact, path string) (facts.Fact, bool) {
	for _, f := range fs {
		if f.Kind == facts.KindRoute && f.Name == path && f.PropString(facts.PropFramework) == AngularFramework {
			return f, true
		}
	}
	return facts.Fact{}, false
}

func routeNamed(t *testing.T, fs []facts.Fact, path string) facts.Fact {
	t.Helper()
	for _, f := range fs {
		if f.Kind == facts.KindRoute && f.Name == path && f.PropString(facts.PropFramework) == AngularFramework {
			return f
		}
	}
	t.Fatalf("no Angular route %q; got %v", path, routePaths(fs))
	return facts.Fact{}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestAngularForRootRoutesCompose(t *testing.T) {
	fs := extractAngular(t, map[string]string{
		"src/app/home.component.ts": `import { Component } from '@angular/core';
@Component({ selector: 'app-home' })
export class HomeComponent {}
`,
		"src/app/detail.component.ts": `import { Component } from '@angular/core';
@Component({ selector: 'app-detail' })
export class DetailComponent {}
`,
		"src/app/app-routing.module.ts": `import { NgModule } from '@angular/core';
import { RouterModule, Routes } from '@angular/router';
import { HomeComponent } from './home.component';
import { DetailComponent } from './detail.component';

const routes: Routes = [
  { path: '', component: HomeComponent },
  { path: 'items', component: HomeComponent, children: [
    { path: ':id', component: DetailComponent },
  ]},
];

@NgModule({ imports: [RouterModule.forRoot(routes)] })
export class AppRoutingModule {}
`,
	}, true)

	want := []string{"/", "/items", "/items/:id"}
	if got := routePaths(fs); !equalStrings(got, want) {
		t.Fatalf("routes = %v, want %v", got, want)
	}
	f := routeNamed(t, fs, "/items/:id")
	if f.PropString("type") != "page" {
		t.Errorf("type = %q, want page — a page route must never match an HTTP endpoint", f.PropString("type"))
	}
	if f.PropString(facts.PropSource) != facts.RouteSourceAngularRouter {
		t.Errorf("source = %q", f.PropString(facts.PropSource))
	}
	if !hasRelation(f, facts.RelHandledBy, "src/app.DetailComponent") {
		t.Errorf("no handled_by edge to the component; relations: %+v", f.Relations)
	}
}

// The cross-file half: a lazy module's routes live in another file, and their path
// is decided by the entry that loads them.
func TestAngularLazyLoadChildrenComposesThePrefix(t *testing.T) {
	fs := extractAngular(t, map[string]string{
		"src/app/admin/users.component.ts": `import { Component } from '@angular/core';
@Component({ selector: 'app-users' })
export class UsersComponent {}
`,
		"src/app/admin/admin-routing.module.ts": `import { NgModule } from '@angular/core';
import { RouterModule, Routes } from '@angular/router';
import { UsersComponent } from './users.component';

const routes: Routes = [
  { path: 'users', component: UsersComponent },
];

@NgModule({ imports: [RouterModule.forChild(routes)] })
export class AdminRoutingModule {}
`,
		"src/app/admin/admin.module.ts": `import { NgModule } from '@angular/core';
import { AdminRoutingModule } from './admin-routing.module';

@NgModule({ imports: [AdminRoutingModule] })
export class AdminModule {}
`,
		"src/app/app-routing.module.ts": `import { NgModule } from '@angular/core';
import { RouterModule, Routes } from '@angular/router';

const routes: Routes = [
  { path: 'admin', loadChildren: () => import('./admin/admin.module').then(m => m.AdminModule) },
];

@NgModule({ imports: [RouterModule.forRoot(routes)] })
export class AppRoutingModule {}
`,
	}, true)

	f := routeNamed(t, fs, "/admin/users")
	if f.Props["mount_composed"] != true {
		t.Error("a route whose prefix came from another file must say so")
	}
	if f.File != "src/app/admin/admin-routing.module.ts" {
		t.Errorf("route attributed to %s, want the file that declares it", f.File)
	}
}

// The standalone dialect: provideRouter over a route const in another file, with
// loadComponent leaves.
func TestAngularProvideRouterAndLoadComponent(t *testing.T) {
	fs := extractAngular(t, map[string]string{
		"src/app/dashboard/dashboard.component.ts": `import { Component } from '@angular/core';
@Component({ selector: 'app-dashboard', standalone: true })
export class DashboardComponent {}
`,
		"src/app/app.routes.ts": `import { Routes } from '@angular/router';

export const routes: Routes = [
  { path: 'dashboard', loadComponent: () => import('./dashboard/dashboard.component').then(m => m.DashboardComponent) },
];
`,
		"src/app/app.config.ts": `import { ApplicationConfig } from '@angular/core';
import { provideRouter } from '@angular/router';
import { routes } from './app.routes';

export const appConfig: ApplicationConfig = { providers: [provideRouter(routes)] };
`,
	}, true)

	f := routeNamed(t, fs, "/dashboard")
	if !hasRelation(f, facts.RelHandledBy, "src/app/dashboard.DashboardComponent") {
		t.Errorf("loadComponent did not resolve; relations: %+v", f.Relations)
	}
}

// A library declares forChild and nothing mounts it. Emitting its fragments would
// be a wrong path rather than a missing one.
func TestAngularUnmountedRoutesEmitNothing(t *testing.T) {
	fs := extractAngular(t, map[string]string{
		"src/lib/thing.component.ts": `import { Component } from '@angular/core';
@Component({ selector: 'lib-thing' })
export class ThingComponent {}
`,
		"src/lib/lib-routing.module.ts": `import { NgModule } from '@angular/core';
import { RouterModule, Routes } from '@angular/router';
import { ThingComponent } from './thing.component';

const routes: Routes = [{ path: 'thing', component: ThingComponent }];

@NgModule({ imports: [RouterModule.forChild(routes)] })
export class LibRoutingModule {}
`,
	}, true)

	if got := routePaths(fs); len(got) != 0 {
		t.Errorf("emitted %v for routes nothing mounts", got)
	}
}

func TestAngularGuardsBecomeEdges(t *testing.T) {
	fs := extractAngular(t, map[string]string{
		"src/app/auth.guard.ts": `import { Injectable } from '@angular/core';
@Injectable()
export class AuthGuard {}
`,
		"src/app/secret.component.ts": `import { Component } from '@angular/core';
@Component({ selector: 'app-secret' })
export class SecretComponent {}
`,
		"src/app/app-routing.module.ts": `import { NgModule } from '@angular/core';
import { RouterModule, Routes } from '@angular/router';
import { AuthGuard } from './auth.guard';
import { SecretComponent } from './secret.component';

const routes: Routes = [
  { path: 'secret', component: SecretComponent, canActivate: [AuthGuard] },
];

@NgModule({ imports: [RouterModule.forRoot(routes)] })
export class AppRoutingModule {}
`,
	}, true)

	if f := routeNamed(t, fs, "/secret"); !hasRelation(f, facts.RelDependsOn, "src/app.AuthGuard") {
		t.Errorf("no edge to the guard; relations: %+v", f.Relations)
	}
}

// A pure redirect configures the router without being a page. It is counted, not
// emitted, so the coverage number still accounts for it.
func TestAngularRedirectIsCountedNotEmitted(t *testing.T) {
	fs := extractAngular(t, map[string]string{
		"src/app/home.component.ts": `import { Component } from '@angular/core';
@Component({ selector: 'app-home' })
export class HomeComponent {}
`,
		"src/app/app-routing.module.ts": `import { NgModule } from '@angular/core';
import { RouterModule, Routes } from '@angular/router';
import { HomeComponent } from './home.component';

const routes: Routes = [
  { path: '', redirectTo: 'home', pathMatch: 'full' },
  { path: 'home', component: HomeComponent },
];

@NgModule({ imports: [RouterModule.forRoot(routes)] })
export class AppRoutingModule {}
`,
	}, true)

	if got := routePaths(fs); !equalStrings(got, []string{"/home"}) {
		t.Errorf("routes = %v, want only /home", got)
	}
	var found bool
	for _, f := range fs {
		if f.Kind == facts.KindExtraction && f.Name == "typescript:angular-routes" {
			found = true
			if got, _ := f.Props["unresolved_macros"].(string); got != "redirect_or_config=1" {
				t.Errorf("unresolved causes = %q", got)
			}
		}
	}
	if !found {
		t.Error("no typescript:angular-routes coverage fact")
	}
}

// A route array reached only through a repository this snapshot does not hold, or
// through a dynamic import, stays silent and is counted.
func TestAngularUnresolvableLazyTargetIsCounted(t *testing.T) {
	fs := extractAngular(t, map[string]string{
		"src/app/app-routing.module.ts": `import { NgModule } from '@angular/core';
import { RouterModule, Routes } from '@angular/router';

const routes: Routes = [
  { path: 'plugin', loadChildren: () => import('@acme/plugin').then(m => m.PluginModule) },
];

@NgModule({ imports: [RouterModule.forRoot(routes)] })
export class AppRoutingModule {}
`,
	}, true)

	if got := routePaths(fs); len(got) != 0 {
		t.Errorf("emitted %v for a module outside the snapshot", got)
	}
}

// A test file's router configures a fixture, not the application.
func TestAngularTestRoutersAreNotExtracted(t *testing.T) {
	fs := extractAngular(t, map[string]string{
		"src/app/thing.component.ts": `import { Component } from '@angular/core';
@Component({ selector: 'app-thing' })
export class ThingComponent {}
`,
		"src/app/thing.component.spec.ts": `import { RouterModule, Routes } from '@angular/router';
import { ThingComponent } from './thing.component';

const routes: Routes = [{ path: 'fixture', component: ThingComponent }];
RouterModule.forRoot(routes);
`,
	}, true)

	if got := routePaths(fs); len(got) != 0 {
		t.Errorf("a spec file's router produced %v", got)
	}
}

// A lazy mount nested inside a `children:` array resolves exactly as one at the
// top level — the two paths through the walk must not diverge.
func TestAngularNestedLazyMountResolves(t *testing.T) {
	fs := extractAngular(t, map[string]string{
		"src/app/settings/profile.component.ts": `import { Component } from '@angular/core';
@Component({ selector: 'app-profile' })
export class ProfileComponent {}
`,
		"src/app/settings/settings.routes.ts": `import { Routes } from '@angular/router';
import { ProfileComponent } from './profile.component';

export const settingsRoutes: Routes = [{ path: 'profile', component: ProfileComponent }];
`,
		"src/app/shell.component.ts": `import { Component } from '@angular/core';
@Component({ selector: 'app-shell' })
export class ShellComponent {}
`,
		"src/app/app-routing.module.ts": `import { NgModule } from '@angular/core';
import { RouterModule, Routes } from '@angular/router';
import { ShellComponent } from './shell.component';

const routes: Routes = [
  { path: 'app', component: ShellComponent, children: [
    { path: 'settings', loadChildren: () => import('./settings/settings.routes').then(m => m.settingsRoutes) },
  ]},
];

@NgModule({ imports: [RouterModule.forRoot(routes)] })
export class AppRoutingModule {}
`,
	}, true)

	if _, ok := findRoute(fs, "/app/settings/profile"); !ok {
		t.Errorf("nested lazy mount did not compose; got %v", routePaths(fs))
	}
}

// Routes supplied at runtime through a ROUTES provider are not written down
// anywhere to read. The placeholder array is empty, and saying so by name is the
// difference between that and a route the extractor failed on.
func TestAngularRuntimeRouteProviderIsNamed(t *testing.T) {
	fs := extractAngular(t, map[string]string{
		"src/app/pages/pages-routing.module.ts": `import { NgModule } from '@angular/core';
import { ROUTES, RouterModule } from '@angular/router';
import { buildRoutes } from './pages.routes';

@NgModule({
  imports: [RouterModule.forChild([])],
  providers: [{ provide: ROUTES, useFactory: buildRoutes, multi: true }],
})
export class PagesRoutingModule {}
`,
		"src/app/app-routing.module.ts": `import { NgModule } from '@angular/core';
import { RouterModule, Routes } from '@angular/router';

const routes: Routes = [
  { path: 'pages', loadChildren: () => import('./pages/pages-routing.module').then(m => m.PagesRoutingModule) },
];

@NgModule({ imports: [RouterModule.forRoot(routes)] })
export class AppRoutingModule {}
`,
	}, true)

	if got := routePaths(fs); len(got) != 0 {
		t.Errorf("emitted %v for routes built at runtime", got)
	}
	for _, f := range fs {
		if f.Kind == facts.KindExtraction && f.Name == "typescript:angular-routes" {
			if got, _ := f.Props["unresolved_macros"].(string); got != "runtime_route_provider=1" {
				t.Errorf("unresolved causes = %q, want runtime_route_provider=1", got)
			}
			return
		}
	}
	t.Error("no typescript:angular-routes coverage fact")
}

// `export default [ … ] as Routes`, reached by `import('./routes')` with no
// `.then(m => m.X)`. One real application spells every lazy mount this way, and it
// contributed one route out of 57 until the wrapper was unwrapped.
func TestAngularDefaultExportLazyRoutesResolve(t *testing.T) {
	fs := extractAngular(t, map[string]string{
		"src/app/+admin/users.component.ts": `import { Component } from '@angular/core';
@Component({ selector: 'app-users' })
export class UsersComponent {}
`,
		"src/app/+admin/routes.ts": `import { Routes } from '@angular/router';
import { UsersComponent } from './users.component';

export default [
  { path: 'users', component: UsersComponent }
] as Routes
`,
		"src/app/app.routes.ts": `import { Routes } from '@angular/router';

const routes: Routes = [
  { path: 'admin', loadChildren: () => import('./+admin/routes') },
];

export default routes
`,
		"src/main.ts": `import { provideRouter } from '@angular/router';
import routes from './app/app.routes';

export const config = { providers: [provideRouter(routes)] };
`,
	}, true)

	if _, ok := findRoute(fs, "/admin/users"); !ok {
		t.Errorf("default-export lazy routes did not resolve; got %v", routePaths(fs))
	}
}

// One component library writes every route through a helper that returns it. The
// object inside carries the path, and refusing to look through the call read a
// 212-route application as having one.
func TestAngularFactoryWrappedRoutesAreRead(t *testing.T) {
	fs := extractAngular(t, map[string]string{
		"src/app/landing.component.ts": `import { Component } from '@angular/core';
@Component({ selector: 'app-landing' })
export class LandingComponent {}
`,
		"src/app/app.routes.ts": `import { Routes } from '@angular/router';
import { pageTab as route } from '@acme/doc';
import { LandingComponent } from './landing.component';

export const routes: Routes = [
  route({ path: 'docs', component: LandingComponent }),
];
`,
		"src/app/app.config.ts": `import { provideRouter } from '@angular/router';
import { routes } from './app.routes';

export const appConfig = { providers: [provideRouter(routes)] };
`,
	}, true)

	if _, ok := findRoute(fs, "/docs"); !ok {
		t.Errorf("factory-wrapped route not read; got %v", routePaths(fs))
	}
}

// A path that names a constant is not a URL. Writing the constant's own text would
// be a fact about nothing, so the entry is counted and skipped.
func TestAngularNonLiteralPathIsRefused(t *testing.T) {
	fs := extractAngular(t, map[string]string{
		"src/app/thing.component.ts": `import { Component } from '@angular/core';
@Component({ selector: 'app-thing' })
export class ThingComponent {}
`,
		"src/app/app.routes.ts": `import { Routes } from '@angular/router';
import { ThingComponent } from './thing.component';
import { AppRoute } from './route-names';

export const routes: Routes = [
  { path: AppRoute.Thing, component: ThingComponent },
];
`,
		"src/app/app.config.ts": `import { provideRouter } from '@angular/router';
import { routes } from './app.routes';

export const appConfig = { providers: [provideRouter(routes)] };
`,
	}, true)

	for _, p := range routePaths(fs) {
		if strings.Contains(p, "AppRoute") {
			t.Errorf("wrote a constant's text as a route path: %q", p)
		}
	}
	assertRouteCause(t, fs, "non_literal_path=1")
}

// A mount whose module could not be resolved is a missing mount, not configuration.
func TestAngularUnresolvedLazyImportIsNamed(t *testing.T) {
	fs := extractAngular(t, map[string]string{
		"src/app/app.routes.ts": `import { Routes } from '@angular/router';

export const routes: Routes = [
  { path: 'admin', loadChildren: () => import('@acme/admin').then(m => m.AdminModule) },
];
`,
		"src/app/app.config.ts": `import { provideRouter } from '@angular/router';
import { routes } from './app.routes';

export const appConfig = { providers: [provideRouter(routes)] };
`,
	}, true)

	assertRouteCause(t, fs, "unresolved_lazy_import=1")
}

func assertRouteCause(t *testing.T, fs []facts.Fact, want string) {
	t.Helper()
	for _, f := range fs {
		if f.Kind == facts.KindExtraction && f.Name == "typescript:angular-routes" {
			if got, _ := f.Props["unresolved_macros"].(string); got != want {
				t.Errorf("unresolved causes = %q, want %q", got, want)
			}
			return
		}
	}
	t.Error("no typescript:angular-routes coverage fact")
}
