package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
)

// TestRemoteModelPickerScopesByProviderLikeRust mirrors Rust #63553: the remote
// picker asks model/list for the active thread's provider, keeps the default
// list when no provider is set, and falls back to the unscoped server list when
// the server rejects provider scoping.
func TestRemoteModelPickerScopesByProviderLikeRust(t *testing.T) {
	cases := []struct {
		name           string
		provider       string
		rejectProvider bool
		wantProviders  []string
		wantOption     string
	}{
		{name: "provider scoped", provider: "acme", wantProviders: []string{"acme"}, wantOption: "acme-model"},
		{name: "server default", provider: "", wantProviders: []string{""}, wantOption: "default-model"},
		{name: "unscoped fallback", provider: "acme", rejectProvider: true, wantProviders: []string{"acme", ""}, wantOption: "default-model"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var providers []string
			serverErr := make(chan error, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := websocket.Accept(w, r, nil)
				if err != nil {
					remoteTUITestSendErr(serverErr, err)
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
					case string(appserver.MethodModelList):
						var body struct {
							ModelProvider string `json:"modelProvider"`
						}
						_ = json.Unmarshal(req.Params, &body)
						provider := strings.TrimSpace(body.ModelProvider)
						providers = append(providers, provider)
						if testCase.rejectProvider && provider != "" {
							remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{"code": -32602, "message": "unsupported provider"}})
							continue
						}
						model := "default-model"
						if provider != "" {
							model = "acme-model"
						}
						remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
							"data": []any{map[string]any{
								"model":                     model,
								"displayName":               strings.ToUpper(model),
								"isDefault":                 true,
								"defaultReasoningEffort":    "high",
								"supportedReasoningEfforts": []any{map[string]any{"reasoningEffort": "high", "description": "High"}},
								"serviceTiers":              []any{map[string]any{"id": "priority", "name": "Priority", "description": "Fast"}},
							}},
						}})
					default:
						remoteTUITestSendErr(serverErr, nil)
						return
					}
				}
			}))
			defer server.Close()

			endpoint := appserverdaemon.NewWebSocketEndpoint("ws"+strings.TrimPrefix(server.URL, "http"), nil)
			loader := interactiveRemoteListModels(ctx, endpoint)
			options, err := loader("", testCase.provider, true)
			if err != nil {
				t.Fatalf("list models: %v", err)
			}
			if len(options) != 1 || options[0].ID != testCase.wantOption {
				t.Fatalf("options = %#v, want %s", options, testCase.wantOption)
			}
			if options[0].DefaultReasoningEffort != "high" || len(options[0].ServiceTiers) != 1 || options[0].ServiceTiers[0] != "priority" {
				t.Fatalf("catalog metadata = %#v", options[0])
			}
			if strings.Join(providers, ",") != strings.Join(testCase.wantProviders, ",") {
				t.Fatalf("model/list providers = %#v, want %#v", providers, testCase.wantProviders)
			}
		})
	}
}
