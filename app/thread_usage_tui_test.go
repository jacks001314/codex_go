package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	"codex_go/auth"
	codextea "codex_go/tui/tea"
)

func TestThreadUsageReadResultFromAuth(t *testing.T) {
	disabled := threadUsageReadResultFromAuth(&auth.GetAccountTokenUsageResponse{})
	if disabled.Outcome != codextea.ThreadUsageDisabled {
		t.Fatalf("missing thread usage outcome = %v, want disabled", disabled.Outcome)
	}

	model := "gpt-5.4"
	effort := "high"
	speed := "fast"
	input := int64(120)
	cached := int64(25)
	output := int64(50)
	estimatedUSD := int64(1_820_000)
	nativeUSD := int64(750_000)
	result := threadUsageReadResultFromAuth(&auth.GetAccountTokenUsageResponse{
		ThreadUsage: &auth.ThreadUsage{
			ThreadID:                    " thread-usage ",
			EstimatedUsageCreditsMicros: 50_000_000,
			EstimatedUsageUSDMicros:     &estimatedUSD,
			NativeUsageUSDMicros:        &nativeUSD,
			Groups: []auth.ThreadUsageBreakdownGroup{{
				Model:                       &model,
				ReasoningEffort:             &effort,
				Speed:                       &speed,
				EstimatedUsageCreditsMicros: 40_000_000,
				InputTokens:                 &input,
				CachedInputTokens:           &cached,
				OutputTokens:                &output,
			}},
		},
	})
	if result.Outcome != codextea.ThreadUsageAvailable {
		t.Fatalf("outcome = %v, want available", result.Outcome)
	}
	usage := result.Usage
	if usage.ThreadID != "thread-usage" || usage.EstimatedUsageCreditsMicros != 50_000_000 {
		t.Fatalf("usage identity = %#v", usage)
	}
	if usage.EstimatedUsageUSDMicros == nil || *usage.EstimatedUsageUSDMicros != estimatedUSD {
		t.Fatalf("estimated usd = %#v", usage.EstimatedUsageUSDMicros)
	}
	if usage.NativeUsageUSDMicros == nil || *usage.NativeUsageUSDMicros != nativeUSD {
		t.Fatalf("native usd = %#v", usage.NativeUsageUSDMicros)
	}
	if len(usage.Groups) != 1 {
		t.Fatalf("groups = %#v", usage.Groups)
	}
	group := usage.Groups[0]
	if group.Model == nil || *group.Model != model || group.ReasoningEffort == nil || *group.ReasoningEffort != effort || group.Speed == nil || *group.Speed != speed {
		t.Fatalf("group labels = %#v", group)
	}
	if group.InputTokens == nil || *group.InputTokens != input || group.CachedInputTokens == nil || *group.CachedInputTokens != cached || group.OutputTokens == nil || *group.OutputTokens != output {
		t.Fatalf("group tokens = %#v", group)
	}
}

func TestInteractiveRemoteThreadUsageReadsActiveThread(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	serverErrs := make(chan error, 1)
	disabled := false
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
				if websocket.CloseStatus(err) == websocket.StatusNormalClosure || websocket.CloseStatus(err) == websocket.StatusGoingAway || errors.Is(err, context.Canceled) {
					return
				}
				remoteTUITestSendErr(serverErrs, err)
				return
			}
			switch req.Method {
			case string(appserver.MethodInitialize):
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}})
			case string(appserver.MethodGetAccountTokenUsage):
				var params auth.GetAccountTokenUsageParams
				if err := json.Unmarshal(req.Params, &params); err != nil {
					remoteTUITestSendErr(serverErrs, err)
					return
				}
				if params.ThreadID == nil || *params.ThreadID != "thread-usage" {
					remoteTUITestSendErr(serverErrs, fmt.Errorf("usage params = %#v", params))
					return
				}
				if disabled {
					remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}})
					return
				}
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
					"threadUsage": map[string]any{
						"threadId":                    "thread-usage",
						"estimatedUsageCreditsMicros": 50_000_000,
						"estimatedUsageUsdMicros":     1_820_000,
						"nativeUsageUsdMicros":        750_000,
						"groups": []map[string]any{{
							"model":                       "gpt-5.4",
							"estimatedUsageCreditsMicros": 50_000_000,
							"inputTokens":                 120,
							"cachedInputTokens":           25,
							"outputTokens":                50,
						}},
					},
				}})
				return
			default:
				remoteTUITestSendErr(serverErrs, fmt.Errorf("unexpected method %s", req.Method))
				return
			}
		}
	}))
	defer server.Close()

	endpoint := appserverdaemon.NewWebSocketEndpoint("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	reader := interactiveRemoteThreadUsageReader(ctx, endpoint)
	result, err := reader("thread-usage")
	if err != nil {
		t.Fatalf("thread usage reader error = %v", err)
	}
	if result.Outcome != codextea.ThreadUsageAvailable || result.Usage.ThreadID != "thread-usage" {
		t.Fatalf("thread usage result = %#v", result)
	}
	if result.Usage.NativeUsageUSDMicros == nil || *result.Usage.NativeUsageUSDMicros != 750_000 {
		t.Fatalf("native usage = %#v", result.Usage.NativeUsageUSDMicros)
	}

	disabled = true
	result, err = reader("thread-usage")
	if err != nil {
		t.Fatalf("disabled thread usage reader error = %v", err)
	}
	if result.Outcome != codextea.ThreadUsageDisabled {
		t.Fatalf("disabled outcome = %v", result.Outcome)
	}
	select {
	case err := <-serverErrs:
		t.Fatalf("server error: %v", err)
	default:
	}
}
