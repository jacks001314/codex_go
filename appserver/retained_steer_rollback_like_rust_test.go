package appserver

import (
	"encoding/json"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/retainedctx"
	"codex_go/rollout"
	"codex_go/session"
)

// Mirrors Rust #42298 across resume: a restarted thread replays the rollout's
// retained answers, but an answer produced by a rolled-back steer must not come
// back while the earlier answer from the same turn's surviving call remains.
func TestRuntimeRouterResumePreservesAnswersBeforeRolledBackSteerLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(home)
	threadID := session.ThreadID("thread-steer-rollback")
	if err := store.Create(&session.Record{ID: threadID, SessionID: string(threadID)}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	now := time.Now().UTC()
	recorder, err := rollout.NewRecorder(&rollout.CreateParams{
		CodexHome:     home,
		ThreadID:      string(threadID),
		Source:        "cli",
		CWD:           home,
		ModelProvider: "openai",
		Now:           now,
	})
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	const turnID = "shared-turn"
	userRaw := func(text string) json.RawMessage {
		raw, marshalErr := json.Marshal(map[string]any{
			"type":    "message",
			"role":    "user",
			"content": []any{map[string]any{"type": "input_text", "text": text}},
		})
		if marshalErr != nil {
			t.Fatalf("marshal user item: %v", marshalErr)
		}
		return raw
	}
	items := []rollout.Item{
		{ID: "user-before", Type: "message", Role: "user", Text: "Check whether to publish.", Raw: userRaw("Check whether to publish."), Metadata: map[string]any{"turnId": turnID}},
		{ID: "call-before", Type: "function_call", Name: "request_user_input", CallID: "before-steer", Text: "{}", Metadata: map[string]any{"turnId": turnID}},
	}
	for _, item := range items {
		if err := recorder.AppendItem(item); err != nil {
			t.Fatalf("AppendItem(%s) error = %v", item.ID, err)
		}
	}
	if err := recorder.AppendRetainedContext(retainedctx.RetainedContextEvent{Answer: retainedctx.VerifiedAnswer{
		TurnID:    turnID,
		CallID:    "before-steer",
		Questions: []retainedctx.VerifiedQuestionAnswer{{Question: "Publish?", Answer: "Only privately."}},
	}}, now); err != nil {
		t.Fatalf("AppendRetainedContext(before-steer) error = %v", err)
	}
	steer := rollout.Item{ID: "user-after", Type: "message", Role: "user", Text: "Also inspect the README.", Raw: userRaw("Also inspect the README."), Metadata: map[string]any{"turnId": turnID}}
	if err := recorder.AppendItem(steer); err != nil {
		t.Fatalf("AppendItem(steer) error = %v", err)
	}
	if err := recorder.AppendItem(rollout.Item{ID: "call-after", Type: "function_call", Name: "request_user_input", CallID: "after-steer", Text: "{}", Metadata: map[string]any{"turnId": turnID}}); err != nil {
		t.Fatalf("AppendItem(call-after) error = %v", err)
	}
	if err := recorder.AppendRetainedContext(retainedctx.RetainedContextEvent{Answer: retainedctx.VerifiedAnswer{
		TurnID:    turnID,
		CallID:    "after-steer",
		Questions: []retainedctx.VerifiedQuestionAnswer{{Question: "Publish the README?", Answer: "Do not publish it."}},
	}}, now); err != nil {
		t.Fatalf("AppendRetainedContext(after-steer) error = %v", err)
	}
	if err := recorder.AppendThreadRolledBack(1, now); err != nil {
		t.Fatalf("AppendThreadRolledBack() error = %v", err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	fresh := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
	})
	context := fresh.retainedContextForThread(string(threadID))
	if context == nil {
		t.Fatal("retainedContextForThread() = nil after resume")
	}
	answers := context.VerifiedAnswers()
	if len(answers) != 1 || answers[0].CallID != "before-steer" {
		t.Fatalf("restored answers after steer rollback = %#v, want only before-steer", answers)
	}
}
