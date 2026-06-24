package swiftextractor

import (
	"testing"
)

func TestExtractURLSessionFacts(t *testing.T) {
	src := `import Foundation

final class EntitlementAPIService {
    func getDefinitions() async throws -> [EntitlementDto] {
        var request = URLRequest(url: baseURL.appendingPathComponent("settings/entitlements/definitions"))
        request.httpMethod = "GET"
        return try await send(request)
    }

    func getActive(userID: Int) async throws -> [ActiveEntitlementDto] {
        var request = URLRequest(url: baseURL.appendingPathComponent("settings/entitlements/users/\(userID)/active"))
        return try await send(request)   // no explicit httpMethod -> default GET
    }

    func grant(userID: Int) async throws {
        var urlRequest = URLRequest(url: baseURL.appendingPathComponent("settings/entitlements/users/\(userID)/grant"))
        urlRequest.httpMethod = "POST"
        _ = try await send(urlRequest)
    }
}
`
	ff := extractURLSessionFacts([]byte(src), "Data/Network/EntitlementAPIService.swift")
	if len(ff) != 3 {
		t.Fatalf("expected 3 client routes, got %d: %+v", len(ff), ff)
	}

	byName := map[string]string{} // name -> method
	for _, f := range ff {
		if f.Props["role"] != "client" || f.Props["framework"] != "urlsession" {
			t.Errorf("%s wrong props: %+v", f.Name, f.Props)
		}
		if f.Props["api"] != "EntitlementAPIService" {
			t.Errorf("%s api hint = %v", f.Name, f.Props["api"])
		}
		byName[f.Name] = f.Props["method"].(string)
	}

	if byName["settings/entitlements/definitions"] != "GET" {
		t.Errorf("definitions: want GET, got %q", byName["settings/entitlements/definitions"])
	}
	// Interpolation collapsed to {}, no httpMethod line -> default GET.
	if byName["settings/entitlements/users/{}/active"] != "GET" {
		t.Errorf("active: want default GET on {}, got %+v", byName)
	}
	if byName["settings/entitlements/users/{}/grant"] != "POST" {
		t.Errorf("grant: want POST, got %+v", byName)
	}
}

// TestExtractURLSessionFacts_NonNetworkFileSkipped verifies the file-level gate:
// a source that never references URLSession/URLRequest (e.g. a PDF exporter using
// appendingPathComponent for file I/O) emits no client routes.
func TestExtractURLSessionFacts_NonNetworkFileSkipped(t *testing.T) {
	src := `import Foundation

final class PdfExporter {
    func exportURL() -> URL {
        let dir = FileManager.default.temporaryDirectory.appendingPathComponent("exports")
        return dir.appendingPathComponent("report.pdf")
    }
}
`
	if ff := extractURLSessionFacts([]byte(src), "Screens/PerformanceReport/PdfExporter.swift"); len(ff) != 0 {
		t.Fatalf("expected no routes from a non-network file, got %d: %+v", len(ff), ff)
	}
}

// TestExtractURLSessionFacts_FileURLsExcluded verifies that file-URL building inside
// a network file (a temp .mov written before an upload) is excluded — via the
// file-URL line signal and the media-extension filter — while a real API call in the
// same file is still detected.
func TestExtractURLSessionFacts_FileURLsExcluded(t *testing.T) {
	src := `import Foundation

final class MediaAnalysisModal {
    func upload() async throws {
        let tmp = FileManager.default.temporaryDirectory.appendingPathComponent("media-analysis-\(UUID().uuidString).mov")
        var request = URLRequest(url: baseURL.appendingPathComponent("settings/visual-ai-coach/analyze"))
        request.httpMethod = "POST"
        _ = try await URLSession.shared.data(for: request)
    }
}
`
	ff := extractURLSessionFacts([]byte(src), "Components/QuickActions/MediaAnalysisModal.swift")
	if len(ff) != 1 {
		t.Fatalf("expected exactly 1 route (the real API call), got %d: %+v", len(ff), ff)
	}
	if ff[0].Name != "settings/visual-ai-coach/analyze" || ff[0].Props["method"] != "POST" {
		t.Errorf("unexpected route: %+v", ff[0])
	}
}

func TestCollapseSwiftInterpolation(t *testing.T) {
	cases := map[string]string{
		`media-analysis-\(UUID().uuidString).mov`: "media-analysis-{}.mov",
		`users/\(userID)/active`:                  "users/{}/active",
		`a/\(x)/b/\(y)`:                           "a/{}/b/{}",
		`no-interpolation`:                        "no-interpolation",
	}
	for in, want := range cases {
		if got := collapseSwiftInterpolation(in); got != want {
			t.Errorf("collapseSwiftInterpolation(%q) = %q, want %q", in, got, want)
		}
	}
}
