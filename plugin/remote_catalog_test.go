package plugin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestRemoteCatalogScopeMarketplaceMappingLikeRust(t *testing.T) {
	cases := map[string]string{
		RemoteCatalogScopeGlobal:    RemoteGlobalMarketplaceName,
		RemoteCatalogScopeUser:      "created-by-me-remote",
		RemoteCatalogScopeWorkspace: "workspace-directory",
	}
	for scope, marketplace := range cases {
		if got := RemoteMarketplaceNameForScope(scope); got != marketplace {
			t.Fatalf("RemoteMarketplaceNameForScope(%q) = %q, want %q", scope, got, marketplace)
		}
		back, ok := RemoteCatalogScopeForMarketplace(marketplace)
		if !ok || back != scope {
			t.Fatalf("RemoteCatalogScopeForMarketplace(%q) = %q/%v", marketplace, back, ok)
		}
	}
	if _, ok := RemoteCatalogScopeForMarketplace("local"); ok {
		t.Fatal("local marketplace resolved as remote")
	}
}

func TestFetchRemoteCatalogPluginsPagesAndCachesLikeRust(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.Header.Get("Authorization") != "Bearer token" || request.Header.Get("ChatGPT-Account-ID") != "account" {
			t.Errorf("headers = %#v", request.Header)
		}
		if request.URL.Query().Get("scope") != RemoteCatalogScopeGlobal || request.URL.Query().Get("collection") != "beta" {
			t.Errorf("query = %s", request.URL.RawQuery)
		}
		page := map[string]any{}
		if request.URL.Query().Get("pageToken") == "" {
			page = map[string]any{
				"plugins": []any{map[string]any{
					"id": "remote-1", "name": "sample", "scope": "GLOBAL",
					"installation_policy": "AVAILABLE", "authentication_policy": "ON_USE", "status": "AVAILABLE",
					"release": map[string]any{"version": "1.2.3", "display_name": "Sample", "description": "desc"},
				}},
				"pagination": map[string]any{"next_page_token": "next"},
			}
		} else {
			page = map[string]any{
				"plugins": []any{map[string]any{
					"id": "remote-2", "name": "second", "scope": "GLOBAL",
					"installation_policy": "AVAILABLE", "authentication_policy": "ON_INSTALL", "status": "DISABLED_BY_ADMIN",
					"release": map[string]any{"version": "2.0.0", "display_name": "Second", "description": "desc"},
				}},
				"pagination": map[string]any{},
			}
		}
		_ = json.NewEncoder(w).Encode(page)
	}))
	defer server.Close()
	home := t.TempDir()
	options := &RemoteCatalogOptions{
		BaseURL: server.URL, AccessToken: "token", AccountID: "account",
		Scope: RemoteCatalogScopeGlobal, Collection: "beta", CodexHome: home,
	}
	plugins, err := FetchRemoteCatalogPlugins(context.Background(), options)
	if err != nil {
		t.Fatalf("FetchRemoteCatalogPlugins() error = %v", err)
	}
	if len(plugins) != 2 || plugins[0].ID != "remote-1" || plugins[1].ID != "remote-2" || plugins[1].Availability != PluginDisabledByAdmin {
		t.Fatalf("plugins = %#v", plugins)
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("requests = %d, want 2", got)
	}
	if _, err := FetchRemoteCatalogPlugins(context.Background(), options); err != nil {
		t.Fatalf("cached FetchRemoteCatalogPlugins() error = %v", err)
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("cached requests = %d, want 2", got)
	}
	options.ForceRefresh = true
	if _, err := FetchRemoteCatalogPlugins(context.Background(), options); err != nil {
		t.Fatalf("forced FetchRemoteCatalogPlugins() error = %v", err)
	}
	if got := requests.Load(); got != 4 {
		t.Fatalf("forced requests = %d, want 4", got)
	}
}

func TestRemotePluginMutationsLikeRust(t *testing.T) {
	var installed, uninstalled bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/ps/plugins/remote-1/install":
			installed = true
			if request.Method != http.MethodPost || request.URL.Query().Get("includeAppsNeedingAuth") != "true" {
				t.Errorf("install request = %s %s", request.Method, request.URL.RawQuery)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "remote-1", "enabled": true, "app_ids_needing_auth": []string{"app-1"}})
		case "/ps/plugins/remote-1/uninstall":
			uninstalled = true
			if request.Method != http.MethodPost {
				t.Errorf("uninstall method = %s", request.Method)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "remote-1", "enabled": false})
		default:
			http.NotFound(w, request)
		}
	}))
	defer server.Close()
	options := &RemoteCatalogOptions{BaseURL: server.URL, AccessToken: "token"}
	apps, err := InstallRemotePluginMutation(context.Background(), options, "remote-1")
	if err != nil || len(apps) != 1 || apps[0] != "app-1" {
		t.Fatalf("install = %#v, %v", apps, err)
	}
	if err := UninstallRemotePluginMutation(context.Background(), options, "remote-1"); err != nil {
		t.Fatalf("uninstall error = %v", err)
	}
	if !installed || !uninstalled {
		t.Fatalf("installed=%v uninstalled=%v", installed, uninstalled)
	}
}
