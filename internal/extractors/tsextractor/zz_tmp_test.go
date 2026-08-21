package tsextractor

import "testing"

func TestZZGauzyShape(t *testing.T) {
	fs := extractAngular(t, map[string]string{
		"src/app/pages/pages.module.ts": `import { NgModule } from '@angular/core';
import { PagesRoutingModule } from './pages-routing.module';
@NgModule({ imports: [PagesRoutingModule] })
export class PagesModule {}
`,
		"src/app/pages/pages-routing.module.ts": `import { NgModule } from '@angular/core';
import { RouterModule, Routes } from '@angular/router';
import { DashboardComponent } from './dashboard.component';

const routes: Routes = [
	{ path: 'dashboard', component: DashboardComponent }
];

@NgModule({ imports: [RouterModule.forChild(routes)] })
export class PagesRoutingModule {}
`,
		"src/app/pages/dashboard.component.ts": `import { Component } from '@angular/core';
@Component({ selector: 'app-dash' })
export class DashboardComponent {}
`,
		"src/app/app.routes.ts": `import { Routes } from '@angular/router';
import { AuthGuard } from './auth.guard';

export const appRoutes: Routes = [
	{
		path: 'pages',
		loadChildren: () => import('./pages/pages.module').then((m) => m.PagesModule),
		canActivate: [AuthGuard]
	}
];
`,
		"src/app/auth.guard.ts": `import { Injectable } from '@angular/core';
@Injectable()
export class AuthGuard {}
`,
		"src/app/app.module.ts": `import { NgModule } from '@angular/core';
import { RouterModule } from '@angular/router';
import { appRoutes } from './app.routes';

const config = {};

@NgModule({ imports: [RouterModule.forRoot(appRoutes, config)] })
export class AppModule {}
`,
	}, true)
	t.Logf("routes: %v", routePaths(fs))
	for _, f := range fs {
		if f.Kind == "extraction" {
			t.Logf("cov %v %v", f.Props["edge_coverage"], f.Props["unresolved_macros"])
		}
	}
}
