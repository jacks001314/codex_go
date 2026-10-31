package appserver

import (
	"strings"
	"testing"

	"codex_go/compact"
	"codex_go/model"
	"codex_go/session"
	"codex_go/turn"
)

// Rust #58404: the app server records the history boundary a server-reported
// usage covers so a later resume only estimates items added after it.
func TestPersistCompactTokenStatusRecordsUsageBoundaryLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	now := fixedTime()
	if err := store.Create(&session.Record{
		ID: "thread-usage-boundary", SessionID: "thread-usage-boundary",
		CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{Model: "gpt-5.4"},
		Items: []session.Item{
			{ID: "u1", Type: "message", Role: "user", Text: "hello", CreatedAt: now},
			{ID: "a1", Type: "agent_message", Role: "assistant", Text: "answer", CreatedAt: now},
		},
	}); err != nil {
		t.Fatalf("Create record error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	usage := model.AgentUsage{InputTokens: 10, OutputTokens: 2, TotalTokens: 12}
	if _, _, err := router.persistCompactTokenStatus("thread-usage-boundary", "gpt-5.4", &turn.TurnStartParams{}, usage, usage); err != nil {
		t.Fatalf("persistCompactTokenStatus() error = %v", err)
	}
	record, err := store.Read("thread-usage-boundary", true, true)
	if err != nil {
		t.Fatalf("Read record error = %v", err)
	}
	boundary := tokenUsageBoundaryFromMetadata(record.Metadata.Extra)
	if boundary != len(record.Items) {
		t.Fatalf("token_usage_boundary = %d, want the %d covered items", boundary, len(record.Items))
	}
}

// Rust #58404: a persisted boundary stops the estimate from re-charging items
// the last usage report already covered.
func TestCompactTokenStatusHonorsStoredUsageBoundaryLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	now := fixedTime()
	items := []session.Item{
		{ID: "a1", Type: "agent_message", Role: "assistant", Text: "answer", CreatedAt: now},
		{ID: "u1", Type: "message", Role: "user", Text: strings.Repeat("u", 400), CreatedAt: now},
		{ID: "o1", Type: "tool_output", CallID: "call-1", Text: strings.Repeat("o", 400), CreatedAt: now},
	}
	create := func(id string, extra map[string]any) {
		if err := store.Create(&session.Record{
			ID: session.ThreadID(id), SessionID: id,
			CreatedAt: now, UpdatedAt: now, RecencyAt: now,
			Metadata: session.Metadata{Model: "gpt-5.4", Extra: extra},
			Items:    items,
		}); err != nil {
			t.Fatalf("Create record %s error = %v", id, err)
		}
	}
	lastUsage := map[string]any{"total_tokens": int64(100)}
	create("thread-boundary-covered", map[string]any{
		"last_token_usage":     lastUsage,
		"token_usage_boundary": int64(len(items)),
	})
	create("thread-boundary-restored", map[string]any{
		"last_token_usage": lastUsage,
	})
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	params := &turn.TurnStartParams{Config: map[string]any{"model_auto_compact_token_limit": 101}}
	if status := router.compactTokenStatusForTurn("thread-boundary-covered", "gpt-5.4", params); status.ShouldCompact {
		t.Fatalf("covered boundary must not re-charge the trailing items: %#v", status)
	}
	if status := router.compactTokenStatusForTurn("thread-boundary-restored", "gpt-5.4", params); !status.ShouldCompact {
		t.Fatalf("restored-history approximation must still estimate the trailing items: %#v", status)
	}
}

// Rust #58404: a response without usable usage keeps everything it retained in
// the mid-turn estimate.
func TestMidTurnActiveContextTokensKeepUnreportedUsageLikeRust(t *testing.T) {
	history := []compact.Item{
		{ID: "a1", Type: "agent_message", Role: "assistant", Text: "answer"},
		{ID: "u1", Type: "message", Role: "user", Text: strings.Repeat("u", 400)},
	}
	reported := &turn.AgentLoopResult{Responses: []*model.AgentResponse{{
		ResponseID: "resp-1",
		Items:      []model.AgentItem{{ID: "a1", Type: "agent_message"}},
		Usage:      model.AgentUsage{InputTokens: 90, OutputTokens: 10, TotalTokens: 100},
	}}}
	boundary, total, ok := inTurnUsageBoundaryLikeRust(history, reported)
	if !ok || boundary != 1 || total != 100 {
		t.Fatalf("inTurnUsageBoundaryLikeRust() = (%d, %d, %t), want (1, 100, true)", boundary, total, ok)
	}
	tail := compact.EstimateItemTokens(&history[1])
	if got := midTurnActiveContextTokensLikeRust(history, reported, nil); got != 100+tail {
		t.Fatalf("mid-turn active context = %d, want the report plus the %d retained tokens", got, tail)
	}
	// Without any usable report every retained item is estimated.
	if got, want := midTurnActiveContextTokensLikeRust(history, &turn.AgentLoopResult{}, nil), compact.EstimateTokens(history); got != want {
		t.Fatalf("active context without usage = %d, want the whole-history estimate %d", got, want)
	}
	// A persisted boundary covers the history up to it.
	extra := map[string]any{
		"last_token_usage":     map[string]any{"total_tokens": int64(50)},
		"token_usage_boundary": int64(len(history)),
	}
	if got := midTurnActiveContextTokensLikeRust(history, nil, extra); got != 50 {
		t.Fatalf("active context with a covered boundary = %d, want 50", got)
	}
}

// A usage record written before the boundary existed keeps the restored-history
// approximation.
func TestTokenUsageBoundaryFromMetadataDefaultsToRestoredApproximationLikeRust(t *testing.T) {
	if got := tokenUsageBoundaryFromMetadata(nil); got != -1 {
		t.Fatalf("tokenUsageBoundaryFromMetadata(nil) = %d, want -1", got)
	}
	if got := tokenUsageBoundaryFromMetadata(map[string]any{}); got != -1 {
		t.Fatalf("tokenUsageBoundaryFromMetadata({}) = %d, want -1", got)
	}
	if got := tokenUsageBoundaryFromMetadata(map[string]any{"token_usage_boundary": int64(4)}); got != 4 {
		t.Fatalf("tokenUsageBoundaryFromMetadata() = %d, want 4", got)
	}
}
