package appserver

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestActiveRemotePluginRootPrefersLocalThenRustSemverOrder(t *testing.T) {
	pluginBase := t.TempDir()
	for _, version := range []string{"1.9.0", "1.10.0", "local"} {
		if err := os.MkdirAll(filepath.Join(pluginBase, version), 0o755); err != nil {
			t.Fatalf("MkdirAll(%s) error = %v", version, err)
		}
	}
	if got := activeRemotePluginRoot(pluginBase); got != filepath.Join(pluginBase, "local") {
		t.Fatalf("active root = %q, want local", got)
	}
	if err := os.RemoveAll(filepath.Join(pluginBase, "local")); err != nil {
		t.Fatalf("RemoveAll(local) error = %v", err)
	}
	if got := activeRemotePluginRoot(pluginBase); got != filepath.Join(pluginBase, "1.10.0") {
		t.Fatalf("active root = %q, want semver 1.10.0", got)
	}
}

func TestValidateCachedRemotePluginRootRequiresMatchingManifestNameLikeRust(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".codex-plugin"), 0o755); err != nil {
		t.Fatalf("MkdirAll(manifest) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".codex-plugin", "plugin.json"), []byte(`{"name":"other"}`), 0o600); err != nil {
		t.Fatalf("WriteFile(manifest) error = %v", err)
	}
	if err := validateCachedRemotePluginRoot(root, "linear"); err == nil {
		t.Fatal("validateCachedRemotePluginRoot() error = nil, want name mismatch")
	}
	if err := os.WriteFile(filepath.Join(root, ".codex-plugin", "plugin.json"), []byte(`{"name":"linear"}`), 0o600); err != nil {
		t.Fatalf("WriteFile(matching manifest) error = %v", err)
	}
	if err := validateCachedRemotePluginRoot(root, "linear"); err != nil {
		t.Fatalf("validateCachedRemotePluginRoot() error = %v", err)
	}
}

func TestFetchInstalledRemotePluginDetailsPaginatesAndKeepsDisabledCacheLikeRust(t *testing.T) {
	home := t.TempDir()
	writeCachedRemotePluginForTest(t, home, remoteInstalledGlobalMarketplace, "alpha")
	betaRoot := writeCachedRemotePluginForTest(t, home, remoteInstalledGlobalMarketplace, "beta")
	writeCachedRemotePluginForTest(t, home, remoteInstalledWorkspaceSharedMarketplace, "gamma")
	var requestMu sync.Mutex
	requests := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer token" || request.Header.Get("ChatGPT-Account-ID") != "account-1" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if request.URL.Query().Get("includeDownloadUrls") != "true" {
			http.Error(w, "missing includeDownloadUrls", http.StatusBadRequest)
			return
		}
		scope := request.URL.Query().Get("scope")
		pageToken := request.URL.Query().Get("pageToken")
		requestMu.Lock()
		requests[scope+":"+pageToken]++
		requestMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case scope == "GLOBAL" && pageToken == "":
			_, _ = w.Write([]byte(`{"plugins":[{"id":"remote-alpha","name":"alpha","scope":"GLOBAL","enabled":true,"release":{"display_name":"Alpha","description":"Alpha plugin"}}],"pagination":{"next_page_token":"next"}}`))
		case scope == "GLOBAL" && pageToken == "next":
			_, _ = w.Write([]byte(`{"plugins":[{"id":"remote-beta","name":"beta","scope":"GLOBAL","enabled":false,"release":{"display_name":"Beta","description":"Beta plugin"}}],"pagination":{"next_page_token":null}}`))
		case scope == "WORKSPACE":
			_, _ = w.Write([]byte(`{"plugins":[{"id":"remote-gamma","name":"gamma","scope":"WORKSPACE","discoverability":"PRIVATE","enabled":true,"release":{"display_name":"Gamma","description":"Gamma plugin"}}],"pagination":{"next_page_token":null}}`))
		default:
			_, _ = w.Write([]byte(`{"plugins":[],"pagination":{"next_page_token":null}}`))
		}
	}))
	defer server.Close()

	details, ok := fetchInstalledRemotePluginDetails(context.Background(), server.Client(), server.URL, "token", "account-1", home)
	if !ok {
		t.Fatal("fetchInstalledRemotePluginDetails() ok = false")
	}
	if len(details[remoteInstalledGlobalMarketplace]) != 1 || details[remoteInstalledGlobalMarketplace][0].Summary.Name != "alpha" {
		t.Fatalf("global details = %#v", details[remoteInstalledGlobalMarketplace])
	}
	if len(details[remoteInstalledWorkspaceSharedMarketplace]) != 1 || details[remoteInstalledWorkspaceSharedMarketplace][0].Summary.Name != "gamma" {
		t.Fatalf("shared workspace details = %#v", details[remoteInstalledWorkspaceSharedMarketplace])
	}
	if _, err := os.Stat(betaRoot); err != nil {
		t.Fatalf("disabled installed plugin cache was removed: %v", err)
	}
	requestMu.Lock()
	defer requestMu.Unlock()
	for key, want := range map[string]int{"GLOBAL:": 1, "GLOBAL:next": 1, "USER:": 1, "WORKSPACE:": 1} {
		if requests[key] != want {
			t.Fatalf("requests = %#v, want %s=%d", requests, key, want)
		}
	}
}

func writeCachedRemotePluginForTest(t *testing.T, home string, marketplace string, name string) string {
	t.Helper()
	root := filepath.Join(home, "plugins", "cache", marketplace, name, "local")
	if err := os.MkdirAll(filepath.Join(root, ".codex-plugin"), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s) error = %v", name, err)
	}
	manifest := fmt.Sprintf(`{"name":%q}`, name)
	if err := os.WriteFile(filepath.Join(root, ".codex-plugin", "plugin.json"), []byte(manifest), 0o600); err != nil {
		t.Fatalf("WriteFile(%s manifest) error = %v", name, err)
	}
	skillDir := filepath.Join(root, "skills", "demo")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s skill) error = %v", name, err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, SkillFilename), []byte("---\nname: demo\ndescription: Demo\n---\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(%s skill) error = %v", name, err)
	}
	return root
}
