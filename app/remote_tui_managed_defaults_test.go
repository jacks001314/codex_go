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
	"codex_go/config"
	codextui "codex_go/tui"

	"github.com/coder/websocket"
)

func stringPtr(value string) *string { return &value }

// TestRemoteStartThreadAppliesManagedNewThreadDefaultsLikeRust is the
// end-to-end check for #44693's TUI wiring: the thread/start params carry the
// requirements' models.newThread model/effort/service tier unless the launch
// explicitly selected them (generic -c override, or a highest-precedence
// profiled user layer).
func TestRemoteStartThreadAppliesManagedNewThreadDefaultsLikeRust(t *testing.T) {
	profile := "work"
	managedRequirements := map[string]any{
		"models": map[string]any{
			"newThread": map[string]any{
				"model":                "gpt-5-managed",
				"modelReasoningEffort": "high",
				"serviceTier":          "fast",
			},
		},
	}

	cases := []struct {
		name         string
		requirements map[string]any
		layers       []any
		root         cli.RootOptions
		state        *codextui.State
		wantModel    string
		wantEffort   string
		wantTier     string
	}{
		{
			name:         "managed defaults apply when nothing is selected",
			requirements: managedRequirements,
			state:        codextui.NewState(nil),
			wantModel:    "gpt-5-managed",
			wantEffort:   "high",
			wantTier:     "priority",
		},
		{
			name:         "generic -c model opts out of the managed model pair",
			requirements: managedRequirements,
			root:         cli.RootOptions{ConfigOverrides: []string{"model=gpt-5.4"}},
			state:        codextui.NewState(&codextui.Options{Model: "gpt-5.4"}),
			wantModel:    "gpt-5.4",
			wantTier:     "priority",
		},
		{
			name:         "profiled user layer opts out of the managed model pair",
			requirements: managedRequirements,
			layers: []any{map[string]any{
				"name":   map[string]any{"type": "user", "profile": profile},
				"config": map[string]any{"model": "gpt-5-profile"},
			}},
			state:     codextui.NewState(&codextui.Options{Model: "gpt-5-profile"}),
			wantModel: "gpt-5-profile",
			wantTier:  "priority",
		},
		{
			name:         "project layer shadows the profile so defaults apply",
			requirements: managedRequirements,
			layers: []any{
				map[string]any{
					"name":   map[string]any{"type": "user", "profile": profile},
					"config": map[string]any{"model": "gpt-5-profile"},
				},
				map[string]any{
					"name":   map[string]any{"type": "project"},
					"config": map[string]any{"model": "gpt-5-project"},
				},
			},
			state:      codextui.NewState(&codextui.Options{Model: "gpt-5-project"}),
			wantModel:  "gpt-5-managed",
			wantEffort: "high",
			wantTier:   "priority",
		},
		{
			name:         "explicit service tier stays independent",
			requirements: managedRequirements,
			root:         cli.RootOptions{ConfigOverrides: []string{"service_tier=flex"}},
			state:        codextui.NewState(&codextui.Options{ServiceTier: "flex"}),
			wantModel:    "gpt-5-managed",
			wantEffort:   "high",
			wantTier:     "flex",
		},
		{
			name:       "no managed defaults leaves the selection untouched",
			state:      codextui.NewState(&codextui.Options{Model: "gpt-5.4", ReasoningEffort: "low"}),
			wantModel:  "gpt-5.4",
			wantEffort: "low",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
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
						result := map[string]any{"config": map[string]any{}, "origins": map[string]any{}}
						if len(tc.layers) > 0 {
							result["layers"] = tc.layers
						}
						remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
					case string(appserver.MethodConfigRequirementsRead):
						requirements := tc.requirements
						if requirements == nil {
							requirements = map[string]any{}
						}
						remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"requirements": requirements}})
					case string(appserver.MethodModelList):
						// Rust #43177: without a configured model the TUI consults
						// the server catalog; an empty catalog keeps the bootstrap
						// model.
						remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"data": []any{}}})
					case string(appserver.MethodThreadStart):
						var params map[string]any
						if err := json.Unmarshal(req.Params, &params); err != nil {
							remoteTUITestSendErr(serverErrs, err)
							return
						}
						paramsCh <- params
						remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"thread": map[string]any{"id": "thread-managed"}}})
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
			threadID, err := client.startThread(ctx, &tc.root, tc.state)
			if err != nil {
				t.Fatalf("startThread: %v", err)
			}
			if threadID != "thread-managed" {
				t.Fatalf("threadID = %q", threadID)
			}
			params := <-paramsCh
			if got := stringOrEmpty(params["model"]); got != tc.wantModel {
				t.Fatalf("model = %q, want %q (params %#v)", got, tc.wantModel, params)
			}
			configValues, _ := params["config"].(map[string]any)
			if got := stringOrEmpty(configValues["model_reasoning_effort"]); got != tc.wantEffort {
				t.Fatalf("reasoning effort = %q, want %q (config %#v)", got, tc.wantEffort, configValues)
			}
			if got := stringOrEmpty(params["serviceTier"]); got != tc.wantTier {
				t.Fatalf("service tier = %q, want %q (params %#v)", got, tc.wantTier, params)
			}
			select {
			case err := <-serverErrs:
				t.Fatalf("server error: %v", err)
			default:
			}
		})
	}
}

func stringOrEmpty(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func TestNewThreadModelDefaultsFromRequirementsLikeRust(t *testing.T) {
	if got := newThreadModelDefaultsFromRequirements(nil); got != nil {
		t.Fatalf("nil requirements = %#v", got)
	}
	if got := newThreadModelDefaultsFromRequirements(&config.ConfigRequirements{}); got != nil {
		t.Fatalf("no models = %#v", got)
	}
	if got := newThreadModelDefaultsFromRequirements(&config.ConfigRequirements{
		Models: &config.ModelsRequirements{},
	}); got != nil {
		t.Fatalf("no new-thread defaults = %#v", got)
	}
	if got := newThreadModelDefaultsFromRequirements(&config.ConfigRequirements{
		Models: &config.ModelsRequirements{NewThread: &config.NewThreadModelDefaults{}},
	}); got != nil {
		t.Fatalf("empty defaults = %#v", got)
	}
	got := newThreadModelDefaultsFromRequirements(&config.ConfigRequirements{
		Models: &config.ModelsRequirements{NewThread: &config.NewThreadModelDefaults{
			Model:                stringPtr("gpt-5-managed"),
			ModelReasoningEffort: stringPtr("high"),
			ServiceTier:          stringPtr("fast"),
		}},
	})
	if got == nil || got.Model != "gpt-5-managed" || got.ReasoningEffort != "high" || got.ServiceTier != "fast" {
		t.Fatalf("defaults = %#v", got)
	}
}

func TestRemoteCLIConfigOverrideKeys(t *testing.T) {
	if keys := remoteCLIConfigOverrideKeys(nil); keys != nil {
		t.Fatalf("nil root keys = %#v", keys)
	}
	if keys := remoteCLIConfigOverrideKeys(&cli.RootOptions{}); keys != nil {
		t.Fatalf("no overrides keys = %#v", keys)
	}
	keys := remoteCLIConfigOverrideKeys(&cli.RootOptions{ConfigOverrides: []string{
		"model=gpt-5.4",
		"model_reasoning_effort=low",
	}})
	if len(keys) != 2 || keys[0] != "model" || keys[1] != "model_reasoning_effort" {
		t.Fatalf("keys = %#v", keys)
	}
	if keys := remoteCLIConfigOverrideKeys(&cli.RootOptions{ConfigOverrides: []string{"not-an-override"}}); keys != nil {
		t.Fatalf("invalid overrides keys = %#v", keys)
	}
}
