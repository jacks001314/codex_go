package appserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"codex_go/auth"
	"codex_go/config"
)

// Rust parity: app-server/src/request_processors/account_processor/
// workspace_routing.rs (#52635, "Skip background workspace routing reads without
// ChatGPT auth").
//
// Background routing reads - app-server startup and connection initialization -
// used to reload configuration even when signed out or on an API key, although
// routing requires ChatGPT authentication. Rust's
// read_account_for_background_workspace_routing now returns early on the cached
// credential, so nothing is read; explicit account/read keeps reading and
// reloading configuration.
//
// The Go probes: the accounts/check call count is the "did a routing request go
// out" probe, and the credential store is the "did this read configuration"
// probe, because a configuration read enforces forced_login_method and would log
// an API-key credential out.

// workspaceRoutingBackgroundRouter wires a router over an httptest backend that
// counts workspace routing requests, with extraConfig prepended to config.toml.
func workspaceRoutingBackgroundRouter(t *testing.T, home string, extraConfig string, calls *int32) *RuntimeRouter {
	t.Helper()
	clearAuthEnvAppserver(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/codex/accounts/check" {
			t.Errorf("workspace routing request path = %q", r.URL.Path)
			return
		}
		atomic.AddInt32(calls, 1)
		writeJSON(t, w, map[string]any{"accounts": []any{map[string]any{
			"id":                       "workspace",
			"workspace_backend_origin": "https://gov.chatgpt.com",
			"account_routing_override": "us",
		}}})
	}))
	t.Cleanup(server.Close)
	body := strings.TrimSpace(extraConfig)
	if body != "" {
		body += "\n"
	}
	body += "chatgpt_base_url = \"" + server.URL + "\"\n"
	if err := os.WriteFile(config.ConfigPath(home), []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return NewRuntimeRouter(RuntimeServices{
		Account: auth.NewAccountManager(),
		Config:  config.NewConfigService(home),
	})
}

// TestWorkspaceRoutingForBackgroundReadSkipsWithoutChatGPTAuthLikeRust mirrors
// Rust background_routing_call_sites_skip_config: signed out or on an API key the
// background read returns nothing, without touching the backend.
func TestWorkspaceRoutingForBackgroundReadSkipsWithoutChatGPTAuthLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		apiKey string
	}{
		{name: "signed_out"},
		{name: "api_key", apiKey: "sk-test-background"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			home := t.TempDir()
			var calls int32
			router := workspaceRoutingBackgroundRouter(t, home, "", &calls)
			if testCase.apiKey != "" {
				if err := auth.NewStore(home).Save(auth.FromAPIKey(testCase.apiKey)); err != nil {
					t.Fatalf("auth save error: %v", err)
				}
			}
			routing, err := router.workspaceRoutingForBackgroundRead(context.Background())
			if routing != nil || err != nil {
				t.Fatalf("background read = (%+v, %v), want the read skipped", routing, err)
			}
			if got := atomic.LoadInt32(&calls); got != 0 {
				t.Fatalf("accounts/check requests = %d, want 0", got)
			}
		})
	}
}

// TestWorkspaceRoutingForBackgroundReadDoesNotReadConfigurationLikeRust pins the
// reason the skip matters: the configuration is not read at all. A configuration
// read enforces the forced login method, so an API-key credential under
// forced_login_method = "chatgpt" is logged out by that read - and stays logged in
// when the background read is skipped.
func TestWorkspaceRoutingForBackgroundReadDoesNotReadConfigurationLikeRust(t *testing.T) {
	home := t.TempDir()
	var calls int32
	router := workspaceRoutingBackgroundRouter(t, home, "forced_login_method = \"chatgpt\"", &calls)
	store := auth.NewStore(home)
	if err := store.Save(auth.FromAPIKey("sk-test-background")); err != nil {
		t.Fatalf("auth save error: %v", err)
	}
	routing, err := router.workspaceRoutingForBackgroundRead(context.Background())
	if routing != nil || err != nil {
		t.Fatalf("background read = (%+v, %v), want the read skipped", routing, err)
	}
	if got := atomic.LoadInt32(&calls); got != 0 {
		t.Fatalf("accounts/check requests = %d, want 0", got)
	}
	if _, statErr := os.Stat(store.Path()); statErr != nil {
		t.Fatalf("background read read the configuration and logged the credential out: %v", statErr)
	}
}

// TestWorkspaceRoutingForBackgroundReadResolvesAfterChatGPTSignInLikeRust pins the
// switchable state: the same router skips the read on an API key and resolves
// exactly one routing request once ChatGPT signs in.
func TestWorkspaceRoutingForBackgroundReadResolvesAfterChatGPTSignInLikeRust(t *testing.T) {
	home := t.TempDir()
	var calls int32
	router := workspaceRoutingBackgroundRouter(t, home, "", &calls)
	store := auth.NewStore(home)
	if err := store.Save(auth.FromAPIKey("sk-test-background")); err != nil {
		t.Fatalf("auth save error: %v", err)
	}
	if routing, err := router.workspaceRoutingForBackgroundRead(context.Background()); routing != nil || err != nil {
		t.Fatalf("api-key background read = (%+v, %v), want the read skipped", routing, err)
	}
	if got := atomic.LoadInt32(&calls); got != 0 {
		t.Fatalf("api-key accounts/check requests = %d, want 0", got)
	}

	if err := store.Save(auth.FromChatGPTAuthTokens("chatgpt-token", "workspace", nil)); err != nil {
		t.Fatalf("chatgpt auth save error: %v", err)
	}
	routing, err := router.workspaceRoutingForBackgroundRead(context.Background())
	if err != nil {
		t.Fatalf("chatgpt background read error = %v", err)
	}
	if routing == nil || routing.ChatGPTAccountID != "workspace" || routing.BackendOrigin != "https://gov.chatgpt.com" {
		t.Fatalf("chatgpt background read = %+v", routing)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("chatgpt accounts/check requests = %d, want 1", got)
	}
}

// TestNotifyWorkspaceRoutingToConnectionSkipsWithoutChatGPTAuthLikeRust drives the
// real background call site (connection initialization) for all three credential
// states.
func TestNotifyWorkspaceRoutingToConnectionSkipsWithoutChatGPTAuthLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		mode    string
		apiKey  string
		wantCue bool
	}{
		{name: "signed_out"},
		{name: "api_key", mode: "api-key", apiKey: "sk-test-background"},
		{name: "chatgpt", mode: "chatgpt", wantCue: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			home := t.TempDir()
			var calls int32
			router := workspaceRoutingBackgroundRouter(t, home, "", &calls)
			store := auth.NewStore(home)
			switch testCase.mode {
			case "api-key":
				if err := store.Save(auth.FromAPIKey(testCase.apiKey)); err != nil {
					t.Fatalf("auth save error: %v", err)
				}
			case "chatgpt":
				if err := store.Save(auth.FromChatGPTAuthTokens("chatgpt-token", "workspace", nil)); err != nil {
					t.Fatalf("auth save error: %v", err)
				}
			}
			sink := NewNotificationBuffer()
			router.SetNotificationSink(sink)

			router.notifyWorkspaceRoutingToConnection("conn-background")
			if testCase.wantCue {
				deadline := time.Now().Add(3 * time.Second)
				for time.Now().Before(deadline) && !sinkHasMethod(sink, NotificationAccountUpdated) {
					time.Sleep(10 * time.Millisecond)
				}
				if !sinkHasMethod(sink, NotificationAccountUpdated) {
					t.Fatalf("workspace routing did not notify account/updated: %+v", sink.List())
				}
				if got := atomic.LoadInt32(&calls); got != 1 {
					t.Fatalf("chatgpt accounts/check requests = %d, want 1", got)
				}
				return
			}
			// The spawned read either returns immediately (skipped) or, in the
			// broken variant, reaches configuration and the backend. Give it a
			// bounded window before taking the negative assertions.
			time.Sleep(300 * time.Millisecond)
			if sinkHasMethod(sink, NotificationAccountUpdated) {
				t.Fatalf("connection without a ChatGPT credential received account/updated: %+v", sink.List())
			}
			if got := atomic.LoadInt32(&calls); got != 0 {
				t.Fatalf("accounts/check requests = %d, want 0", got)
			}
		})
	}
}

// TestRuntimeRouterGetAccountStillReadsConfigurationLikeRust pins the half the
// upstream change deliberately preserved: explicit account/read keeps discovering
// routing (and reloading configuration) with a ChatGPT credential.
func TestRuntimeRouterGetAccountStillReadsConfigurationLikeRust(t *testing.T) {
	home := t.TempDir()
	var calls int32
	router := workspaceRoutingBackgroundRouter(t, home, "", &calls)
	if err := auth.NewStore(home).Save(auth.FromChatGPTAuthTokens("chatgpt-token", "workspace", nil)); err != nil {
		t.Fatalf("auth save error: %v", err)
	}
	response := router.Handle(requestWithParams(t, IntID(1), MethodGetAccount, auth.GetAccountParams{}))
	if response.Error != nil {
		t.Fatalf("get account error = %+v", response.Error)
	}
	account, ok := response.Result.(*auth.GetAccountResponse)
	if !ok || account == nil || account.WorkspaceRouting == nil {
		t.Fatalf("get account result = %+v, want a resolved workspace routing", response.Result)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("explicit account/read accounts/check requests = %d, want 1", got)
	}
	// The cached discovery still serves a later background read.
	routing, err := router.workspaceRoutingForBackgroundRead(context.Background())
	if err != nil || routing == nil {
		t.Fatalf("background read after account/read = (%+v, %v)", routing, err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("cached background read issued %d requests, want 0 more", got)
	}
}
