package tsextractor

import (
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

const cardComponent = `import { Component } from '@angular/core';

@Component({ selector: 'app-card', template: '<p>card</p>' })
export class CardComponent {}
`

func TestAngularTemplateBindsMembersAndChildren(t *testing.T) {
	fs := extractAngular(t, map[string]string{
		"src/app/card.component.ts": cardComponent,
		"src/app/page.component.ts": `import { Component } from '@angular/core';

@Component({ selector: 'app-page', templateUrl: './page.component.html' })
export class PageComponent {
  total = 0;
  save(): void {}
}
`,
		"src/app/page.component.html": `<h1>{{ total }}</h1>
<button (click)="save()">save</button>
<app-card></app-card>
`,
	}, true)

	f := symbolNamed(t, fs, "src/app.PageComponent")
	for _, want := range []string{"src/app.PageComponent.total", "src/app.PageComponent.save", "src/app.CardComponent"} {
		if !hasRelation(f, facts.RelCalls, want) {
			t.Errorf("no template edge to %s; relations: %+v", want, f.Relations)
		}
	}
}

// The rule the pass rests on: an identifier that names no member of this component
// is a local, an alias or a global, and produces nothing.
func TestAngularTemplateIgnoresNonMembers(t *testing.T) {
	fs := extractAngular(t, map[string]string{
		"src/app/page.component.ts": `import { Component } from '@angular/core';

@Component({ selector: 'app-page', templateUrl: './page.component.html' })
export class PageComponent {
  items: string[] = [];
}
`,
		"src/app/page.component.html": `<ul>
  <li *ngFor="let item of items">{{ item }} {{ unknownThing }}</li>
</ul>
`,
	}, true)

	f := symbolNamed(t, fs, "src/app.PageComponent")
	if !hasRelation(f, facts.RelCalls, "src/app.PageComponent.items") {
		t.Error("the member the loop iterates was not referenced")
	}
	for _, r := range f.Relations {
		if r.Kind == facts.RelCalls && (r.Target == "src/app.PageComponent.item" || r.Target == "src/app.PageComponent.unknownThing") {
			t.Errorf("invented an edge for a template local or unknown name: %s", r.Target)
		}
	}
}

// Angular 17 block control flow. Both dialects are live in the same corpus, and a
// reader that only knows the older one loses every expression inside a block.
func TestAngularTemplateReadsBlockControlFlow(t *testing.T) {
	fs := extractAngular(t, map[string]string{
		"src/app/page.component.ts": `import { Component } from '@angular/core';

@Component({ selector: 'app-page', templateUrl: './page.component.html' })
export class PageComponent {
  loading = false;
  rows: string[] = [];
  reload(): void {}
}
`,
		"src/app/page.component.html": `@if (loading) {
  <span>…</span>
} @else {
  @for (row of rows; track row) {
    <b>{{ row }}</b>
  }
  <button (click)="reload()">reload</button>
}
`,
	}, true)

	f := symbolNamed(t, fs, "src/app.PageComponent")
	for _, want := range []string{"src/app.PageComponent.loading", "src/app.PageComponent.rows", "src/app.PageComponent.reload"} {
		if !hasRelation(f, facts.RelCalls, want) {
			t.Errorf("block control flow lost the reference to %s", want)
		}
	}
}

func TestAngularInlineTemplateIsRead(t *testing.T) {
	fs := extractAngular(t, map[string]string{
		"src/app/card.component.ts": cardComponent,
		"src/app/page.component.ts": `import { Component } from '@angular/core';

@Component({
  selector: 'app-page',
  template: ` + "`" + `<app-card [label]="title"></app-card>` + "`" + `,
})
export class PageComponent {
  title = 'hi';
}
`,
	}, true)

	f := symbolNamed(t, fs, "src/app.PageComponent")
	for _, want := range []string{"src/app.PageComponent.title", "src/app.CardComponent"} {
		if !hasRelation(f, facts.RelCalls, want) {
			t.Errorf("inline template lost %s", want)
		}
	}
}

func TestAngularTemplateResolvesPipes(t *testing.T) {
	fs := extractAngular(t, map[string]string{
		"src/app/truncate.pipe.ts": `import { Pipe } from '@angular/core';

@Pipe({ name: 'truncate' })
export class TruncatePipe {}
`,
		"src/app/page.component.ts": `import { Component } from '@angular/core';

@Component({ selector: 'app-page', templateUrl: './page.component.html' })
export class PageComponent {
  text = '';
}
`,
		"src/app/page.component.html": `<p>{{ text | truncate }}</p>`,
	}, true)

	f := symbolNamed(t, fs, "src/app.PageComponent")
	if !hasRelation(f, facts.RelCalls, "src/app.TruncatePipe") {
		t.Errorf("pipe not resolved; relations: %+v", f.Relations)
	}
	if hasRelation(f, facts.RelCalls, "src/app.PageComponent.truncate") {
		t.Error("a pipe name was read as a member reference")
	}
}

// A compound selector needs both halves. Matching the attribute alone attached a
// component to every template that happened to write that attribute on any element
// — the one wrong edge in a 200-edge audit.
func TestAngularCompoundSelectorNeedsBothHalves(t *testing.T) {
	fs := extractAngular(t, map[string]string{
		"src/app/wrapper.component.ts": `import { Component } from '@angular/core';

@Component({ selector: 'app-wrapper[labels]', template: '<div></div>' })
export class WrapperComponent {}
`,
		"src/app/page.component.ts": `import { Component } from '@angular/core';

@Component({ selector: 'app-page', templateUrl: './page.component.html' })
export class PageComponent {}
`,
		"src/app/page.component.html": `<app-other labels="a,b"></app-other>`,
	}, true)

	f := symbolNamed(t, fs, "src/app.PageComponent")
	if hasRelation(f, facts.RelCalls, "src/app.WrapperComponent") {
		t.Error("matched a compound selector on its attribute alone")
	}
}

// `selector: 'app-icon:not([badge])'` is an ordinary element selector with an
// exclusion attached. Refusing to read it left most of one library's components
// with no indexed selector at all.
func TestAngularSelectorWithNotPseudoIsIndexed(t *testing.T) {
	fs := extractAngular(t, map[string]string{
		"src/app/icon.component.ts": `import { Component } from '@angular/core';

@Component({ selector: 'app-icon:not([badge])', template: '<i></i>' })
export class IconComponent {}
`,
		"src/app/page.component.ts": `import { Component } from '@angular/core';

@Component({ selector: 'app-page', templateUrl: './page.component.html' })
export class PageComponent {}
`,
		"src/app/page.component.html": `<app-icon></app-icon>`,
	}, true)

	if f := symbolNamed(t, fs, "src/app.PageComponent"); !hasRelation(f, facts.RelCalls, "src/app.IconComponent") {
		t.Errorf("a :not() selector was not indexed; relations: %+v", f.Relations)
	}
}

// Angular's own structural elements render no component of the repository's, so
// counting them as unresolved would report the framework as a gap.
func TestAngularBuiltInTagsAreNotCountedAsMisses(t *testing.T) {
	fs := extractAngular(t, map[string]string{
		"src/app/page.component.ts": `import { Component } from '@angular/core';

@Component({ selector: 'app-page', templateUrl: './page.component.html' })
export class PageComponent {}
`,
		"src/app/page.component.html": `<ng-container><ng-template></ng-template><router-outlet></router-outlet></ng-container>`,
	}, true)

	for _, f := range fs {
		if f.Kind == facts.KindExtraction && f.Name == "typescript:angular-templates" {
			if got, _ := f.Props["unresolved_macros"].(string); got != "" {
				t.Errorf("Angular's own elements were counted as unresolved: %q", got)
			}
		}
	}
}

// A .html file in a repository with no Angular dependency models nothing, exactly
// as a lone .hbs does.
func TestAngularTemplatesAreGatedOnTheDependency(t *testing.T) {
	fs := extractAngular(t, map[string]string{
		"src/app/page.component.ts": `import { Component } from '@angular/core';

@Component({ selector: 'app-page', templateUrl: './page.component.html' })
export class PageComponent {
  total = 0;
}
`,
		"src/app/page.component.html": `<h1>{{ total }}</h1>`,
	}, false)

	f := symbolNamed(t, fs, "src/app.PageComponent")
	for _, r := range f.Relations {
		if r.Kind == facts.RelCalls {
			t.Errorf("read a template in a non-Angular repository: %s", r.Target)
		}
	}
}
