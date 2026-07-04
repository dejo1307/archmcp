package swiftextractor

import (
	"testing"
)

func TestExtractEndpointFacts(t *testing.T) {
	// An endpoint-enum type (Moya TargetType-like idiom): path + prefix + method are
	// per-case computed properties driven by `switch self`. Uses neutral names.
	src := `import Foundation

extension APIService {
   enum Reports { case send([String: Any]); case list; case sourceType }
}

extension APIService.Reports: APIEndpoint {
   var urlPrefixComponent: String {
      switch self {
      case .send: return "v2"
      case .list, .sourceType: return "public/v1"
      }
   }
   var urlPathComponent: String {
      switch self {
      case .send: return "reports.json"
      case .list: return "report_reasons.json"
      case .sourceType: return "report_source_types.json"
      }
   }
   var method: HTTPMethod {
      switch self {
      case .send: return .post
      case .list, .sourceType: return .get
      }
   }
}
`
	// defaultPrefix "v2" is present but must be ignored — this type defines its own
	// per-case prefix.
	ff := extractEndpointFacts([]byte(src), "Sources/Core/APIService.Reports.swift", "Sources/Core", "v2")
	got := map[string]string{}
	for _, f := range ff {
		if f.Props["role"] != "client" || f.Props["source"] != "swift-endpoint" {
			t.Errorf("%s wrong props: %+v", f.Name, f.Props)
		}
		got[f.Name] = f.Props["method"].(string)
	}
	if got["v2/reports.json"] != "POST" {
		t.Errorf("send: want v2/reports.json POST, got %+v", got)
	}
	if got["public/v1/report_reasons.json"] != "GET" {
		t.Errorf("list: want public/v1/report_reasons.json GET, got %+v", got)
	}
	if got["public/v1/report_source_types.json"] != "GET" {
		t.Errorf("sourceType: want GET, got %+v", got)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 endpoints, got %d: %+v", len(got), got)
	}
}

// TestExtractEndpointFacts_Interpolated covers interpolated paths (associated
// values) collapsing to the {} placeholder.
func TestExtractEndpointFacts_Interpolated(t *testing.T) {
	src := `extension APIService.Group: APIEndpoint {
   var urlPathComponent: String {
      switch self {
      case .memberships(let groupId, let userId):
         return "groups/\(groupId)/members/\(userId).json"
      }
   }
   var method: HTTPMethod {
      switch self {
      case .memberships: return .delete
      }
   }
}
`
	// No prefix property and empty defaultPrefix -> path stays base-relative.
	ff := extractEndpointFacts([]byte(src), "Sources/Core/APIService.Group.swift", "Sources/Core", "")
	if len(ff) != 1 || ff[0].Name != "groups/{}/members/{}.json" || ff[0].Props["method"] != "DELETE" {
		t.Fatalf("interpolated endpoint mismatch: %+v", ff)
	}
}

// TestExtractEndpointFacts_NotAnEndpointType ensures ordinary types with a `path`
// or `method` alone are not treated as endpoint enums.
func TestExtractEndpointFacts_NotAnEndpointType(t *testing.T) {
	src := `struct FileHelper {
   var path: String { return "/tmp/cache" }
}
`
	if ff := extractEndpointFacts([]byte(src), "Sources/Core/FileHelper.swift", "Sources/Core", "v2"); len(ff) != 0 {
		t.Errorf("a type with a path but no method must not be an endpoint: %+v", ff)
	}
}

// TestExtractEndpointFacts_DefaultPrefix: a type with no urlPrefixComponent gets
// the repo-wide default prefix applied to every case.
func TestExtractEndpointFacts_DefaultPrefix(t *testing.T) {
	src := `extension APIService.Device: APIEndpoint {
   var urlPathComponent: String {
      switch self {
      case .register: return "devices.json"
      case .unregister(let id): return "devices/\(id).json"
      }
   }
   var method: HTTPMethod {
      switch self {
      case .register: return .post
      case .unregister: return .delete
      }
   }
}
`
	got := map[string]string{}
	for _, f := range extractEndpointFacts([]byte(src), "Sources/Core/APIService.Device.swift", "Sources/Core", "v2") {
		got[f.Name] = f.Props["method"].(string)
	}
	if got["v2/devices.json"] != "POST" {
		t.Errorf("collection endpoint should get default v2 prefix: %+v", got)
	}
	if got["v2/devices/{}.json"] != "DELETE" {
		t.Errorf("param endpoint should get default v2 prefix: %+v", got)
	}
}

// TestExtractEndpointFacts_SingleValueOverride: a single-value computed prefix
// (implicit return, no switch) overrides the default for all cases.
func TestExtractEndpointFacts_SingleValueOverride(t *testing.T) {
	src := `extension APIService.Interactions: APIEndpoint {
   var urlPrefixComponent: String { "core/v3" }
   var urlPathComponent: String {
      switch self {
      case .get: return "interactions"
      }
   }
   var method: HTTPMethod {
      switch self {
      case .get: return .get
      }
   }
}
`
	got := map[string]string{}
	for _, f := range extractEndpointFacts([]byte(src), "Sources/Core/APIService.Interactions.swift", "Sources/Core", "v2") {
		got[f.Name] = f.Props["method"].(string)
	}
	if _, ok := got["core/v3/interactions"]; !ok {
		t.Errorf("single-value override should win over default: %+v", got)
	}
}

// TestExtractEndpointFacts_VersionInterpPrefix: a version-constant interpolation in
// the prefix resolves to the version token, not the {} placeholder.
func TestExtractEndpointFacts_VersionInterpPrefix(t *testing.T) {
	src := `extension APIService.Items: APIEndpoint {
   var urlPrefixComponent: String { "core/\(APIVersion.v3)" }
   var urlPathComponent: String {
      switch self {
      case .list: return "items"
      }
   }
   var method: HTTPMethod {
      switch self {
      case .list: return .get
      }
   }
}
`
	got := map[string]string{}
	for _, f := range extractEndpointFacts([]byte(src), "Sources/Core/APIService.Items.swift", "Sources/Core", "v2") {
		got[f.Name] = f.Props["method"].(string)
	}
	if _, ok := got["core/v3/items"]; !ok {
		t.Errorf("version interpolation should resolve to core/v3: %+v", got)
	}
}

// TestExtractEndpointFacts_SwitchPrefixDefault: a prefix switch with a `default:`
// branch resolves unlisted cases to the default branch value.
func TestExtractEndpointFacts_SwitchPrefixDefault(t *testing.T) {
	src := `extension APIService.Account: APIEndpoint {
   var urlPrefixComponent: String {
      switch self {
      case .generateToken, .deleteToken: return "core/v3"
      default: return "v2"
      }
   }
   var urlPathComponent: String {
      switch self {
      case .loadAccount: return "account.json"
      case .generateToken: return "account/token.json"
      }
   }
   var method: HTTPMethod {
      switch self {
      case .loadAccount: return .get
      case .generateToken: return .post
      }
   }
}
`
	got := map[string]string{}
	for _, f := range extractEndpointFacts([]byte(src), "Sources/Core/APIService.Account.swift", "Sources/Core", "v2") {
		got[f.Name] = f.Props["method"].(string)
	}
	if got["v2/account.json"] != "GET" {
		t.Errorf("unlisted case should use switch default v2: %+v", got)
	}
	if got["core/v3/account/token.json"] != "POST" {
		t.Errorf("explicit case should use core/v3: %+v", got)
	}
}

// TestDetectDefaultURLPrefix: the default is read from the protocol-declaring file
// and a concrete single-value override elsewhere is not mistaken for it.
func TestDetectDefaultURLPrefix(t *testing.T) {
	protoFile := "Sources/Core/APIEndpoint.swift"
	proto := `public protocol APIEndpoint {
   var urlPrefixComponent: String { get }
   var urlPathComponent: String { get }
}

public extension APIEndpoint {
   var urlPrefixComponent: String {
      "v2" // default for all existing urls
   }
}
`
	concrete := "Sources/Core/APIService.Widget.swift"

	dir := t.TempDir()
	mustWrite(t, dir, protoFile, proto)
	// A concrete single-value override — must NOT be picked as the default.
	mustWrite(t, dir, concrete, "struct WidgetEndpoint: APIEndpoint {\n   var urlPrefixComponent: String { \"core/v3\" }\n}\n")

	if got := detectDefaultURLPrefix(dir, []string{concrete, protoFile}); got != "v2" {
		t.Errorf("detectDefaultURLPrefix = %q, want v2 (from the protocol file, ignoring the concrete override)", got)
	}
}

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
