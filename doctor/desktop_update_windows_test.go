//go:build windows

package doctor

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWindowsStoreUpdateComparesAllFourBuildComponentsLikeRust(t *testing.T) {
	manifest := func(product string, version string) []byte {
		body, err := json.Marshal(map[string]any{
			"schemaVersion":   1,
			"buildVersion":    version,
			"storeProductId":  product,
			"packageIdentity": "OpenAI.Codex",
		})
		if err != nil {
			t.Fatalf("marshal manifest: %v", err)
		}
		return body
	}
	build, newer, err := windowsStoreUpdate(manifest("9PLM9XGG6VKS", "26.803.5235.1"), "26.803.5235.0")
	if err != nil || !newer || build != "26.803.5235.1" {
		t.Fatalf("update = %q, %v, %v", build, newer, err)
	}
	if _, newer, err := windowsStoreUpdate(manifest("9PLM9XGG6VKS", "26.803.5235.1"), "26.803.5235.1"); err != nil || newer {
		t.Fatalf("same build reported as update: %v, %v", newer, err)
	}
	if _, _, err := windowsStoreUpdate(manifest("other", "26.803.5235.1"), "26.803.5235.0"); err == nil {
		t.Fatal("foreign product manifest accepted")
	}
	if _, _, err := windowsStoreUpdate([]byte("{"), "26.803.5235.0"); err == nil {
		t.Fatal("invalid manifest accepted")
	}
	if _, _, err := windowsStoreUpdate(manifest("9PLM9XGG6VKS", "26.1"), "26.803.5235.0"); err == nil {
		t.Fatal("invalid build version accepted")
	}
}

func TestDesktopUpdateAppendsRowsAndCDNDetailLikeRust(t *testing.T) {
	restore := stubDesktopUpdate(t)
	defer restore()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		body, _ := json.Marshal(map[string]any{
			"schemaVersion":   1,
			"buildVersion":    "26.1027.6604.0",
			"storeProductId":  "9PLM9XGG6VKS",
			"packageIdentity": "OpenAI.Codex",
		})
		_, _ = w.Write(body)
	}))
	defer server.Close()
	desktopUpdateURLProbe = func() string { return server.URL + "/codex-app-prod/windows-store-update.json?v=2" }
	desktopAppProbe = func() (*desktopApp, error) {
		return &desktopApp{identity: "OpenAI.Codex", version: "26.1021.3705.0"}, nil
	}
	checks := desktopUpdateTestChecks()
	applyDesktopUpdateDetails(checks, server.Client())
	if got := strings.Join(checks[1].Details, "\n"); got != "desktop assets CDN: "+server.URL+"/codex-app-prod/windows-store-update.json reachable (HTTP 200)" {
		t.Fatalf("reachability details = %q", got)
	}
	want := []string{
		"desktop update status: available",
		"desktop latest build: 26.1027.6604.0",
		"desktop application: OpenAI.Codex",
	}
	joined := strings.Join(checks[0].Details, "\n")
	for _, detail := range want {
		if !strings.Contains(joined, detail) {
			t.Fatalf("updates details missing %q:\n%s", detail, joined)
		}
	}
	if note := desktopUpdateNote(checks[0]); note == nil || note.summary != "build 26.1027.6604.0 available" || note.name != "desktop" {
		t.Fatalf("desktop note = %+v", note)
	}
}

func TestDesktopUpdateReportsMissingManifestWithoutRowsLikeRust(t *testing.T) {
	restore := stubDesktopUpdate(t)
	defer restore()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer server.Close()
	desktopUpdateURLProbe = func() string { return server.URL + "/codex-app-prod/windows-store-update.json" }
	checks := desktopUpdateTestChecks()
	applyDesktopUpdateDetails(checks, server.Client())
	if got, want := strings.Join(checks[1].Details, "\n"), "desktop assets CDN: "+server.URL+"/codex-app-prod/windows-store-update.json reachable (HTTP 404; no update available)"; got != want {
		t.Fatalf("reachability details = %q, want %q", got, want)
	}
	if len(checks[0].Details) != 0 {
		t.Fatalf("updates details = %#v", checks[0].Details)
	}
}

func TestDesktopUpdateWarnsWhenCDNIsUnreachableLikeRust(t *testing.T) {
	restore := stubDesktopUpdate(t)
	defer restore()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	desktopUpdateURLProbe = func() string { return server.URL + "/codex-app-prod/windows-store-update.json" }
	checks := desktopUpdateTestChecks()
	applyDesktopUpdateDetails(checks, server.Client())
	reachability := checks[1]
	if reachability.Status != CheckStatusWarning || reachability.Summary != "desktop update and runtime CDN is unreachable" {
		t.Fatalf("reachability = %+v", reachability)
	}
	if !strings.Contains(strings.Join(reachability.Details, "\n"), "HTTP 500 (optional)") {
		t.Fatalf("reachability details = %#v", reachability.Details)
	}
	if len(reachability.Issues) != 1 || reachability.Issues[0].Cause != "desktop update and runtime CDN is unreachable" {
		t.Fatalf("reachability issues = %#v", reachability.Issues)
	}
}

func TestDesktopUpdateRejectsNonHTTPSManifestRedirectLikeRust(t *testing.T) {
	restore := stubDesktopUpdate(t)
	defer restore()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("{}"))
	}))
	defer server.Close()
	desktopUpdateURLProbe = func() string { return server.URL + "/codex-app-prod/windows-store-update.json" }
	checks := desktopUpdateTestChecks()
	applyDesktopUpdateDetails(checks, server.Client())
	reachability := checks[1]
	if reachability.Status != CheckStatusWarning || reachability.Summary != "desktop update and runtime CDN is unreachable" {
		t.Fatalf("reachability = %+v", reachability)
	}
	if !strings.Contains(strings.Join(reachability.Details, "\n"), "update manifest redirected to a non-HTTPS URL (optional)") {
		t.Fatalf("reachability details = %#v", reachability.Details)
	}
	if len(checks[0].Details) != 0 {
		t.Fatalf("updates details = %#v", checks[0].Details)
	}
}

func desktopUpdateTestChecks() []*DoctorCheck {
	return []*DoctorCheck{
		NewCheck("updates.status", "updates", CheckStatusOK, "update configuration is locally consistent"),
		NewCheck("network.provider_reachability", "reachability", CheckStatusOK, "active provider endpoints are reachable over HTTP"),
	}
}

func stubDesktopUpdate(t *testing.T) func() {
	t.Helper()
	url, app := desktopUpdateURLProbe, desktopAppProbe
	return func() {
		desktopUpdateURLProbe = url
		desktopAppProbe = app
	}
}
