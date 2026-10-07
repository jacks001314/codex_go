package exec

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/model"
	"codex_go/session"
)

// Rust #49598 (`de02016798`) `Session::replace_compacted_history`
// (`codex-rs/core/src/session/mod.rs`): "Goal edits are published outside the
// running task. Keep edits accepted after the compaction input snapshot, in
// their original order, after its replacement." Rust applies this once in core,
// so the CLI/exec compaction install points must do the same.

func execGoalInstructionRaw(t *testing.T, kind, text string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"type": "message",
		"role": "user",
		"content": []map[string]any{
			{"type": "input_text", "text": text},
		},
		"internal_chat_message_metadata_passthrough": map[string]any{
			"content_item_kinds": []string{kind},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func execGoalItem(t *testing.T, id, text string) session.Item {
	t.Helper()
	return session.Item{ID: id, Type: "message", Role: "user", CreatedAt: time.Now().UTC(), Raw: execGoalInstructionRaw(t, "user.goal", text)}
}

func execItemIDs(items []session.Item) []string {
	ids := make([]string, 0, len(items))
	for i := range items {
		ids = append(ids, items[i].ID)
	}
	return ids
}

// TestExecUserGoalInstructionTextLikeRust mirrors Rust `UserGoalUpdate::message_text`
// (#49598): only a host-annotated goal instruction counts.
func TestExecUserGoalInstructionTextLikeRust(t *testing.T) {
	if _, ok := execUserGoalInstructionText(execGoalInstructionRaw(t, "user.goal", "Ship it.")); !ok {
		t.Fatal("small host-annotated goal edit should count as a goal instruction")
	}
	if _, ok := execUserGoalInstructionText(execGoalInstructionRaw(t, "user.goal.omitted", "User cleared the goal.")); !ok {
		t.Fatal("omitted-objective goal instruction should count as a goal instruction")
	}
	// A matching text wrapper without the host annotation is not a goal edit.
	unannotated := []byte(`{"type":"message","role":"user","content":[{"type":"input_text","text":"<codex_internal_context source=\"user_goal\">\nUser cleared the goal.\n</codex_internal_context>"}]}`)
	if _, ok := execUserGoalInstructionText(unannotated); ok {
		t.Fatal("unannotated wrapper must not count as a goal instruction")
	}
	// Several content kinds do not identify a single goal edit.
	multi := []byte(`{"type":"message","role":"user","content":[{"type":"input_text","text":"x"}],"internal_chat_message_metadata_passthrough":{"content_item_kinds":["user.goal","user.text"]}}`)
	if _, ok := execUserGoalInstructionText(multi); ok {
		t.Fatal("multi-kind item must not count as a goal instruction")
	}
}

// TestRetainExecGoalInstructionsAcrossCompactionLikeRust covers the goal half of
// Rust `Session::replace_compacted_history` directly.
func TestRetainExecGoalInstructionsAcrossCompactionLikeRust(t *testing.T) {
	summary := session.Item{ID: "summary", Type: "agent_message", Role: "assistant", Text: "summary"}
	goal := execGoalItem(t, "goal-1", "Ship the sync work.")

	// An edit published after the snapshot is appended after the replacement.
	got := retainExecGoalInstructionsAcrossCompaction([]session.Item{summary}, []session.Item{summary, goal}, nil)
	if len(got) != 2 || got[1].ID != "goal-1" {
		t.Fatalf("published goal edit not retained: ids=%v", execItemIDs(got))
	}
	// An edit already covered by the compaction input is not duplicated.
	if got := retainExecGoalInstructionsAcrossCompaction([]session.Item{summary}, []session.Item{summary, goal}, map[string]bool{"goal-1": true}); len(got) != 1 {
		t.Fatalf("input goal edit should not be re-added: ids=%v", execItemIDs(got))
	}
	// An edit already present in the replacement is not duplicated.
	replacement := []session.Item{summary, goal}
	if got := retainExecGoalInstructionsAcrossCompaction(replacement, []session.Item{summary, goal}, nil); len(got) != 2 {
		t.Fatalf("replacement goal edit should not be duplicated: ids=%v", execItemIDs(got))
	}
	// Non-goal live items are ignored.
	plain := session.Item{ID: "u2", Type: "message", Role: "user", Text: "hello"}
	if got := retainExecGoalInstructionsAcrossCompaction([]session.Item{summary}, []session.Item{summary, plain}, nil); len(got) != 1 {
		t.Fatalf("non-goal live item must not be retained: ids=%v", execItemIDs(got))
	}
}

// TestExecCompactResumeBeforeTurnRetainsGoalEditsPublishedAfterSnapshotLikeRust
// drives the real pre-turn compaction install point: a goal edit present only in
// the stored history (published after the resume snapshot the compaction input is
// built from) survives the replacement.
func TestExecCompactResumeBeforeTurnRetainsGoalEditsPublishedAfterSnapshotLikeRust(t *testing.T) {
	home := t.TempDir()
	now := fixedExecTime()
	const threadID = "thread-exec-goal-retention"
	store := session.NewStore(filepath.Join(home, "sessions"))
	metadata := session.Metadata{
		Model:         "gpt-5.4",
		ModelProvider: model.OpenAIProviderID,
		CWD:           ".",
		Source:        "exec",
		ThreadSource:  "user",
		HistoryMode:   "legacy",
		Extra: map[string]any{
			"last_token_usage": map[string]any{
				"input_tokens": 264790, "output_tokens": 10, "total_tokens": 270000,
			},
			"model_context_window":                        int64(258400),
			"auto_compact_window_prefill":                 int64(260000),
			"auto_compact_window_prefill_server_observed": true,
		},
	}
	snapshot := &session.Record{
		ID: threadID, SessionID: threadID,
		CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: metadata,
		Items: []session.Item{
			{ID: "u1", Type: "message", Role: "user", Text: "first", CreatedAt: now},
			{ID: "a1", Type: "agent_message", Role: "assistant", Text: "answer", CreatedAt: now},
		},
	}
	if err := store.Save(snapshot); err != nil {
		t.Fatalf("Save snapshot error = %v", err)
	}
	// A goal edit accepted after the resume snapshot the compaction input is built
	// from. It lives only in the stored history, exactly the window Rust guards.
	goal := execGoalItem(t, "goal-published-after-snapshot", "Ship the sync work.")
	live, err := store.Read(session.ThreadID(threadID), true, true)
	if err != nil {
		t.Fatalf("Read live error = %v", err)
	}
	live.Items = append(live.Items, goal)
	if err := store.Save(live); err != nil {
		t.Fatalf("Save live error = %v", err)
	}

	runner := NewRunner(home)
	runner.Now = func() time.Time { return now }
	if err := runner.createExecRollout(snapshot, now); err != nil {
		t.Fatalf("createExecRollout error = %v", err)
	}
	cfg, _ := config.Load("")
	cfg.Values["model_auto_compact_token_limit_scope"] = "body_after_prefix"
	agent := &recordingAgent{message: "compacted summary"}
	compacted, err := runner.compactResumeBeforeTurn(context.Background(), &execResumeContext{Record: snapshot}, threadID, "turn-compact", "gpt-5.4", model.OpenAIProviderID, cfg, agent, nil)
	if err != nil {
		t.Fatalf("compactResumeBeforeTurn error = %v", err)
	}
	if !compacted {
		t.Fatal("expected pre-turn compaction")
	}
	saved, err := session.NewStore(filepath.Join(home, "sessions")).Read(session.ThreadID(threadID), true, true)
	if err != nil {
		t.Fatalf("Read saved record error = %v", err)
	}
	for i := range saved.Items {
		if saved.Items[i].ID == goal.ID {
			return
		}
	}
	t.Fatalf("goal edit published after the compaction snapshot was dropped: ids=%v", execItemIDs(saved.Items))
}
