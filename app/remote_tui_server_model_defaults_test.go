package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	"codex_go/cli"
	codextui "codex_go/tui"

	"github.com/coder/websocket"
)

// TestRemoteFreshStartUsesServerModelDefaultsLikeRust covers Rust #50013
// (b527ce4734, "Honor server model defaults when starting fresh TUI threads")
// and #50913 (c2f7fe89d8): a connected TUI fresh start without an explicit
// profile lets the app server own the new-thread model, so neither an implicit
// client model nor an implicit reasoning effort is forwarded to thread/start,
// and the server's configured model/catalog default are not echoed back as
// explicit overrides. Explicit launch choices and the managed new-thread
// defaults still apply, and an explicit profile keeps the resolved path.
//
// Rust tests:
// app/tests/startup_defaults_tests.rs::fresh_startup_uses_server_defaults_with_explicit_and_managed_precedence
// and ::fresh_startup_reads_destination_and_cleared_model_uses_catalog (which
// asserts the stale client model never reaches the request: `latest["model"] ==
// serde_json::Value::Null` and no `model_reasoning_effort`).
func TestRemoteFreshStartUsesServerModelDefaultsLikeRust(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())

	managedRequirements := map[string]any{
		"models": map[string]any{
			"newThread": map[string]any{
				"model":                "managed-model",
				"modelReasoningEffort": "medium",
			},
		},
	}
	catalogDefault := []any{map[string]any{"model": "catalog-model", "isDefault": true}}

	cases := []struct {
		name         string
		serverConfig map[string]any
		requirements map[string]any
		catalog      []any
		root         func() *cli.RootOptions
		state        *codextui.State
		wantModel    string
		wantEffort   string
		wantProvider string
	}{
		{
			// The Rust #50013 regression: the client's stale settings used to seed
			// the request before (or without) the server configuration read.
			name:         "stale client model is not forwarded",
			serverConfig: map[string]any{},
			state:        codextui.NewState(&codextui.Options{Model: "stale-client-model", ReasoningEffort: "low"}),
			wantModel:    "",
			wantEffort:   "",
		},
		{
			// Rust #50013: the server's own configuration is not echoed back as an
			// explicit model/effort override; the server resolves it.
			name:         "server configuration stays server-owned",
			serverConfig: map[string]any{"model": "server-model", "model_reasoning_effort": "high"},
			state:        codextui.NewState(&codextui.Options{Model: "stale-client-model", ReasoningEffort: "low"}),
			wantModel:    "",
			wantEffort:   "",
		},
		{
			name:         "server catalog default is not forwarded",
			serverConfig: map[string]any{},
			catalog:      catalogDefault,
			state:        codextui.NewState(&codextui.Options{Model: "stale-client-model", ReasoningEffort: "low"}),
			wantModel:    "",
			wantEffort:   "",
		},
		{
			name:         "managed new-thread default supplies a model with an empty catalog",
			serverConfig: map[string]any{},
			requirements: managedRequirements,
			state:        codextui.NewState(&codextui.Options{Model: "stale-client-model", ReasoningEffort: "low"}),
			wantModel:    "managed-model",
			wantEffort:   "medium",
		},
		{
			// The launch's own `-m` choice is explicit: `interactiveUIState`
			// resolves it into the live state and the launch flag preserves it.
			name:         "explicit harness model survives",
			serverConfig: map[string]any{},
			root: func() *cli.RootOptions {
				root := &cli.RootOptions{}
				root.Shared.Model = "cli-model"
				return root
			},
			state:      codextui.NewState(&codextui.Options{Model: "cli-model", ReasoningEffort: "low"}),
			wantModel:  "cli-model",
			wantEffort: "",
		},
		{
			// Rust #50013: repeated `-c model=` assignments keep the last value.
			name:         "repeated generic model override keeps the last value",
			serverConfig: map[string]any{},
			root: func() *cli.RootOptions {
				return &cli.RootOptions{ConfigOverrides: []string{"model=earlier-model", "model=kv-model"}}
			},
			state:      codextui.NewState(&codextui.Options{Model: "earlier-model", ReasoningEffort: "low"}),
			wantModel:  "kv-model",
			wantEffort: "",
		},
		{
			name:         "explicit reasoning effort survives",
			serverConfig: map[string]any{"model": "server-model", "model_reasoning_effort": "high"},
			root: func() *cli.RootOptions {
				return &cli.RootOptions{ConfigOverrides: []string{"model_reasoning_effort=low"}}
			},
			state:      codextui.NewState(&codextui.Options{Model: "stale-client-model", ReasoningEffort: "low"}),
			wantModel:  "",
			wantEffort: "low",
		},
		{
			name:         "explicit typed provider survives",
			serverConfig: map[string]any{},
			root: func() *cli.RootOptions {
				root := &cli.RootOptions{}
				root.Shared.OSSProvider = "openai"
				return root
			},
			state:        codextui.NewState(&codextui.Options{Model: "stale-client-model", Provider: "openai", ReasoningEffort: "low"}),
			wantProvider: "openai",
		},
		{
			name:         "explicit generic provider override survives",
			serverConfig: map[string]any{},
			root: func() *cli.RootOptions {
				return &cli.RootOptions{ConfigOverrides: []string{"model_provider=openai"}}
			},
			state:        codextui.NewState(&codextui.Options{Model: "stale-client-model", Provider: "openai", ReasoningEffort: "low"}),
			wantProvider: "openai",
		},
		{
			name:         "explicit profile keeps its resolved client model",
			serverConfig: map[string]any{},
			root: func() *cli.RootOptions {
				root := &cli.RootOptions{}
				root.Shared.Profile = "work"
				return root
			},
			state:      codextui.NewState(&codextui.Options{Model: "profile-model", ReasoningEffort: "low"}),
			wantModel:  "profile-model",
			wantEffort: "low",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			paramsCh := make(chan map[string]any, 1)
			serverErrs := make(chan error, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := websocket.Accept(w, r, nil)
				if err != nil {
					remoteTUITestSendErr(serverErrs, err)
					return
				}
				defer conn.Close(websocket.StatusNormalClosure, "")
				for {
					req, err := remoteTUITestReadRequest(ctx, conn)
					if err != nil {
						return
					}
					switch req.Method {
					case string(appserver.MethodInitialize):
						remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}})
					case string(appserver.MethodConfigRead):
						remoteTUITestWrite(ctx, conn, map[string]any{
							"jsonrpc": "2.0",
							"id":      req.ID,
							"result":  map[string]any{"config": tc.serverConfig, "origins": map[string]any{}},
						})
					case string(appserver.MethodConfigRequirementsRead):
						requirements := tc.requirements
						if requirements == nil {
							requirements = map[string]any{}
						}
						remoteTUITestWrite(ctx, conn, map[string]any{
							"jsonrpc": "2.0",
							"id":      req.ID,
							"result":  map[string]any{"requirements": requirements},
						})
					case string(appserver.MethodModelList):
						catalog := tc.catalog
						if catalog == nil {
							catalog = []any{}
						}
						remoteTUITestWrite(ctx, conn, map[string]any{
							"jsonrpc": "2.0",
							"id":      req.ID,
							"result":  map[string]any{"data": catalog, "nextCursor": nil},
						})
					case string(appserver.MethodThreadStart):
						var params map[string]any
						if err := json.Unmarshal(req.Params, &params); err != nil {
							remoteTUITestSendErr(serverErrs, err)
							return
						}
						paramsCh <- params
						remoteTUITestWrite(ctx, conn, map[string]any{
							"jsonrpc": "2.0",
							"id":      req.ID,
							"result":  map[string]any{"thread": map[string]any{"id": "thread-server-model"}},
						})
					default:
						remoteTUITestSendErr(serverErrs, fmt.Errorf("unexpected method %s", req.Method))
						return
					}
				}
			}))
			defer server.Close()

			endpoint := appserverdaemon.NewWebSocketEndpoint("ws"+strings.TrimPrefix(server.URL, "http"), nil)
			client, err := openRemoteSessionClient(ctx, endpoint)
			if err != nil {
				t.Fatalf("openRemoteSessionClient: %v", err)
			}
			defer client.close()

			root := &cli.RootOptions{}
			if tc.root != nil {
				root = tc.root()
			}
			threadID, err := client.startThread(ctx, root, tc.state)
			if err != nil {
				t.Fatalf("startThread: %v", err)
			}
			if threadID != "thread-server-model" {
				t.Fatalf("threadID = %q", threadID)
			}
			params := <-paramsCh
			if got := stringOrEmpty(params["model"]); got != tc.wantModel {
				t.Fatalf("thread/start model = %q, want %q (params %#v)", got, tc.wantModel, params)
			}
			configValues, _ := params["config"].(map[string]any)
			if got := stringOrEmpty(configValues["model_reasoning_effort"]); got != tc.wantEffort {
				t.Fatalf("thread/start model_reasoning_effort = %q, want %q (config %#v)", got, tc.wantEffort, configValues)
			}
			if got := stringOrEmpty(params["modelProvider"]); got != tc.wantProvider {
				t.Fatalf("thread/start modelProvider = %q, want %q (params %#v)", got, tc.wantProvider, params)
			}
			select {
			case err := <-serverErrs:
				t.Fatalf("server error: %v", err)
			default:
			}
		})
	}
}

// TestRemoteFreshStartClearsImplicitClientModelWithoutConfigReadLikeRust covers
// the Rust #50913 `config/read`-unsupported branch of
// `bootstrap_server_owned_start`: when the app server cannot answer
// `config/read`, an implicit client model and reasoning effort are cleared
// rather than restored, while an explicit launch model still survives.
//
// Rust test:
// app/tests/startup_defaults_tests.rs::fresh_startup_uses_server_defaults_with_explicit_and_managed_precedence
// (its `server_defaults_read == false` path).
func TestRemoteFreshStartClearsImplicitClientModelWithoutConfigReadLikeRust(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())

	cases := []struct {
		name       string
		root       func() *cli.RootOptions
		state      *codextui.State
		wantModel  string
		wantEffort string
	}{
		{
			name:       "implicit client settings are cleared",
			wantModel:  "",
			wantEffort: "",
		},
		{
			name: "explicit harness model survives",
			root: func() *cli.RootOptions {
				root := &cli.RootOptions{}
				root.Shared.Model = "cli-model"
				return root
			},
			state:      codextui.NewState(&codextui.Options{Model: "cli-model", ReasoningEffort: "low"}),
			wantModel:  "cli-model",
			wantEffort: "",
		},
		{
			name: "explicit generic override survives",
			root: func() *cli.RootOptions {
				return &cli.RootOptions{ConfigOverrides: []string{"model=override-model"}}
			},
			state:      codextui.NewState(&codextui.Options{Model: "override-model", ReasoningEffort: "low"}),
			wantModel:  "override-model",
			wantEffort: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			paramsCh := make(chan map[string]any, 1)
			serverErrs := make(chan error, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := websocket.Accept(w, r, nil)
				if err != nil {
					remoteTUITestSendErr(serverErrs, err)
					return
				}
				defer conn.Close(websocket.StatusNormalClosure, "")
				for {
					req, err := remoteTUITestReadRequest(ctx, conn)
					if err != nil {
						return
					}
					switch req.Method {
					case string(appserver.MethodInitialize):
						remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}})
					case string(appserver.MethodConfigRead):
						// An older app server rejects `config/read`, so the launch
						// has no server defaults to read.
						remoteTUITestWrite(ctx, conn, map[string]any{
							"jsonrpc": "2.0",
							"id":      req.ID,
							"error":   map[string]any{"code": -32601, "message": "Method not found"},
						})
					case string(appserver.MethodThreadStart):
						var params map[string]any
						if err := json.Unmarshal(req.Params, &params); err != nil {
							remoteTUITestSendErr(serverErrs, err)
							return
						}
						paramsCh <- params
						remoteTUITestWrite(ctx, conn, map[string]any{
							"jsonrpc": "2.0",
							"id":      req.ID,
							"result":  map[string]any{"thread": map[string]any{"id": "thread-no-config-read"}},
						})
					default:
						remoteTUITestSendErr(serverErrs, fmt.Errorf("unexpected method %s", req.Method))
						return
					}
				}
			}))
			defer server.Close()

			endpoint := appserverdaemon.NewWebSocketEndpoint("ws"+strings.TrimPrefix(server.URL, "http"), nil)
			client, err := openRemoteSessionClient(ctx, endpoint)
			if err != nil {
				t.Fatalf("openRemoteSessionClient: %v", err)
			}
			defer client.close()

			root := &cli.RootOptions{}
			if tc.root != nil {
				root = tc.root()
			}
			state := tc.state
			if state == nil {
				state = codextui.NewState(&codextui.Options{Model: "stale-client-model", ReasoningEffort: "low"})
			}
			threadID, err := client.startThread(ctx, root, state)
			if err != nil {
				t.Fatalf("startThread: %v", err)
			}
			if threadID != "thread-no-config-read" {
				t.Fatalf("threadID = %q", threadID)
			}
			params := <-paramsCh
			if got := stringOrEmpty(params["model"]); got != tc.wantModel {
				t.Fatalf("thread/start model = %q, want %q (params %#v)", got, tc.wantModel, params)
			}
			configValues, _ := params["config"].(map[string]any)
			if got := stringOrEmpty(configValues["model_reasoning_effort"]); got != tc.wantEffort {
				t.Fatalf("thread/start model_reasoning_effort = %q, want %q (config %#v)", got, tc.wantEffort, configValues)
			}
			select {
			case err := <-serverErrs:
				t.Fatalf("server error: %v", err)
			default:
			}
		})
	}
}
