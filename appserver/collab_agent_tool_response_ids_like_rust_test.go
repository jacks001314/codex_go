package appserver

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/model"
	"codex_go/session"
	"codex_go/telemetry"
	"codex_go/turn"
)

func newCollabAnalyticsResponseRouter(t *testing.T) (*RuntimeRouter, *recordingTurnEventSink, string, string) {
	t.Helper()
	home := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte(`model = "gpt-5"`), 0o600); err != nil {
		t.Fatalf("write config error = %v", err)
	}
	store := session.NewStore(filepath.Join(home, "sessions"))
	sink := newRecordingTurnEventSink()
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter:          NewRouter(store),
		Config:                config.NewConfigService(home),
		Turns:                 turn.NewTurnService(),
		Agent:                 model.NewLocalAgentRunner(),
		ThreadStatus:          NewThreadStatusManager(),
		Analytics:             sink,
		AnalyticsRPCTransport: telemetry.AppServerRPCTransportInProcess,
	})
	connectionID := "conn-collab-response-ids"
	initialize := requestWithParams(t, IntID(1), MethodInitialize, InitializeParams{
		ClientInfo:   ClientInfo{Name: "codex-tui", Version: "1.2.3"},
		Capabilities: &InitializeCapabilities{ExperimentalAPI: true},
	})
	initialize.ConnectionID = connectionID
	if response := router.Handle(initialize); response.Error != nil {
		t.Fatalf("initialize error: %+v", response.Error)
	}
	threadStart := requestWithParams(t, IntID(2), MethodThreadStart, ThreadStartParams{
		CWD:          t.TempDir(),
		ThreadSource: threadSourcePtr(string(ThreadSourceUser)),
	})
	threadStart.ConnectionID = connectionID
	response := router.Handle(threadStart)
	if response.Error != nil {
		t.Fatalf("thread/start error: %+v", response.Error)
	}
	return router, sink, connectionID, response.Result.(*ThreadStartResponse).Thread.ID
}

func requireNoCollabAnalyticsEvent(t *testing.T, sink *recordingTurnEventSink) {
	t.Helper()
	select {
	case event := <-sink.collabToolCall:
		t.Fatalf("collaborator event emitted before response correlation: %#v", event.EventParams)
	case <-time.After(100 * time.Millisecond):
	}
}

// TestCollabAgentToolEventsKeepResponseIDsWhenCompletionRacesSamplingLikeRust
// pins Rust #42752 for both orderings: a collaborator completion is held until
// a sampling response can supply the originating id, and the next response is
// reported as the subsequent one.
func TestCollabAgentToolEventsKeepResponseIDsWhenCompletionRacesSamplingLikeRust(t *testing.T) {
	for _, responseFirst := range []bool{false, true} {
		name := "completion_first"
		if responseFirst {
			name = "response_first"
		}
		t.Run(name, func(t *testing.T) {
			router, sink, connectionID, threadID := newCollabAnalyticsResponseRouter(t)
			turnID := "turn-collab-response-ids"
			item := ThreadItem{
				ID:   "call-collab-response-ids",
				Type: "collabAgentToolCall",
				Data: map[string]any{
					"tool":              string(CollabAgentToolSendMessage),
					"status":            string(CollabAgentToolCallFailed),
					"senderThreadId":    threadID,
					"receiverThreadIds": []string{},
					"startedAtMs":       int64(1000),
					"completedAtMs":     int64(1010),
				},
			}
			if responseFirst {
				router.rememberSampledToolCalls(threadID, turnID, "response-1", []string{item.ID})
			}
			router.emitCollabAgentToolCallAnalyticsEvent(context.Background(), connectionID, threadID, turnID, &item, &appTurnRunConfig{})
			if !responseFirst {
				router.rememberSampledToolCalls(threadID, turnID, "response-1", []string{item.ID})
			}
			requireNoCollabAnalyticsEvent(t, sink)

			router.rememberSampledToolCalls(threadID, turnID, "response-2", nil)
			event := waitForCollabAgentToolCallAnalyticsEvent(t, sink, turnID)
			params := event.EventParams
			if params.ItemID != item.ID || params.TerminalStatus != telemetry.ToolItemTerminalStatusFailed {
				t.Fatalf("collaborator event = %#v", params)
			}
			if params.OriginatingResponseID == nil || *params.OriginatingResponseID != "response-1" {
				t.Fatalf("originating response id = %#v", params.OriginatingResponseID)
			}
			if params.SubsequentResponseID == nil || *params.SubsequentResponseID != "response-2" {
				t.Fatalf("subsequent response id = %#v", params.SubsequentResponseID)
			}
		})
	}
}
