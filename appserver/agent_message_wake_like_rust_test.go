package appserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"codex_go/agent"
	"codex_go/config"
	"codex_go/model"
	"codex_go/session"
)

func TestQueueOnlyAgentMessageWakesCompletedRootLikeRust(t *testing.T) {
	router, cwd := newAgentMessageWakeTestRouter(t, true)
	router.setRuntimeTurnContext("root", json.RawMessage(`{"turn_id":"root-turn","model":"gpt-wake-test","effort":"high","cyber_access_program":"daybreak_red"}`))
	controller, ok := newRuntimeAgentControllerWithVersion(router, "child", cwd, 4, agent.VersionV2).(agent.V2ToolController)
	if !ok {
		t.Fatal("runtime controller does not implement V2ToolController")
	}

	if err := controller.SendMessage(context.Background(), &agent.SendMessageArgs{
		Target:    "/root",
		Message:   "Late agent update",
		Plaintext: true,
	}); err != nil {
		t.Fatalf("SendMessage() error = %v", err)
	}
	active := router.threads.ActiveTurn("root")
	if active == nil || active.Params == nil {
		t.Fatal("queue-only agent message did not wake the completed root")
	}
	defer router.cancelActiveRuntimeTurn("root", active.TurnID)
	if len(active.Params.AdditionalInputItems) != 1 {
		t.Fatalf("additional input items = %#v, want one agent message", active.Params.AdditionalInputItems)
	}
	if active.Params.CoreCyberAccessProgram != "daybreak_red" {
		t.Fatalf("previous cyber access program = %q, want daybreak_red", active.Params.CoreCyberAccessProgram)
	}
	if active.Params.Effort == nil || *active.Params.Effort != "high" {
		t.Fatalf("previous effort = %#v, want high", active.Params.Effort)
	}
	if queued := router.drainRuntimeAgentMessages("root"); len(queued) != 0 {
		t.Fatalf("queued mail after wake = %#v, want none", queued)
	}
	assertAgentMessageRoute(t, active.Params.AdditionalInputItems[0], "/root/worker", "/root")
	firstTurnID := active.TurnID
	if err := controller.SendMessage(context.Background(), &agent.SendMessageArgs{
		Target:    "/root",
		Message:   "Another update",
		Plaintext: true,
	}); err != nil {
		t.Fatalf("second SendMessage() error = %v", err)
	}
	if current := router.threads.ActiveTurn("root"); current == nil || current.TurnID != firstTurnID {
		t.Fatalf("second message started another root turn: %#v", current)
	}
}

func TestHostMailboxCompletionWakesParentAfterFinalAnswerLikeRust(t *testing.T) {
	router, _ := newAgentMessageWakeTestRouter(t, true)

	router.deliverRuntimeAgentCompletion("child", agent.AgentMessageStatus{
		Kind:    agent.AgentMessageStatusCompleted,
		Message: "All release checks passed.",
	})
	active := router.threads.ActiveTurn("root")
	if active == nil || active.Params == nil {
		t.Fatal("idle parent did not wake for the child's final result")
	}
	defer router.cancelActiveRuntimeTurn("root", active.TurnID)
	if len(active.Params.AdditionalInputItems) != 1 {
		t.Fatalf("additional input items = %#v, want one completion message", active.Params.AdditionalInputItems)
	}
	if queued := router.drainRuntimeAgentMessages("root"); len(queued) != 0 {
		t.Fatalf("queued mail after wake = %#v, want none", queued)
	}
	assertAgentMessageRoute(t, active.Params.AdditionalInputItems[0], "/root/worker", "/root")
	firstTurnID := active.TurnID
	router.deliverRuntimeAgentCompletion("child", agent.AgentMessageStatus{
		Kind:    agent.AgentMessageStatusCompleted,
		Message: "A later final result.",
	})
	if current := router.threads.ActiveTurn("root"); current == nil || current.TurnID != firstTurnID {
		t.Fatalf("second completion started another root turn: %#v", current)
	}
}

func TestAgentMessageWakePreservesExistingBoundariesLikeRust(t *testing.T) {
	tests := []struct {
		name   string
		setup  func(t *testing.T, router *RuntimeRouter, root *session.Record)
		target string
	}{
		{
			name: "feature disabled by default",
			setup: func(t *testing.T, router *RuntimeRouter, root *session.Record) {
				writeAgentWakeFeature(t, router.config.CodexHome(), false)
			},
			target: "root",
		},
		{
			name: "plan mode",
			setup: func(t *testing.T, router *RuntimeRouter, root *session.Record) {
				if _, err := router.requireThreadExtras().UpdateSettings(&SettingsUpdateParams{
					ThreadID:          "root",
					CollaborationMode: map[string]any{"mode": string(ModeKindPlan)},
				}); err != nil {
					t.Fatalf("UpdateSettings() error = %v", err)
				}
			},
			target: "root",
		},
		{
			name: "interrupted root",
			setup: func(t *testing.T, router *RuntimeRouter, root *session.Record) {
				root.Metadata.RolloutTurns = []session.TurnSnapshot{{ID: "root-turn", Status: string(TurnStatusInterrupted)}}
				if err := router.services.ThreadRouter.store.Save(root); err != nil {
					t.Fatalf("Save() error = %v", err)
				}
			},
			target: "root",
		},
		{
			name:   "child agent",
			setup:  func(t *testing.T, router *RuntimeRouter, root *session.Record) {},
			target: "child",
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			router, _ := newAgentMessageWakeTestRouter(t, true)
			root, err := router.threadRecord("root", true, false)
			if err != nil {
				t.Fatalf("threadRecord(root) error = %v", err)
			}
			testCase.setup(t, router, root)
			recipient := "/root"
			author := "/root/worker"
			if testCase.target == "child" {
				recipient = "/root/worker"
				author = "/root/worker/grandchild"
			}
			router.enqueueRuntimeAgentMessage(testCase.target, runtimeAgentCommunicationInputItem(author, recipient, "queue only", false, true))
			if active := router.threads.ActiveTurn(testCase.target); active != nil {
				router.cancelActiveRuntimeTurn(testCase.target, active.TurnID)
				t.Fatalf("thread %s started a turn despite wake boundary: %#v", testCase.target, active)
			}
			if queued := router.drainRuntimeAgentMessages(testCase.target); len(queued) != 1 {
				t.Fatalf("queued mail = %#v, want exactly one preserved item", queued)
			}
		})
	}
}

func newAgentMessageWakeTestRouter(t *testing.T, wakeEnabled bool) (*RuntimeRouter, string) {
	t.Helper()
	home := t.TempDir()
	writeAgentWakeFeature(t, home, wakeEnabled)
	cwd := t.TempDir()
	now := time.Now().UTC()
	store := session.NewStore(filepath.Join(home, "sessions"))
	root := &session.Record{
		ID:        "root",
		SessionID: "root",
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata: session.Metadata{
			CWD: cwd, Model: "gpt-wake-test", Source: string(SessionSourceCli), AgentPath: "/root",
			RolloutTurns: []session.TurnSnapshot{{ID: "root-turn", Status: string(TurnStatusCompleted)}},
		},
	}
	child := &session.Record{
		ID:             "child",
		SessionID:      "root",
		ParentThreadID: "root",
		CreatedAt:      now,
		UpdatedAt:      now,
		RecencyAt:      now,
		Metadata: session.Metadata{
			CWD: cwd, Model: "gpt-wake-test", Source: string(SessionSourceAppServer),
			AgentPath: "/root/worker", AgentDepth: 1,
			RolloutTurns: []session.TurnSnapshot{{ID: "child-turn", Status: string(TurnStatusCompleted)}},
		},
	}
	if err := store.Create(root); err != nil {
		t.Fatalf("Create(root) error = %v", err)
	}
	if err := store.Create(child); err != nil {
		t.Fatalf("Create(child) error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
		Agent:        &blockingReviewRuntimeAgent{requests: make(chan model.AgentRequest, 4)},
	})
	t.Cleanup(func() {
		for _, threadID := range []string{"root", "child"} {
			if active := router.threads.ActiveTurn(threadID); active != nil {
				router.cancelActiveRuntimeTurn(threadID, active.TurnID)
			}
		}
	})
	return router, cwd
}

func writeAgentWakeFeature(t *testing.T, home string, enabled bool) {
	t.Helper()
	if !enabled {
		if err := os.Remove(config.ConfigPath(home)); err != nil && !os.IsNotExist(err) {
			t.Fatalf("remove config error = %v", err)
		}
		return
	}
	body := "[features]\nwake_on_agent_messages = true\n"
	if err := os.WriteFile(config.ConfigPath(home), []byte(body), 0o600); err != nil {
		t.Fatalf("write config error = %v", err)
	}
}

func assertAgentMessageRoute(t *testing.T, item any, wantAuthor string, wantRecipient string) {
	t.Helper()
	message, ok := item.(map[string]any)
	if !ok || message["type"] != "agent_message" {
		t.Fatalf("input item = %#v, want agent_message", item)
	}
	if message["author"] != wantAuthor || message["recipient"] != wantRecipient {
		t.Fatalf("agent route = %s -> %s, want %s -> %s", message["author"], message["recipient"], wantAuthor, wantRecipient)
	}
}
