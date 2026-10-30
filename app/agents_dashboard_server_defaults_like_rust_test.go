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

	"github.com/coder/websocket"
)

// Rust #43261: a background task started from the overview reads config/read for
// its destination and applies the managed new-thread defaults; servers that do
// not support config/read keep the previous fallback.
func TestDashboardNewSessionUsesServerDefaultsLikeRust(t *testing.T) {
	managed := map[string]any{
		"models": map[string]any{
			"newThread": map[string]any{
				"model":                "gpt-5-managed",
				"modelReasoningEffort": "high",
			},
		},
	}
	cases := []struct {
		name           string
		failConfigRead bool
		wantModel      string
		wantEffort     string
	}{
		{name: "managed destination defaults", wantModel: "gpt-5-managed", wantEffort: "high"},
		{name: "older server without config/read", failConfigRead: true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			paramsCh := make(chan map[string]any, 1)
			configReadCh := make(chan map[string]any, 1)
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
						var params map[string]any
						_ = json.Unmarshal(req.Params, &params)
						configReadCh <- params
						if testCase.failConfigRead {
							remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{"code": -32601, "message": "method not found"}})
							continue
						}
						remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
							"config": map[string]any{}, "origins": map[string]any{},
						}})
					case string(appserver.MethodConfigRequirementsRead):
						remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"requirements": managed}})
					case string(appserver.MethodModelList):
						remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"data": []any{}}})
					case string(appserver.MethodThreadStart):
						var params map[string]any
						if err := json.Unmarshal(req.Params, &params); err != nil {
							remoteTUITestSendErr(serverErrs, err)
							return
						}
						paramsCh <- params
						remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"thread": map[string]any{"id": "thread-dashboard"}}})
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
			source := newRemoteAgentsDashboardSource(client, "")
			threadID, err := source.NewSession(ctx, "/work/dest")
			if err != nil {
				t.Fatalf("NewSession: %v", err)
			}
			if threadID != "thread-dashboard" {
				t.Fatalf("threadID = %q", threadID)
			}
			params := <-paramsCh
			if got := stringOrEmpty(params["cwd"]); got != "/work/dest" {
				t.Fatalf("cwd = %q (params %#v)", got, params)
			}
			if got := stringOrEmpty(params["model"]); got != testCase.wantModel {
				t.Fatalf("model = %q, want %q (params %#v)", got, testCase.wantModel, params)
			}
			configValues, _ := params["config"].(map[string]any)
			if got := stringOrEmpty(configValues["model_reasoning_effort"]); got != testCase.wantEffort {
				t.Fatalf("reasoning effort = %q, want %q (config %#v)", got, testCase.wantEffort, configValues)
			}
			readParams := <-configReadCh
			if got := stringOrEmpty(readParams["cwd"]); got != "/work/dest" {
				t.Fatalf("config/read cwd = %q (params %#v)", got, readParams)
			}
			select {
			case err := <-serverErrs:
				t.Fatalf("server error: %v", err)
			default:
			}
		})
	}
}
