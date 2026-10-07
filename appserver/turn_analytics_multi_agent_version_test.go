package appserver

import (
	"testing"
	"time"

	"codex_go/agent"
	"codex_go/config"
	"codex_go/session"
	"codex_go/telemetry"
	"codex_go/turn"
)

// Rust #51333 (5ddd19e8a9): the turn analytics event reports the turn context's
// resolved multi-agent version. The app-server resolves the version the same way
// it resolves the collaboration tool surface: a version persisted on the thread
// wins, otherwise the stable `multi_agent` feature (on by default) resolves V1.
func TestRuntimeRouterTurnEventReportsMultiAgentVersionLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name          string
		threadVersion string
		want          string
	}{
		{name: "persisted v2 thread", threadVersion: string(agent.VersionV2), want: telemetry.MultiAgentVersionV2},
		{name: "persisted v1 thread", threadVersion: string(agent.VersionV1), want: telemetry.MultiAgentVersionV1},
		{name: "default thread resolves the stable multi-agent feature", threadVersion: "", want: telemetry.MultiAgentVersionV1},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			home := t.TempDir()
			cwd := t.TempDir()
			store := session.NewStore(t.TempDir())
			now := time.Date(2026, 10, 7, 8, 0, 0, 0, time.UTC)
			const threadID = "thread-multi-agent-version"
			if err := store.Save(&session.Record{
				ID: threadID, SessionID: threadID, CreatedAt: now, UpdatedAt: now,
				Metadata: session.Metadata{
					CWD: cwd, AgentPath: "/root", MultiAgentVersion: testCase.threadVersion,
				},
			}); err != nil {
				t.Fatalf("save thread error = %v", err)
			}

			sink := NewNotificationBuffer()
			analyticsSink := newRecordingTurnEventSink()
			router := NewRuntimeRouter(RuntimeServices{
				ThreadRouter: NewRouter(store),
				Config:       config.NewConfigService(home),
				Turns:        turn.NewTurnService(),
				Agent:        newRecordingRuntimeAgent("done"),
				ThreadStatus: NewThreadStatusManager(),
				Analytics:    analyticsSink,
				DefaultCWD:   cwd,
			})
			router.SetNotificationSink(sink)
			router.requireThreadStatus().UpsertThread(threadID, false)

			initialize := requestWithParams(t, IntID(1), MethodInitialize, InitializeParams{
				ClientInfo: ClientInfo{Name: "codex-tui", Version: "1.2.3"},
			})
			initialize.ConnectionID = "conn-multi-agent-version"
			if response := router.Handle(initialize); response.Error != nil {
				t.Fatalf("initialize error: %+v", response.Error)
			}

			turnStart := requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
				ThreadID: threadID,
				Prompt:   "multi agent version",
			})
			turnStart.ConnectionID = "conn-multi-agent-version"
			response := router.Handle(turnStart)
			if response.Error != nil {
				t.Fatalf("turn start error: %+v", response.Error)
			}
			turnID := response.Result.(*turn.TurnStartResponse).Turn.ID
			waitForTurnCompletedStatus(t, sink, turnID, TurnStatusCompleted)

			event := waitForCodexTurnEvent(t, analyticsSink, turnID)
			if event.EventParams.MultiAgentVersion != testCase.want {
				t.Fatalf("multi_agent_version = %q, want %q", event.EventParams.MultiAgentVersion, testCase.want)
			}
		})
	}
}
