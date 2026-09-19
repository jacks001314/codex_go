package appserver

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"codex_go/auth"
	"codex_go/chatgptapi"
	"codex_go/config"
	"codex_go/model"
	"codex_go/turn"
)

func workspaceRoutingRouter(t *testing.T, handler http.HandlerFunc) *RuntimeRouter {
	t.Helper()
	return workspaceRoutingRouterWithHome(t, t.TempDir(), handler)
}

func workspaceRoutingRouterWithHome(t *testing.T, home string, handler http.HandlerFunc) *RuntimeRouter {
	t.Helper()
	clearAuthEnvAppserver(t)
	if err := auth.NewStore(home).Save(auth.FromChatGPTAuthTokens("chatgpt-token", "workspace", nil)); err != nil {
		t.Fatalf("auth save error: %v", err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	if err := os.WriteFile(config.ConfigPath(home), []byte(`chatgpt_base_url = "`+server.URL+`"`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return NewRuntimeRouter(RuntimeServices{
		Account: auth.NewAccountManager(),
		Config:  config.NewConfigService(home),
	})
}

// TestRuntimeRouterGetAccountExposesWorkspaceRoutingLikeRust mirrors Rust
// #46281: account/read returns the discovered workspace routing and caches it
// per credential generation.
func TestRuntimeRouterGetAccountExposesWorkspaceRoutingLikeRust(t *testing.T) {
	requests := 0
	router := workspaceRoutingRouter(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/codex/accounts/check" {
			t.Fatalf("accounts check path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer chatgpt-token" {
			t.Fatalf("Authorization = %q", got)
		}
		if got := r.Header.Get("ChatGPT-Account-ID"); got != "workspace" {
			t.Fatalf("ChatGPT-Account-ID = %q", got)
		}
		requests++
		writeJSON(t, w, map[string]any{
			"accounts": []any{map[string]any{
				"id":                       "workspace",
				"workspace_backend_origin": "https://gov.chatgpt.com",
				"account_routing_override": "us_cr",
			}},
		})
	})

	response := router.Handle(requestWithParams(t, IntID(1), MethodGetAccount, auth.GetAccountParams{}))
	if response.Error != nil {
		t.Fatalf("get account = %+v", response.Error)
	}
	account := response.Result.(*auth.GetAccountResponse)
	if account.WorkspaceRouting == nil {
		t.Fatalf("workspace routing missing: %+v", account)
	}
	if account.WorkspaceRouting.ChatGPTAccountID != "workspace" ||
		account.WorkspaceRouting.BackendOrigin != "https://gov.chatgpt.com" ||
		account.WorkspaceRouting.AccountRoutingOverride != auth.AccountRoutingOverrideUSCR {
		t.Fatalf("workspace routing = %+v", account.WorkspaceRouting)
	}

	// The second read reuses the cached discovery for the same key.
	response = router.Handle(requestWithParams(t, IntID(2), MethodGetAccount, auth.GetAccountParams{}))
	if response.Error != nil {
		t.Fatalf("second get account = %+v", response.Error)
	}
	if requests != 1 {
		t.Fatalf("accounts/check requests = %d, want 1 (cached)", requests)
	}
}

func TestRuntimeRouterGetAccountWorkspaceRoutingErrorsLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		body    map[string]any
		status  int
		message string
	}{
		{
			name:    "missing workspace",
			body:    map[string]any{"accounts": []any{map[string]any{"id": "other"}}},
			message: "selected workspace missing from routing discovery",
		},
		{
			name: "duplicate workspace",
			body: map[string]any{"accounts": []any{
				map[string]any{"id": "workspace", "workspace_backend_origin": "https://chatgpt.com", "account_routing_override": "us"},
				map[string]any{"id": "workspace", "workspace_backend_origin": "https://chatgpt.com", "account_routing_override": "us"},
			}},
			message: "duplicate workspace in routing discovery",
		},
		{
			name:    "missing backend origin",
			body:    map[string]any{"accounts": []any{map[string]any{"id": "workspace", "account_routing_override": "us"}}},
			message: "workspace routing discovery missing backend origin",
		},
		{
			name:    "invalid override",
			body:    map[string]any{"accounts": []any{map[string]any{"id": "workspace", "workspace_backend_origin": "https://chatgpt.com", "account_routing_override": "unknown"}}},
			message: "workspace routing discovery has invalid account routing override",
		},
		{
			name:    "backend is not an origin",
			body:    map[string]any{"accounts": []any{map[string]any{"id": "workspace", "workspace_backend_origin": "https://chatgpt.com/backend-api", "account_routing_override": "us"}}},
			message: "workspace routing discovery must return an origin",
		},
		{
			name:    "unauthorized",
			status:  http.StatusUnauthorized,
			message: "workspace routing discovery unauthorized (401)",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			router := workspaceRoutingRouter(t, func(w http.ResponseWriter, r *http.Request) {
				if testCase.status != 0 {
					w.WriteHeader(testCase.status)
					return
				}
				writeJSON(t, w, testCase.body)
			})
			response := router.Handle(requestWithParams(t, IntID(1), MethodGetAccount, auth.GetAccountParams{}))
			if response.Error == nil || response.Error.Code != JSONRPCInternalErrorCode || response.Error.Message != testCase.message {
				t.Fatalf("response = %+v, want internal error %q", response.Error, testCase.message)
			}
		})
	}
}

func TestResolveWorkspaceRoutingLikeRust(t *testing.T) {
	// A connection that initializes with a ChatGPT credential learns the
	// discovered routing through account/updated (Rust
	// notify_workspace_routing_to_connection).
	t.Run("connection initialize notifies account updated", func(t *testing.T) {
		router := workspaceRoutingRouter(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/codex/accounts/check" {
				t.Fatalf("accounts check path = %q", r.URL.Path)
			}
			writeJSON(t, w, map[string]any{"accounts": []any{map[string]any{
				"id":                       "workspace",
				"workspace_backend_origin": "https://gov.chatgpt.com",
				"account_routing_override": "us",
			}}})
		})
		sink := NewNotificationBuffer()
		router.SetNotificationSink(sink)

		router.notifyWorkspaceRoutingToConnection("conn-1")
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if sinkHasMethod(sink, NotificationAccountUpdated) {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("workspace routing did not notify account/updated: %+v", sink.List())
	})

	// A connection without a ChatGPT credential never discovers routing.
	t.Run("no chatgpt credential stays silent", func(t *testing.T) {
		clearAuthEnvAppserver(t)
		home := t.TempDir()
		if err := auth.NewStore(home).Save(auth.FromAPIKey("sk-test")); err != nil {
			t.Fatalf("auth save error: %v", err)
		}
		router := NewRuntimeRouter(RuntimeServices{Account: auth.NewAccountManager(), Config: config.NewConfigService(home)})
		sink := NewNotificationBuffer()
		router.SetNotificationSink(sink)

		router.notifyWorkspaceRoutingToConnection("conn-1")
		time.Sleep(100 * time.Millisecond)
		if sinkHasMethod(sink, NotificationAccountUpdated) {
			t.Fatalf("api-key connection received account/updated: %+v", sink.List())
		}
	})

	// The turn's Responses provider is rewritten to the discovered workspace
	// backend (Rust RuntimeProvider::responses_api_provider).
	t.Run("responses agent applies routing", func(t *testing.T) {
		router := workspaceRoutingRouter(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/codex/accounts/check" {
				t.Fatalf("accounts check path = %q", r.URL.Path)
			}
			writeJSON(t, w, map[string]any{"accounts": []any{map[string]any{
				"id":                       "workspace",
				"workspace_backend_origin": "https://gov.chatgpt.com",
				"account_routing_override": "us",
			}}})
		})
		agent, err := router.responsesAgentForTurn(&turn.TurnStartParams{ThreadID: "workspace-routing-turn", CWD: t.TempDir()})
		if err != nil {
			t.Fatalf("responsesAgentForTurn error = %v", err)
		}
		if agent == nil || agent.Provider == nil {
			t.Fatalf("agent = %+v", agent)
		}
		if got := agent.Provider.BaseURL; got != "https://gov.chatgpt.com/backend-api/codex" {
			t.Fatalf("routed BaseURL = %q", got)
		}
		if got := agent.Provider.Headers.Get(model.AccountRoutingHeader); got != "us" {
			t.Fatalf("routing header = %q", got)
		}
		if !agent.RejectRedirects {
			t.Fatal("routed provider must reject redirects")
		}
	})
}

func TestResolveWorkspaceRoutingMatchesRust(t *testing.T) {
	origin := func(value string) *string { return &value }
	override := func(value string) *string { return &value }
	for _, testCase := range []struct {
		name            string
		entry           chatgptapi.AccountsCheckEntry
		requiredBaseURL string
		effective       string
		wantOrigin      string
		wantOverride    auth.AccountRoutingOverride
		wantErr         error
	}{
		{
			name:       "no constraint falls back to the effective backend",
			entry:      chatgptapi.AccountsCheckEntry{ID: "workspace", WorkspaceBackendOrigin: origin("NO_CONSTRAINT"), AccountRoutingOverride: override("NO_CONSTRAINT")},
			effective:  "https://chatgpt.com/backend-api",
			wantOrigin: "https://chatgpt.com",
		},
		{
			name:            "required backend wins when it matches the discovered origin",
			entry:           chatgptapi.AccountsCheckEntry{ID: "workspace", WorkspaceBackendOrigin: origin("https://gov.chatgpt.com/"), AccountRoutingOverride: override("us")},
			requiredBaseURL: "https://gov.chatgpt.com/backend-api/",
			effective:       "https://chatgpt.com/backend-api",
			wantOrigin:      "https://gov.chatgpt.com",
			wantOverride:    auth.AccountRoutingOverrideUS,
		},
		{
			name:            "required backend conflict",
			entry:           chatgptapi.AccountsCheckEntry{ID: "workspace", WorkspaceBackendOrigin: origin("https://gov.chatgpt.com"), AccountRoutingOverride: override("us")},
			requiredBaseURL: "https://chatgpt.com/backend-api/",
			effective:       "https://chatgpt.com/backend-api",
			wantErr:         model.ErrWorkspaceRoutingBackendConflict,
		},
		{
			name:    "missing backend origin",
			entry:   chatgptapi.AccountsCheckEntry{ID: "workspace", AccountRoutingOverride: override("us")},
			wantErr: model.ErrWorkspaceRoutingMissingBackendOrigin,
		},
		{
			name:    "missing override",
			entry:   chatgptapi.AccountsCheckEntry{ID: "workspace", WorkspaceBackendOrigin: origin("https://chatgpt.com")},
			wantErr: model.ErrWorkspaceRoutingInvalidOverride,
		},
		{
			name:    "insecure backend origin",
			entry:   chatgptapi.AccountsCheckEntry{ID: "workspace", WorkspaceBackendOrigin: origin("http://chatgpt.com"), AccountRoutingOverride: override("us")},
			wantErr: model.ErrWorkspaceRoutingInvalidBackendOrigin,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := model.ResolveWorkspaceRouting(testCase.entry, testCase.requiredBaseURL, testCase.effective)
			if testCase.wantErr != nil {
				if err == nil || !strings.Contains(err.Error(), testCase.wantErr.Error()) {
					t.Fatalf("resolveWorkspaceRouting error = %v, want %v", err, testCase.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveWorkspaceRouting error = %v", err)
			}
			wantOverride := testCase.wantOverride
			if wantOverride == "" {
				wantOverride = auth.AccountRoutingOverrideNoConstraint
			}
			if got.BackendOrigin != testCase.wantOrigin || got.AccountRoutingOverride != wantOverride {
				t.Fatalf("routing = %+v, want origin %q override %q", got, testCase.wantOrigin, wantOverride)
			}
		})
	}
}

// TestRuntimeRouterWorkspaceRoutingRejectsCredentialChangeLikeRust mirrors Rust
// AuthManager::workspace_routing: a discovery that survives an owner change must
// not be used, because the credential may now belong to another workspace.
func TestRuntimeRouterWorkspaceRoutingRejectsCredentialChangeLikeRust(t *testing.T) {
	var router *RuntimeRouter
	router = workspaceRoutingRouter(t, func(w http.ResponseWriter, r *http.Request) {
		// Switch the credential owner while the discovery request is in flight.
		changed := auth.FromChatGPTAuthTokens("chatgpt-token", "other-workspace", nil)
		router.requireAccount().ApplyAuthSnapshot(&changed)
		router.noteAuthChanged()
		writeJSON(t, w, map[string]any{"accounts": []any{map[string]any{
			"id":                       "workspace",
			"workspace_backend_origin": "https://gov.chatgpt.com",
			"account_routing_override": "us",
		}}})
	})

	response := router.Handle(requestWithParams(t, IntID(1), MethodGetAccount, auth.GetAccountParams{}))
	if response.Error == nil || response.Error.Message != model.ErrWorkspaceRoutingAccountChanged.Error() {
		t.Fatalf("response = %+v, want %q", response.Error, model.ErrWorkspaceRoutingAccountChanged)
	}
}

// TestRuntimeRouterWorkspaceRoutingRejectsConfigChangeLikeRust mirrors Rust
// read_account's post-discovery reload: a configuration change during discovery
// (here the model provider) refuses the discovered routing.
func TestRuntimeRouterWorkspaceRoutingRejectsConfigChangeLikeRust(t *testing.T) {
	home := t.TempDir()
	var configPath string
	router := workspaceRoutingRouterWithHome(t, home, func(w http.ResponseWriter, r *http.Request) {
		// Move the configuration scope while the discovery request is in flight.
		existing, err := os.ReadFile(configPath)
		if err != nil {
			t.Fatalf("read config: %v", err)
		}
		if err := os.WriteFile(configPath, append(existing, []byte("\nmodel_provider = \"other-provider\"\n")...), 0o600); err != nil {
			t.Fatalf("rewrite config: %v", err)
		}
		writeJSON(t, w, map[string]any{"accounts": []any{map[string]any{
			"id":                       "workspace",
			"workspace_backend_origin": "https://gov.chatgpt.com",
			"account_routing_override": "us",
		}}})
	})
	configPath = config.ConfigPath(home)

	response := router.Handle(requestWithParams(t, IntID(1), MethodGetAccount, auth.GetAccountParams{}))
	if response.Error == nil || response.Error.Message != model.ErrWorkspaceRoutingConfigurationChanged.Error() {
		t.Fatalf("response = %+v, want %q", response.Error, model.ErrWorkspaceRoutingConfigurationChanged)
	}
}
