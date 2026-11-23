package appserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/model"
	"codex_go/session"
)

// Mirrors Rust #63312 (session/inject.rs::checkpoint_client_injection): plaintext
// agent mail persisted onto an idle native multi-agent v2 root wakes the root
// through the ordinary wake rules, and the wake turn reads the recorded mail
// from history instead of receiving a queued copy.
func TestThreadInjectItemsWakesIdleNativeRootForRecordedMailLikeRust(t *testing.T) {
	router, _ := newRecordedAgentMailWakeRouter(t, true)
	mail := recordMailBatch(t, "/root/worker", "/root", "report ready")

	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadInjectItems, ThreadInjectItemsParams{
		ThreadID: "root", Items: []json.RawMessage{mail},
	}))
	if response.Error != nil {
		t.Fatalf("root injection = %+v", response.Error)
	}
	active := router.threads.ActiveTurn("root")
	if active == nil || active.Params == nil {
		t.Fatal("recorded agent mail did not wake the idle native root")
	}
	defer router.cancelActiveRuntimeTurn("root", active.TurnID)
	if !active.Params.AutomaticWake {
		t.Fatalf("wake turn params = %#v, want AutomaticWake", active.Params)
	}
	if len(active.Params.AdditionalInputItems) != 0 {
		t.Fatalf("additional input items = %#v, want the recorded history copy only", active.Params.AdditionalInputItems)
	}
	if queued := router.drainRuntimeAgentMessages("root"); len(queued) != 0 {
		t.Fatalf("queued mail after wake = %#v, want none", queued)
	}
}

// The wake keeps Rust's boundaries: the feature stays off by default, the
// multi-agent v2 runtime gate applies, and a child thread never wakes its root
// from this path.
func TestRecordedAgentMailWakeBoundariesLikeRust(t *testing.T) {
	t.Run("wake feature disabled", func(t *testing.T) {
		router, _ := newRecordedAgentMailWakeRouter(t, false)
		router.maybeWakeRootAfterRecordedAgentMail("root", recordMailItems(t, "/root/worker", "/root", "queued"))
		if active := router.threads.ActiveTurn("root"); active != nil {
			router.cancelActiveRuntimeTurn("root", active.TurnID)
			t.Fatalf("wake feature disabled but root woke: %#v", active)
		}
	})
	t.Run("multi-agent v2 disabled", func(t *testing.T) {
		router, _ := newRecordedAgentMailWakeRouter(t, true)
		router.services.Config = config.NewConfigService(writeConfig(t, "[features]\nwake_on_agent_messages = true\n"))
		router.maybeWakeRootAfterRecordedAgentMail("root", recordMailItems(t, "/root/worker", "/root", "queued"))
		if active := router.threads.ActiveTurn("root"); active != nil {
			router.cancelActiveRuntimeTurn("root", active.TurnID)
			t.Fatalf("v1 root woke without multi_agent_v2: %#v", active)
		}
	})
	t.Run("child target", func(t *testing.T) {
		router, _ := newRecordedAgentMailWakeRouter(t, true)
		router.maybeWakeRootAfterRecordedAgentMail("child", recordMailItems(t, "/root", "/root/worker", "queued"))
		if active := router.threads.ActiveTurn("root"); active != nil {
			router.cancelActiveRuntimeTurn("root", active.TurnID)
			t.Fatalf("child mail woke the root: %#v", active)
		}
	})
}

func newRecordedAgentMailWakeRouter(t *testing.T, wakeEnabled bool) (*RuntimeRouter, string) {
	t.Helper()
	body := "[features]\nmulti_agent_v2 = true\n"
	if wakeEnabled {
		body += "wake_on_agent_messages = true\n"
	}
	home := writeConfig(t, body)
	cwd := t.TempDir()
	now := time.Now().UTC()
	store := session.NewStore(filepath.Join(home, "sessions"))
	root := &session.Record{
		ID: "root", SessionID: "root", CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{
			CWD: cwd, Model: "gpt-mail-test", Source: string(SessionSourceCli), AgentPath: "/root",
			RolloutTurns: []session.TurnSnapshot{{ID: "root-turn", Status: string(TurnStatusCompleted)}},
		},
	}
	child := &session.Record{
		ID: "child", SessionID: "root", ParentThreadID: "root", CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{
			CWD: cwd, Model: "gpt-mail-test", Source: string(SessionSourceAppServer),
			AgentPath: "/root/worker", AgentDepth: 1,
			RolloutTurns: []session.TurnSnapshot{{ID: "child-turn", Status: string(TurnStatusCompleted)}},
		},
	}
	for _, record := range []*session.Record{root, child} {
		if err := store.Create(record); err != nil {
			t.Fatalf("Create(%s) error = %v", record.ID, err)
		}
	}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
		DefaultCWD:   cwd,
		Agent:        &blockingReviewRuntimeAgent{requests: make(chan model.AgentRequest, 4)},
	})
	for _, threadID := range []string{"root", "child"} {
		router.requireThreadStatus().UpsertThread(threadID, false)
	}
	router.setRuntimeTurnContext("root", json.RawMessage(`{"turn_id":"root-turn","model":"gpt-mail-test","effort":"high"}`))
	return router, cwd
}

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	home := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte(body), 0o600); err != nil {
		t.Fatalf("write config error = %v", err)
	}
	return home
}

func recordMailBatch(t *testing.T, author, recipient, text string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"type": "agent_message", "author": author, "recipient": recipient,
		"content": []any{map[string]any{"type": "input_text", "text": text}},
	})
	if err != nil {
		t.Fatalf("Marshal(agent_message) error = %v", err)
	}
	return raw
}

func recordMailItems(t *testing.T, author, recipient, text string) []injectedResponseItem {
	t.Helper()
	parsed, _, err := injectedSessionItemsForRequest(&ThreadInjectItemsParams{
		ThreadID: "root", Items: []json.RawMessage{recordMailBatch(t, author, recipient, text)},
	}, time.Now().UTC(), false)
	if err != nil {
		t.Fatalf("injectedSessionItemsForRequest() error = %v", err)
	}
	return parsed
}
