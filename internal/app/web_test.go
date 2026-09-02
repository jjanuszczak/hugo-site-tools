package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWebServerServesReadOnlyContentAndProtectsAPI(t *testing.T) {
	site := t.TempDir()
	if err := os.WriteFile(filepath.Join(site, "hugo.toml"), []byte(`baseURL = "https://example.test/"`), 0644); err != nil {
		t.Fatal(err)
	}
	writePost(t, filepath.Join(site, "content", "articles", "entry.md"), "---\ntitle: Entry\ntags: [Hugo]\n---\nsource body")
	if err := os.WriteFile(filepath.Join(site, ".hs.toml"), []byte(campaignDefaultConfig+`
[[campaigns]]
key = "always-on"
label = "Always on"
status = "active"
description = "General distribution"
`), 0644); err != nil {
		t.Fatal(err)
	}
	server, err := newWebServer(webProjectTarget{Project: site, Kind: "local"}, "secret")
	if err != nil {
		t.Fatal(err)
	}

	unauthorized := httptest.NewRequest(http.MethodGet, "/api/content", nil)
	unauthorizedResult := httptest.NewRecorder()
	server.ServeHTTP(unauthorizedResult, unauthorized)
	if unauthorizedResult.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", unauthorizedResult.Code)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/content", nil)
	request.Header.Set("X-HS-Session", "secret")
	result := httptest.NewRecorder()
	server.ServeHTTP(result, request)
	if result.Code != http.StatusOK {
		t.Fatalf("content status = %d: %s", result.Code, result.Body.String())
	}
	var items []webContentItem
	if err := json.NewDecoder(result.Body).Decode(&items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Source != "content/articles/entry.md" || items[0].GeneratedURL != "/articles/entry/" {
		t.Fatalf("content = %#v", items)
	}

	sourceRequest := httptest.NewRequest(http.MethodGet, "/api/source?path=content/articles/entry.md", nil)
	sourceRequest.Header.Set("X-HS-Session", "secret")
	sourceResult := httptest.NewRecorder()
	server.ServeHTTP(sourceResult, sourceRequest)
	if sourceResult.Code != http.StatusOK || !strings.Contains(sourceResult.Body.String(), "source body") {
		t.Fatalf("source response = %d %s", sourceResult.Code, sourceResult.Body.String())
	}

	blockedRequest := httptest.NewRequest(http.MethodGet, "/api/source?path=hugo.toml", nil)
	blockedRequest.Header.Set("X-HS-Session", "secret")
	blockedResult := httptest.NewRecorder()
	server.ServeHTTP(blockedResult, blockedRequest)
	if blockedResult.Code != http.StatusNotFound {
		t.Fatalf("blocked source status = %d", blockedResult.Code)
	}

	policyRequest := httptest.NewRequest(http.MethodGet, "/api/campaigns", nil)
	policyRequest.Header.Set("X-HS-Session", "secret")
	policyResult := httptest.NewRecorder()
	server.ServeHTTP(policyResult, policyRequest)
	if policyResult.Code != http.StatusOK || !strings.Contains(policyResult.Body.String(), "always-on") {
		t.Fatalf("campaign policy response = %d %s", policyResult.Code, policyResult.Body.String())
	}

	linkRequest := httptest.NewRequest(http.MethodPost, "/api/campaign-links", bytes.NewBufferString(`{"content_source":"content/articles/entry.md","campaign":"always-on","source":"linkedin","medium":"social","content":"ceo-post"}`))
	linkRequest.Header.Set("Content-Type", "application/json")
	linkRequest.Header.Set("X-HS-Session", "secret")
	linkResult := httptest.NewRecorder()
	server.ServeHTTP(linkResult, linkRequest)
	if linkResult.Code != http.StatusOK || !strings.Contains(linkResult.Body.String(), "utm_campaign=always-on") {
		t.Fatalf("campaign link response = %d %s", linkResult.Code, linkResult.Body.String())
	}

	qrRequest := httptest.NewRequest(http.MethodPost, "/api/campaign-links/qr", bytes.NewBufferString(`{"url":"https://example.test/articles/entry/?utm_campaign=always-on&utm_medium=social&utm_source=linkedin"}`))
	qrRequest.Header.Set("Content-Type", "application/json")
	qrRequest.Header.Set("X-HS-Session", "secret")
	qrResult := httptest.NewRecorder()
	server.ServeHTTP(qrResult, qrRequest)
	if qrResult.Code != http.StatusOK || qrResult.Header().Get("Content-Type") != "image/png" || !bytes.HasPrefix(qrResult.Body.Bytes(), []byte{0x89, 'P', 'N', 'G'}) {
		t.Fatalf("QR response = %d %s", qrResult.Code, qrResult.Body.String())
	}

	badQRRequest := httptest.NewRequest(http.MethodPost, "/api/campaign-links/qr", bytes.NewBufferString(`{"url":"https://outside.test/?utm_campaign=always-on&utm_medium=social&utm_source=linkedin"}`))
	badQRRequest.Header.Set("Content-Type", "application/json")
	badQRRequest.Header.Set("X-HS-Session", "secret")
	badQRResult := httptest.NewRecorder()
	server.ServeHTTP(badQRResult, badQRRequest)
	if badQRResult.Code != http.StatusBadRequest {
		t.Fatalf("invalid QR status = %d", badQRResult.Code)
	}
}

func TestWebOptionsAndRepositoryValidation(t *testing.T) {
	opts, err := parseWebOptions([]string{"--repo", "https://github.com/acme/site", "--ref", "main", "--subdir", "website", "--port", "8181"})
	if err != nil || opts.RepoURL != "https://github.com/acme/site" || opts.Ref != "main" || opts.Subdir != "website" || opts.Port != "8181" {
		t.Fatalf("opts=%#v err=%v", opts, err)
	}
	if _, err := validateGitHubRepositoryURL("https://example.com/acme/site"); err == nil {
		t.Fatal("accepted non-GitHub repository URL")
	}
	if err := validateRepoSubdir("../outside"); err == nil {
		t.Fatal("accepted escaping repository subdirectory")
	}
	withRelative, err := parseWebOptions([]string{"--relative-paths"})
	if err != nil || !withRelative.RelativePaths {
		t.Fatalf("relative path option = %#v err=%v", withRelative, err)
	}
}

func TestTUIOptionsSupportRepositoryTargets(t *testing.T) {
	options, err := parseTUIOptions([]string{"--repo", "https://github.com/acme/site", "--ref", "main", "--subdir", "website", "--relative-paths"})
	if err != nil || options.RepoURL != "https://github.com/acme/site" || options.Ref != "main" || options.Subdir != "website" || !options.RelativePath {
		t.Fatalf("TUI repository options = %#v err=%v", options, err)
	}
	if _, err := parseTUIOptions([]string{"--ref", "main"}); err == nil {
		t.Fatal("accepted --ref without --repo")
	}
}

func TestWebJobReportsSupportJSONAndSARIF(t *testing.T) {
	server, err := newWebServer(webProjectTarget{Kind: "local"}, "secret")
	if err != nil {
		t.Fatal(err)
	}
	server.jobs["job-1"] = &webJob{
		ID:     "job-1",
		Kind:   "seo",
		Status: "completed",
		Result: map[string]interface{}{"findings": []Finding{{Code: "HS-SEO-001", Severity: SeverityWarning, Message: "missing description", Source: "content/entry.md", Line: 4}}},
	}

	for _, format := range []string{"json", "sarif"} {
		request := httptest.NewRequest(http.MethodGet, "/api/jobs/job-1/report?format="+format, nil)
		request.Header.Set("X-HS-Session", "secret")
		result := httptest.NewRecorder()
		server.ServeHTTP(result, request)
		if result.Code != http.StatusOK || !strings.Contains(result.Header().Get("Content-Disposition"), "attachment") {
			t.Fatalf("%s report response = %d %s", format, result.Code, result.Body.String())
		}
		var payload map[string]interface{}
		if err := json.NewDecoder(result.Body).Decode(&payload); err != nil {
			t.Fatalf("decode %s report: %v", format, err)
		}
		if format == "sarif" && payload["version"] != "2.1.0" {
			t.Fatalf("SARIF payload = %#v", payload)
		}
	}

	request := httptest.NewRequest(http.MethodGet, "/api/jobs/job-1/report?format=xml", nil)
	request.Header.Set("X-HS-Session", "secret")
	result := httptest.NewRecorder()
	server.ServeHTTP(result, request)
	if result.Code != http.StatusBadRequest {
		t.Fatalf("invalid format status = %d", result.Code)
	}
}
