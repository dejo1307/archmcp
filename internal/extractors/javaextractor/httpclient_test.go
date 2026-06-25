package javaextractor

import (
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// clientRoutes returns the role=client route facts, keyed by "METHOD path".
func clientRoutes(ff []facts.Fact) map[string]facts.Fact {
	m := map[string]facts.Fact{}
	for _, f := range ff {
		if f.Kind == facts.KindRoute && f.Props["role"] == "client" {
			m[f.Props["method"].(string)+" "+f.Name] = f
		}
	}
	return m
}

func TestRestTemplate_ClientCalls(t *testing.T) {
	ff := extractAll(t, map[string]string{
		"rest/RestClient.java": `package rest;
import org.springframework.web.client.RestTemplate;
import org.springframework.http.HttpMethod;
public class RestClient {
    private RestTemplate restTemplate;
    private String baseURL;
    public void readSettings(String key) {
        restTemplate.getForEntity(baseURL + "/api/admin/settings/{key}", AdminSettings.class, key);
    }
    public void save(Object body) {
        restTemplate.exchange(
            baseURL + "/api/admin/securitySettings",
            HttpMethod.POST,
            body,
            SecuritySettings.class);
    }
    public void remove(String id) {
        restTemplate.delete("/api/admin/items/{id}", id);
    }
}`,
	})

	got := clientRoutes(ff)
	for _, want := range []struct{ key, method string }{
		{"GET /api/admin/settings/{key}", "GET"},
		{"POST /api/admin/securitySettings", "POST"},
		{"DELETE /api/admin/items/{id}", "DELETE"},
	} {
		r, ok := got[want.key]
		if !ok {
			t.Errorf("missing client route %q; got %v", want.key, keysOf(got))
			continue
		}
		if r.Props["framework"] != "resttemplate" {
			t.Errorf("%s framework = %v, want resttemplate", want.key, r.Props["framework"])
		}
	}
	if len(got) != 3 {
		t.Errorf("expected 3 client routes, got %d: %v", len(got), keysOf(got))
	}
}

func TestRestTemplate_NoFalsePositives(t *testing.T) {
	ff := extractAll(t, map[string]string{
		"util/Misc.java": `package util;
import java.util.Map;
import org.springframework.web.client.RestTemplate;
public class Misc {
    private Map<String,Object> cache;
    private RestTemplate restTemplate;
    public void notHttp() {
        cache.put("settingsKey", new Object());   // generic put — no leading-slash literal
    }
    public void external() {
        restTemplate.getForObject("https://api.stripe.com/v1/charges", Charge.class); // external URL
    }
}`,
	})
	if got := clientRoutes(ff); len(got) != 0 {
		t.Errorf("expected no client routes (non-HTTP put + external URL), got %v", keysOf(got))
	}
}

func TestFeignClient_AndControllerDiscriminated(t *testing.T) {
	ff := extractAll(t, map[string]string{
		"client/BillingClient.java": `package client;
import org.springframework.cloud.openfeign.FeignClient;
import org.springframework.web.bind.annotation.GetMapping;
@FeignClient(name = "billing")
public interface BillingClient {
    @GetMapping("/api/invoices/{id}")
    Invoice getInvoice(Long id);
}`,
		"web/InvoiceController.java": `package web;
import org.springframework.web.bind.annotation.*;
@RestController
@RequestMapping("/api/invoices")
public class InvoiceController {
    @GetMapping("/{id}")
    public Invoice get(Long id) { return null; }
}`,
	})

	// Feign interface method → client route with the service hint.
	got := clientRoutes(ff)
	r, ok := got["GET /api/invoices/{id}"]
	if !ok {
		t.Fatalf("missing Feign client route; got %v", keysOf(got))
	}
	if r.Props["framework"] != "feign" {
		t.Errorf("framework = %v, want feign", r.Props["framework"])
	}
	if r.Props["target_hint"] != "billing" {
		t.Errorf("target_hint = %v, want billing", r.Props["target_hint"])
	}

	// The @RestController serving the same path stays a SERVER route (no role).
	serverCount := 0
	for _, f := range ff {
		if f.Kind == facts.KindRoute && f.Name == "/api/invoices/{id}" && f.Props["role"] == nil {
			serverCount++
			if f.Props["framework"] != "spring" {
				t.Errorf("server route framework = %v, want spring", f.Props["framework"])
			}
		}
	}
	if serverCount != 1 {
		t.Errorf("expected 1 server route for /api/invoices/{id}, got %d", serverCount)
	}
}

func keysOf(m map[string]facts.Fact) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
