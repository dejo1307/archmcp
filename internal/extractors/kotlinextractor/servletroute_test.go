package kotlinextractor

import (
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

func TestExtractServletRouteFacts(t *testing.T) {
	// The fluent servlet-registration DSL used by embedded HTTP servers: each
	// .addServlet("/path", handler) on its own line in a builder chain.
	src := `package com.example.server.di

class ServletRegistry {
    fun registerWith(builder: HttpServer.Builder): HttpServer.Builder =
        builder
            .addServlet("/v1/widgets/details", widgetDetailsServlet)
            .addServlet("/v1/gadgets", gadgetsServlet)
            .addServlet("/v1/gizmos/*", gizmoValuesServlet)
            .addServletAttribute(Server.attrMetricsProvider, provider())
}
`
	ff := extractServletRouteFacts([]byte(src), "src/main/kotlin/com/example/server/di/ServletRegistry.kt")
	if len(ff) != 3 {
		t.Fatalf("expected 3 server routes (addServletAttribute must be ignored), got %d: %+v", len(ff), ff)
	}

	byName := map[string]map[string]any{}
	for _, f := range ff {
		if f.Kind != facts.KindRoute {
			t.Errorf("kind = %q, want route", f.Kind)
		}
		if f.Props["role"] != "server" {
			t.Errorf("%s role = %v, want server", f.Name, f.Props["role"])
		}
		if f.Props["method"] != facts.MethodAny {
			t.Errorf("%s method = %v, want %q (servlet serves any verb)", f.Name, f.Props["method"], facts.MethodAny)
		}
		if f.Props["framework"] != "servlet" {
			t.Errorf("%s framework = %v, want servlet", f.Name, f.Props["framework"])
		}
		byName[f.Name] = f.Props
	}

	for _, want := range []string{"/v1/widgets/details", "/v1/gadgets"} {
		if _, ok := byName[want]; !ok {
			t.Errorf("missing server route %q; got %+v", want, byName)
		}
	}
	// Trailing path-spec wildcard is trimmed to the concrete prefix.
	if _, ok := byName["/v1/gizmos"]; !ok {
		t.Errorf("wildcard path /v1/gizmos/* should normalize to /v1/gizmos; got %+v", byName)
	}
	if p, ok := byName["/v1/gadgets"]; ok && p["handler"] != "gadgetsServlet" {
		t.Errorf("/v1/gadgets handler = %v, want gadgetsServlet", p["handler"])
	}
}
