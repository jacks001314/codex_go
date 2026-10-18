package appserver

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"codex_go/auth"
	"codex_go/config"
	"codex_go/plugin"
)

func TestRemoteInstalledPluginSnapshotValidationPrecedesMutationLikeRust(t *testing.T) {
	home := t.TempDir()
	staleRoot := writeCachedRemotePluginForTest(t, home, remoteInstalledGlobalMarketplace, "stale-plugin")
	var bundleCalls int
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/ps/plugins/installed":
			if request.URL.Query().Get("scope") != "GLOBAL" {
				_, _ = w.Write([]byte(`{"plugins":[],"pagination":{"next_page_token":null}}`))
				return
			}
			_, _ = w.Write([]byte(`{"plugins":[
				{"id":"plugins~Plugin_valid","name":"valid-plugin","scope":"GLOBAL","enabled":true,"release":{"display_name":"Valid","bundle_download_url":"` + "http://" + request.Host + `/bundle"}},
				{"id":"plugins~Plugin_incomplete","name":"","scope":"GLOBAL","enabled":true,"release":{"display_name":"Incomplete"}}
			],"pagination":{"next_page_token":null}}`))
		case "/bundle":
			mu.Lock()
			bundleCalls++
			mu.Unlock()
			http.Error(w, "bundle should not be requested", http.StatusInternalServerError)
		default:
			http.NotFound(w, request)
		}
	}))
	defer server.Close()

	router, _ := newRemoteInstalledPluginTestRouter(t, home, server.URL)
	if _, err := router.reconcileInstalledRemotePlugins(context.Background()); err == nil {
		t.Fatal("incomplete installed-plugin snapshot unexpectedly succeeded")
	}
	mu.Lock()
	calls := bundleCalls
	mu.Unlock()
	if calls != 0 {
		t.Fatalf("bundle downloads = %d, want 0 before snapshot validation", calls)
	}
	if _, err := os.Stat(staleRoot); err != nil {
		t.Fatalf("stale cache was mutated before snapshot validation: %v", err)
	}
}

func TestRemoteInstalledPluginMaterializationFailureKeepsMetadataLikeRust(t *testing.T) {
	home := t.TempDir()
	clearRemoteInstalledPluginSyncFailures()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/ps/plugins/installed" {
			http.NotFound(w, request)
			return
		}
		if request.URL.Query().Get("scope") != "GLOBAL" {
			_, _ = w.Write([]byte(`{"plugins":[],"pagination":{"next_page_token":null}}`))
			return
		}
		_, _ = w.Write([]byte(`{"plugins":[{
			"id":"plugins~Plugin_missing",
			"name":"missing-version",
			"scope":"GLOBAL",
			"enabled":true,
			"release":{"display_name":"Missing version","description":"Metadata remains installed"}
		}],"pagination":{"next_page_token":null}}`))
	}))
	defer server.Close()

	router, plugins := newRemoteInstalledPluginTestRouter(t, home, server.URL)
	refreshed, err := router.reconcileInstalledRemotePlugins(context.Background())
	if err != nil || !refreshed {
		t.Fatalf("reconcile = refreshed:%t err:%v", refreshed, err)
	}
	if got := installedRemoteMetadataDisplayName(t, plugins, "plugins~Plugin_missing"); got != "Missing version" {
		t.Fatalf("installed metadata display name = %q, want Missing version", got)
	}
	_, failedMaterialization := takeRemoteInstalledPluginSyncFailures()
	if len(failedMaterialization) != 1 || failedMaterialization[0] != "plugins~Plugin_missing" {
		t.Fatalf("failed materialization ids = %#v", failedMaterialization)
	}
}

func TestRemoteInstalledPluginSyncDiscardsStaleAccountGenerationLikeRust(t *testing.T) {
	home := t.TempDir()
	requestArrived := make(chan struct{})
	releaseResponse := make(chan struct{})
	var once sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/ps/plugins/installed" {
			http.NotFound(w, request)
			return
		}
		if request.URL.Query().Get("scope") != "GLOBAL" {
			_, _ = w.Write([]byte(`{"plugins":[],"pagination":{"next_page_token":null}}`))
			return
		}
		once.Do(func() { close(requestArrived) })
		<-releaseResponse
		_, _ = w.Write([]byte(`{"plugins":[{
			"id":"plugins~Plugin_stale",
			"name":"stale-plugin",
			"scope":"GLOBAL",
			"enabled":true,
			"release":{"display_name":"Stale account plugin"}
		}],"pagination":{"next_page_token":null}}`))
	}))
	defer server.Close()

	router, plugins := newRemoteInstalledPluginTestRouter(t, home, server.URL)
	type reconcileResult struct {
		refreshed bool
		err       error
	}
	done := make(chan reconcileResult, 1)
	go func() {
		refreshed, err := router.reconcileInstalledRemotePlugins(context.Background())
		done <- reconcileResult{refreshed: refreshed, err: err}
	}()
	<-requestArrived
	advanceRemoteInstalledPluginCacheGeneration(home)
	close(releaseResponse)
	result := <-done
	if result.err != nil {
		t.Fatalf("stale reconcile error = %v", result.err)
	}
	if result.refreshed {
		t.Fatal("a sync fetched under an older account generation was published")
	}
	for _, detail := range plugins.InstalledDetails() {
		if detail.Summary.RemotePluginID == "plugins~Plugin_stale" {
			t.Fatal("stale account plugin metadata was published")
		}
	}
}

func TestRemoteInstalledPluginMutationGateSerializesSyncLikeRust(t *testing.T) {
	home := t.TempDir()
	releaseMutation := acquireRemoteInstalledPluginCacheMutation(home)
	if _, _, ok := tryAcquireRemoteInstalledPluginSync(home); ok {
		t.Fatal("bundle sync acquired the gate while a direct mutation held it")
	}
	releaseMutation()
	_, releaseSync, ok := tryAcquireRemoteInstalledPluginSync(home)
	if !ok {
		t.Fatal("bundle sync could not acquire the released gate")
	}
	releaseSync()
}

func TestAuthOwnerChangeClearsRemoteInstalledPluginMetadataLikeRust(t *testing.T) {
	home := t.TempDir()
	plugins := plugin.NewPluginService()
	plugins.SetCodexHome(home)
	plugins.ReplaceInstalledRemotePlugins(remoteInstalledGlobalMarketplace, []plugin.PluginDetail{{
		MarketplaceName: remoteInstalledGlobalMarketplace,
		Summary: plugin.PluginSummary{
			ID:              "old-account@openai-curated-remote",
			Name:            "old-account",
			MarketplaceName: remoteInstalledGlobalMarketplace,
			RemotePluginID:  "plugins~Plugin_old_account",
			Installed:       true,
			Enabled:         true,
		},
	}})
	router := NewRuntimeRouter(RuntimeServices{Plugins: plugins})
	router.clearRemoteInstalledPluginMetadataForAuthChange()
	for _, detail := range plugins.InstalledDetails() {
		if detail.Summary.MarketplaceName == remoteInstalledGlobalMarketplace {
			t.Fatalf("old account metadata remained installed: %#v", detail.Summary)
		}
	}
}

func TestRemoteInstalledPluginSyncRetriesAfterCancellationLikeRust(t *testing.T) {
	home := t.TempDir()
	var calls atomic.Int32
	firstRequest := make(chan struct{})
	var once sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/ps/plugins/installed" {
			http.NotFound(w, request)
			return
		}
		if request.URL.Query().Get("scope") != "GLOBAL" {
			_, _ = w.Write([]byte(`{"plugins":[],"pagination":{"next_page_token":null}}`))
			return
		}
		if calls.Add(1) == 1 {
			once.Do(func() { close(firstRequest) })
			<-request.Context().Done()
			return
		}
		_, _ = w.Write([]byte(`{"plugins":[{
			"id":"plugins~Plugin_retry",
			"name":"retry-plugin",
			"scope":"GLOBAL",
			"enabled":true,
			"release":{"display_name":"Retry plugin"}
		}],"pagination":{"next_page_token":null}}`))
	}))
	defer server.Close()

	router, plugins := newRemoteInstalledPluginTestRouter(t, home, server.URL)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := router.reconcileInstalledRemotePlugins(ctx)
		done <- err
	}()
	<-firstRequest
	cancel()
	if err := <-done; err == nil {
		t.Fatal("cancelled sync unexpectedly succeeded")
	}
	refreshed, err := router.reconcileInstalledRemotePlugins(context.Background())
	if err != nil || !refreshed {
		t.Fatalf("retry reconcile = refreshed:%t err:%v", refreshed, err)
	}
	if got := installedRemoteMetadataDisplayName(t, plugins, "plugins~Plugin_retry"); got != "Retry plugin" {
		t.Fatalf("retry metadata display name = %q", got)
	}
}

func newRemoteInstalledPluginTestRouter(t *testing.T, home string, serverURL string) (*RuntimeRouter, *plugin.PluginService) {
	t.Helper()
	if err := os.WriteFile(config.ConfigPath(home), []byte(fmt.Sprintf("chatgpt_base_url = %q\n\n[features]\nplugins = true\nremote_plugin = true\n", serverURL)), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}
	account := auth.NewAccountManager()
	if _, err := account.Login(&auth.LoginAccountParams{
		Type:             "chatgptAuthTokens",
		AccessToken:      "chatgpt-token",
		ChatGPTAccountID: "account-123",
	}); err != nil {
		t.Fatalf("Login(chatgptAuthTokens) error = %v", err)
	}
	plugins := plugin.NewPluginService()
	plugins.SetCodexHome(home)
	router := NewRuntimeRouter(RuntimeServices{
		Config:      config.NewConfigService(home),
		Account:     account,
		AccountHTTP: http.DefaultClient,
		Skills:      NewSkillsService(nil),
		Plugins:     plugins,
	})
	return router, plugins
}
