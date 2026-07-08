package kotlinextractor

import "testing"

func TestExtractRetrofitFacts(t *testing.T) {
	src := `package com.fairwayhub.mygolfjournal.data.api

interface EntitlementApiService {
    @GET("/api/settings/entitlements/users/{userID}/active")
    suspend fun getActiveEntitlements(@Path("userID") userID: Int): Response<List<ActiveEntitlementDto>>

    @POST("auth/login")
    @Headers("X-Client-Type: mobile")
    suspend fun login(@Body request: LoginRequest): Response<LoginResponse>
}
`
	ff := extractRetrofitFacts([]byte(src), "data/api/EntitlementApiService.kt")
	if len(ff) != 2 {
		t.Fatalf("expected 2 client routes, got %d: %+v", len(ff), ff)
	}

	byName := map[string]map[string]any{}
	for _, f := range ff {
		if f.Kind != "route" {
			t.Errorf("kind = %q, want route", f.Kind)
		}
		if f.Props["role"] != "client" {
			t.Errorf("%s role = %v, want client", f.Name, f.Props["role"])
		}
		if f.Props["framework"] != "retrofit" {
			t.Errorf("%s framework = %v, want retrofit", f.Name, f.Props["framework"])
		}
		if f.Props["api"] != "EntitlementApiService" {
			t.Errorf("%s api hint = %v, want EntitlementApiService", f.Name, f.Props["api"])
		}
		byName[f.Name] = f.Props
	}

	if p, ok := byName["/api/settings/entitlements/users/{userID}/active"]; !ok || p["method"] != "GET" {
		t.Errorf("missing GET entitlements route; got %+v", byName)
	}
	if p, ok := byName["auth/login"]; !ok || p["method"] != "POST" {
		t.Errorf("missing POST auth/login route (relative path); got %+v", byName)
	}
}

// TestRetrofit_AbsoluteURLExternal verifies that a Retrofit annotation with a full
// http(s) URL is tagged external + host (bucketed out of internal coverage) with the
// Name reduced to the base-relative path, while a relative annotation is untouched.
func TestRetrofit_AbsoluteURLExternal(t *testing.T) {
	src := `package com.example.data.api

interface AdService {
    @GET("https://adserver.example.com/track/impression")
    suspend fun track(): Response<Unit>

    @POST("v2/events.json")
    suspend fun events(@Body b: EventBody): Response<Unit>
}
`
	ff := extractRetrofitFacts([]byte(src), "data/api/AdService.kt")
	if len(ff) != 2 {
		t.Fatalf("expected 2 client routes, got %d: %+v", len(ff), ff)
	}
	byName := map[string]map[string]any{}
	for _, f := range ff {
		byName[f.Name] = f.Props
	}

	ext, ok := byName["track/impression"]
	if !ok {
		t.Fatalf("absolute URL should reduce to base-relative path 'track/impression'; got %+v", byName)
	}
	if e, _ := ext["external"].(bool); !e {
		t.Errorf("absolute URL should be external=true; got %+v", ext)
	}
	if ext["host"] != "adserver.example.com" {
		t.Errorf("host = %v, want adserver.example.com", ext["host"])
	}

	rel, ok := byName["v2/events.json"]
	if !ok {
		t.Fatalf("relative route missing; got %+v", byName)
	}
	if _, has := rel["external"]; has {
		t.Errorf("relative annotation must not be external; got %+v", rel)
	}
}
