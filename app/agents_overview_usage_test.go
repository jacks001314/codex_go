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

	bubbletea "github.com/charmbracelet/bubbletea"
	"github.com/coder/websocket"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	"codex_go/auth"
	codextui "codex_go/tui"
	codextea "codex_go/tui/tea"
)

// TestAgentsOverviewThreadUsageFromAuth covers Rust #44970's usage mapping:
// only complete non-negative breakdown groups contribute totals.
func TestAgentsOverviewThreadUsageFromAuth(t *testing.T) {
	usage := agentsOverviewThreadUsageFromAuth(&auth.ThreadUsage{
		ThreadID:                    "thread-1",
		EstimatedUsageCreditsMicros: 3_400_000,
		EstimatedUsageUSDMicros:     int64PtrAppTest(140_000),
		Groups: []auth.ThreadUsageBreakdownGroup{
			{InputTokens: int64PtrAppTest(12_000), OutputTokens: int64PtrAppTest(3_000)},
		},
	})
	if !usage.HasGroups || usage.ThreadID != "thread-1" || usage.EstimatedCreditsMicros != 3_400_000 {
		t.Fatalf("usage = %#v", usage)
	}
	if usage.GroupInputTokens == nil || *usage.GroupInputTokens != 12_000 || usage.GroupOutputTokens == nil || *usage.GroupOutputTokens != 3_000 {
		t.Fatalf("group totals = %#v / %#v", usage.GroupInputTokens, usage.GroupOutputTokens)
	}

	// An incomplete group keeps only the complete side.
	partial := agentsOverviewThreadUsageFromAuth(&auth.ThreadUsage{
		ThreadID: "thread-1",
		Groups: []auth.ThreadUsageBreakdownGroup{
			{InputTokens: int64PtrAppTest(100), OutputTokens: int64PtrAppTest(10)},
			{OutputTokens: int64PtrAppTest(20)},
		},
	})
	if partial.GroupInputTokens != nil {
		t.Fatalf("incomplete input total = %v, want nil", *partial.GroupInputTokens)
	}
	if partial.GroupOutputTokens == nil || *partial.GroupOutputTokens != 30 {
		t.Fatalf("output total = %#v, want 30", partial.GroupOutputTokens)
	}

	// No groups and no usage produce an empty estimate.
	empty := agentsOverviewThreadUsageFromAuth(&auth.ThreadUsage{ThreadID: "thread-1"})
	if empty.HasGroups || empty.GroupInputTokens != nil {
		t.Fatalf("empty usage = %#v", empty)
	}
}

// TestInteractiveRemoteAgentsOverviewUsageReadsThreadScope covers Rust #44970:
// the dashboard reads the selected task's estimate through the app server and
// reports a disabled capability when no estimate comes back.
func TestInteractiveRemoteAgentsOverviewUsageReadsThreadScope(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	requests := make(chan remoteTUITestRequest, 4)
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
			requests <- req
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
						"estimatedUsageCreditsMicros": 3_400_000,
						"estimatedUsageUsdMicros":     140_000,
						"groups": []map[string]any{
							{"inputTokens": 12_000, "outputTokens": 3_000},
						},
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
	reader := interactiveRemoteAgentsOverviewUsage(ctx, endpoint)
	result, err := reader("thread-usage")
	if err != nil {
		t.Fatalf("usage reader error = %v", err)
	}
	if result.Outcome != codextea.AgentsOverviewUsageAvailable {
		t.Fatalf("outcome = %v, want available", result.Outcome)
	}
	if result.Usage.GroupInputTokens == nil || *result.Usage.GroupInputTokens != 12_000 {
		t.Fatalf("usage = %#v", result.Usage)
	}
	if result.Usage.EstimatedUSDMicros == nil || *result.Usage.EstimatedUSDMicros != 140_000 {
		t.Fatalf("usd = %#v", result.Usage.EstimatedUSDMicros)
	}

	disabled = true
	result, err = reader("thread-usage")
	if err != nil {
		t.Fatalf("disabled usage reader error = %v", err)
	}
	if result.Outcome != codextea.AgentsOverviewUsageDisabled {
		t.Fatalf("disabled outcome = %v", result.Outcome)
	}
	select {
	case err := <-serverErrs:
		t.Fatalf("server error: %v", err)
	default:
	}
}

// TestRemoteTUIRoutesBackgroundTokenUsageToDashboard covers Rust #44970: a
// token-usage notification for a non-active thread reaches the dashboard
// instead of being dropped.
func TestRemoteTUIRoutesBackgroundTokenUsageToDashboard(t *testing.T) {
	messages := make(chan bubbletea.Msg, 1)
	state := codextui.NewState(nil)
	state.SetThreadID("thread-main")
	client := &remoteAppServerTUIClient{state: state, messages: messages}
	params, err := json.Marshal(appserver.ThreadTokenUsageUpdatedNotification{
		ThreadID: "thread-other",
		TurnID:   "turn-1",
		TokenUsage: appserver.TokenUsage{
			Total: &appserver.TokenUsageBreakdown{InputTokens: 13_000, OutputTokens: 4_000, TotalTokens: 17_000},
			Last:  &appserver.TokenUsageBreakdown{InputTokens: 13_000, OutputTokens: 4_000, TotalTokens: 17_000},
		},
	})
	if err != nil {
		t.Fatalf("marshal notification: %v", err)
	}
	if err := client.handleNotification(remoteAppServerMessage{
		Method: string(appserver.NotificationThreadTokenUsageUpdated),
		Params: params,
	}); err != nil {
		t.Fatalf("handle token usage: %v", err)
	}
	select {
	case message := <-messages:
		scoped, ok := message.(codextea.ThreadScopedEventMsg)
		if !ok {
			t.Fatalf("message = %T, want ThreadScopedEventMsg", message)
		}
		if scoped.ThreadID != "thread-other" || scoped.Event.Type != "thread.token_usage.updated" {
			t.Fatalf("scoped event = %#v", scoped)
		}
	case <-time.After(time.Second):
		t.Fatal("background token usage was dropped")
	}
}

func int64PtrAppTest(value int64) *int64 { return &value }
