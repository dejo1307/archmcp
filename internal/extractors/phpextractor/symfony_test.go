package phpextractor

import (
	"testing"
)

func TestSymfonyRoutes_AttributesClassAndMethod(t *testing.T) {
	src := `<?php
namespace App\Controller;

use Symfony\Component\Routing\Annotation\Route;

#[Route('/api')]
class UserController
{
    #[Route('/users/{id}', methods: ['GET'], name: 'user_show')]
    public function show($id) {}

    #[Route('/users', methods: ['POST'])]
    public function create() {}
}
`
	ff := extractSymfonyRoutes([]byte(src), "src/Controller/UserController.php")
	if got := len(serverRoutes(ff)); got != 2 {
		t.Fatalf("got %d routes, want 2: %s", got, routeSummary(ff))
	}
	show := routeByMethodPath(t, ff, "GET", "/api/users/{id}")
	if show.Props["handler"] != "App\\Controller\\UserController::show" {
		t.Errorf("handler = %v, want App\\Controller\\UserController::show", show.Props["handler"])
	}
	if show.Props["name"] != "user_show" {
		t.Errorf("name = %v, want user_show", show.Props["name"])
	}
	routeByMethodPath(t, ff, "POST", "/api/users")
}

func TestSymfonyRoutes_MultipleMethods(t *testing.T) {
	src := `<?php
namespace App\Controller;
class ContactController
{
    #[Route('/contact', methods: ['GET', 'POST'])]
    public function handle() {}
}
`
	ff := extractSymfonyRoutes([]byte(src), "src/Controller/ContactController.php")
	if got := len(serverRoutes(ff)); got != 2 {
		t.Fatalf("got %d routes, want 2: %s", got, routeSummary(ff))
	}
	routeByMethodPath(t, ff, "GET", "/contact")
	routeByMethodPath(t, ff, "POST", "/contact")
}

func TestSymfonyRoutes_MethodConstants(t *testing.T) {
	// Symfony idiom: methods given as Request::METHOD_* constants, not strings.
	src := `<?php
namespace App\Controller;

use Symfony\Component\HttpFoundation\Request;
use Symfony\Component\Routing\Annotation\Route;

class AuthController
{
    #[Route(
        path: '/account/login',
        name: 'frontend.account.login.page',
        methods: [Request::METHOD_GET]
    )]
    public function loginPage() {}

    #[Route('/account/login', methods: [Request::METHOD_POST, Request::METHOD_PUT])]
    public function doLogin() {}
}
`
	ff := extractSymfonyRoutes([]byte(src), "src/Controller/AuthController.php")
	if got := len(serverRoutes(ff)); got != 3 {
		t.Fatalf("got %d routes, want 3: %s", got, routeSummary(ff))
	}
	get := routeByMethodPath(t, ff, "GET", "/account/login")
	if get.Props["name"] != "frontend.account.login.page" {
		t.Errorf("name = %v", get.Props["name"])
	}
	routeByMethodPath(t, ff, "POST", "/account/login")
	routeByMethodPath(t, ff, "PUT", "/account/login")
	// Ensure it did NOT fall back to ANY.
	for _, f := range serverRoutes(ff) {
		if f.Props["method"] == "ANY" {
			t.Errorf("unexpected ANY method (constant resolution failed): %+v", f.Props)
		}
	}
}

func TestSymfonyRoutes_NamedPathArg(t *testing.T) {
	src := `<?php
namespace App\Controller;
class PingController
{
    #[Route(path: '/ping', name: 'ping')]
    public function ping() {}
}
`
	ff := extractSymfonyRoutes([]byte(src), "src/Controller/PingController.php")
	f := routeByMethodPath(t, ff, "ANY", "/ping")
	if f.Props["name"] != "ping" {
		t.Errorf("name = %v, want ping", f.Props["name"])
	}
}

func TestSymfonyRoutes_LegacyAnnotation(t *testing.T) {
	src := `<?php
namespace App\Controller;
class LegacyController
{
    /**
     * @Route("/legacy", methods={"GET"}, name="legacy_index")
     */
    public function index() {}
}
`
	ff := extractSymfonyRoutes([]byte(src), "src/Controller/LegacyController.php")
	f := routeByMethodPath(t, ff, "GET", "/legacy")
	if f.Props["name"] != "legacy_index" {
		t.Errorf("name = %v, want legacy_index", f.Props["name"])
	}
	if f.Props["handler"] != "App\\Controller\\LegacyController::index" {
		t.Errorf("handler = %v, want App\\Controller\\LegacyController::index", f.Props["handler"])
	}
}
