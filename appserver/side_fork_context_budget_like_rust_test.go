package appserver

import (
	"strings"
	"testing"

	"codex_go/session"
	"codex_go/turn"
)

// Rust seeds a forked session from the copied rollout (`InitialHistory::Forked`
// -> `apply_rollout_reconstruction` + `last_token_info_from_rollout`), so the
// first side-chat turn is measured against the inherited history before the
// request leaves the client. Go forks paginated sources through a
// `history_base` reference, so the pre-turn token check only sees the parent
// history once the store materializes that reference. This pins the side-chat
// report: a fork of a long thread must compact before its first turn instead of
// sending the whole transcript to the provider.
func TestSideForkMeasuresInheritedHistoryLikeRust(t *testing.T) {
	const parentID = session.ThreadID("thread-side-parent")
	const forkID = session.ThreadID("thread-side-fork")
	const modelID = "deepseek-v4-flash"

	store := session.NewStore(t.TempDir())
	now := fixedTime()
	inherited := strings.Repeat("inherited side chat history line\n", 40000)
	// Threads written before usage accounting existed (or whose rollout replay
	// carried no TokenCount event) have to be measured from the history itself.
	parentExtra := map[string]any{
		"auto_compact_fallback_prompt":        "Keep going.",
		"auto_compact_fallback_buffer_tokens": 4000,
	}
	if err := store.Create(&session.Record{
		ID:        parentID,
		SessionID: string(parentID),
		CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{Model: modelID, HistoryMode: "paginated", Extra: parentExtra},
		Items: []session.Item{
			{ID: "u1", Type: "message", Role: "user", Text: inherited, CreatedAt: now},
			{ID: "a1", Type: "agent_message", Role: "assistant", Text: "inherited answer", CreatedAt: now},
		},
	}); err != nil {
		t.Fatalf("Create parent error = %v", err)
	}
	forked, err := store.Fork(parentID, session.ForkOptions{NewID: forkID, Mode: session.ForkAll})
	if err != nil {
		t.Fatalf("Fork error = %v", err)
	}
	if forked.HistoryBase == nil {
		t.Fatalf("paginated fork lost its history reference: %#v", forked)
	}

	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	parentParams := &turn.TurnStartParams{ThreadID: string(parentID), Model: modelID}
	forkParams := &turn.TurnStartParams{ThreadID: string(forkID), Model: modelID}
	parentStatus := router.compactTokenStatusForTurn(string(parentID), modelID, parentParams)
	forkStatus := router.compactTokenStatusForTurn(string(forkID), modelID, forkParams)
	if !parentStatus.ShouldCompact {
		t.Fatalf("parent with no recorded usage must compact from its history: %#v", parentStatus)
	}
	if forkStatus.ActiveContextTokens < parentStatus.ActiveContextTokens {
		t.Fatalf("fork measured %d tokens, parent measured %d: inherited history was dropped",
			forkStatus.ActiveContextTokens, parentStatus.ActiveContextTokens)
	}
	if !forkStatus.ShouldCompact {
		t.Fatalf("side fork skipped pre-turn compaction: %#v", forkStatus)
	}

	forkRecord, err := store.Read(forkID, true, true)
	if err != nil {
		t.Fatalf("Read fork error = %v", err)
	}
	if got := stringFromAny(forkRecord.Metadata.Extra["auto_compact_fallback_prompt"]); got != "Keep going." {
		t.Fatalf("fork dropped the config-derived fallback prompt: %q", got)
	}

	info := router.modelInfoForRuntimeWithConfig(modelID, nil)
	if info == nil || info.ContextWindow != 272000 {
		t.Fatalf("synthetic model window = %#v, want Rust's 272000 fallback", info)
	}
	if budget := compactionHistoryTokenBudget(info); budget != 226100 {
		t.Fatalf("compaction history budget = %d, want 226100 (95%% window * 7/8)", budget)
	}
}
