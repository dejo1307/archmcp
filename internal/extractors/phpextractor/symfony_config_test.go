package phpextractor

import (
	"testing"
)

func TestSymfonyConfig_YAML(t *testing.T) {
	repo := writeRepo(t, map[string]string{
		"config/routes.yaml": `home:
    path: /
    controller: App\Controller\HomeController::index
    methods: [GET]

blog_show:
    path: /blog/{slug}
    controller: App\Controller\BlogController::show
    methods: GET|POST

# an import entry without a path should be skipped
api_routes:
    resource: ../src/Controller/
    type: attribute
    prefix: /api
`,
	})
	ff := extractSymfonyConfigRoutes(repo)
	if got := len(serverRoutes(ff)); got != 3 {
		t.Fatalf("got %d routes, want 3: %s", got, routeSummary(ff))
	}
	home := routeByMethodPath(t, ff, "GET", "/")
	if home.Props["handler"] != "App\\Controller\\HomeController::index" {
		t.Errorf("home handler = %v", home.Props["handler"])
	}
	if home.Props["source"] != "symfony-config" {
		t.Errorf("source = %v, want symfony-config", home.Props["source"])
	}
	routeByMethodPath(t, ff, "GET", "/blog/{slug}")
	routeByMethodPath(t, ff, "POST", "/blog/{slug}")
}

func TestSymfonyConfig_XML(t *testing.T) {
	repo := writeRepo(t, map[string]string{
		"config/routes.xml": `<?xml version="1.0" encoding="UTF-8" ?>
<routes xmlns="http://symfony.com/schema/routing">
    <route id="ping" path="/ping" controller="App\Controller\PingController::ping" methods="GET"/>
    <route id="submit" path="/submit" controller="App\Controller\FormController::submit" methods="POST"/>
</routes>
`,
	})
	ff := extractSymfonyConfigRoutes(repo)
	if got := len(serverRoutes(ff)); got != 2 {
		t.Fatalf("got %d routes, want 2: %s", got, routeSummary(ff))
	}
	ping := routeByMethodPath(t, ff, "GET", "/ping")
	if ping.Props["handler"] != "App\\Controller\\PingController::ping" {
		t.Errorf("ping handler = %v", ping.Props["handler"])
	}
	routeByMethodPath(t, ff, "POST", "/submit")
}

func TestSymfonyConfig_RoutesSubdir(t *testing.T) {
	// Route files split under config/routes/ must be discovered recursively, even
	// though a typical **/*.yaml ignore would hide them from the engine file list.
	repo := writeRepo(t, map[string]string{
		"config/routes/api.yaml": `api_status:
    path: /api/status
    controller: App\Controller\StatusController::status
    methods: [GET]
`,
	})
	ff := extractSymfonyConfigRoutes(repo)
	f := routeByMethodPath(t, ff, "GET", "/api/status")
	if f.Props["source"] != "symfony-config" {
		t.Errorf("source = %v, want symfony-config", f.Props["source"])
	}
	if f.File != "config/routes/api.yaml" {
		t.Errorf("file = %q, want config/routes/api.yaml", f.File)
	}
}

func TestSymfonyConfig_IgnoresNonRouteFiles(t *testing.T) {
	repo := writeRepo(t, map[string]string{
		"config/services.yaml":           "services:\n  _defaults:\n    autowire: true\n",
		"config/packages/framework.yaml": "framework:\n  secret: x\n",
	})
	ff := extractSymfonyConfigRoutes(repo)
	if got := len(ff); got != 0 {
		t.Fatalf("expected non-route config files to be ignored, got %d facts", got)
	}
}
