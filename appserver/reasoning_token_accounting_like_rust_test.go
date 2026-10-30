package appserver

import (
	"encoding/json"
	"strings"
	"testing"

	"codex_go/model"
	"codex_go/session"
	"codex_go/turn"
)

// Rust #57839: the reasoning-inclusion decision travels with the saved token
// usage (`TokenUsageInfo::last_usage_includes_reasoning`), so resuming a thread
// keeps the accounting instead of re-deriving it from the current model.
func TestResumedTokenUsageKeepsReasoningAccountingLikeRust(t *testing.T) {
	raw, err := json.Marshal(map[string]any{
		"type": "reasoning", "summary": []any{}, "encrypted_content": strings.Repeat("A", 4_000),
	})
	if err != nil {
		t.Fatal(err)
	}
	store := session.NewStore(t.TempDir())
	createRecord := func(id string, extra map[string]any) {
		now := fixedTime()
		if _, ok := extra["last_token_usage"]; !ok {
			usage := map[string]any{"total_tokens": int64(80)}
			extra["last_token_usage"] = usage
			extra["total_token_usage"] = usage
		}
		if err := store.Create(&session.Record{
			ID: session.ThreadID(id), SessionID: id,
			CreatedAt: now, UpdatedAt: now, RecencyAt: now,
			Metadata: session.Metadata{Model: "gpt-5.4", Extra: extra},
			Items: []session.Item{
				{ID: "u1", Type: "message", Role: "user", Text: "first", CreatedAt: now},
				{ID: "r1", Type: "reasoning", Raw: raw, CreatedAt: now},
				{ID: "u2", Type: "message", Role: "user", Text: "next", CreatedAt: now},
				{ID: "a1", Type: "agent_message", Role: "assistant", Text: "answer", CreatedAt: now},
			},
		}); err != nil {
			t.Fatalf("Create record %s error = %v", id, err)
		}
	}
	createRecord("thread-info-includes", map[string]any{
		"token_usage_info": map[string]any{
			"total_token_usage":             map[string]any{"total_tokens": int64(80)},
			"last_token_usage":              map[string]any{"total_tokens": int64(80)},
			"last_usage_includes_reasoning": true,
		},
	})
	createRecord("thread-info-excludes", map[string]any{
		"token_usage_info": map[string]any{
			"total_token_usage":             map[string]any{"total_tokens": int64(80)},
			"last_token_usage":              map[string]any{"total_tokens": int64(80)},
			"last_usage_includes_reasoning": false,
		},
	})
	// Rollouts written before #57839 kept the bit beside the usage map.
	createRecord("thread-legacy-extra", map[string]any{"server_reasoning_included": true})
	// Rust's legacy count re-estimates retained reasoning when neither exists.
	createRecord("thread-legacy-missing", map[string]any{})

	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	params := &turn.TurnStartParams{Config: map[string]any{"model_auto_compact_token_limit": 81}}

	cases := []struct {
		threadID     string
		wantCompacts bool
	}{
		{"thread-info-includes", false},
		{"thread-info-excludes", true},
		{"thread-legacy-extra", false},
		{"thread-legacy-missing", true},
	}
	for _, tc := range cases {
		status := router.compactTokenStatusForTurn(tc.threadID, "gpt-5.4", params)
		if status.ShouldCompact != tc.wantCompacts {
			t.Fatalf("%s ShouldCompact = %v (active %d), want %v", tc.threadID, status.ShouldCompact, status.ActiveContextTokens, tc.wantCompacts)
		}
	}
}

// Rust #57839: a fork inherits its source's saved usage accounting.
func TestForkKeepsReasoningAccountingLikeRust(t *testing.T) {
	now := fixedTime()
	store := session.NewStore(t.TempDir())
	raw, err := json.Marshal(map[string]any{
		"type": "reasoning", "summary": []any{}, "encrypted_content": strings.Repeat("A", 4_000),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Create(&session.Record{
		ID: "thread-source-reasoning", SessionID: "thread-source-reasoning",
		CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{Model: "gpt-5.4", Extra: map[string]any{
			"last_token_usage":  map[string]any{"total_tokens": int64(80)},
			"total_token_usage": map[string]any{"total_tokens": int64(80)},
			"token_usage_info": map[string]any{
				"total_token_usage":             map[string]any{"total_tokens": int64(80)},
				"last_token_usage":              map[string]any{"total_tokens": int64(80)},
				"last_usage_includes_reasoning": true,
			},
		}},
		Items: []session.Item{
			{ID: "u1", Type: "message", Role: "user", Text: "first", CreatedAt: now},
			{ID: "r1", Type: "reasoning", Raw: raw, CreatedAt: now},
			{ID: "a1", Type: "agent_message", Role: "assistant", Text: "answer", CreatedAt: now},
		},
	}); err != nil {
		t.Fatalf("Create record error = %v", err)
	}
	source, err := store.Read("thread-source-reasoning", true, true)
	if err != nil {
		t.Fatalf("Read record error = %v", err)
	}
	forked, err := store.ForkRecord(source, session.ForkOptions{NewID: "thread-fork-reasoning", Mode: session.ForkAll, Now: now})
	if err != nil {
		t.Fatalf("ForkRecord() error = %v", err)
	}
	if got := tokenUsageIncludesReasoning(forked.Metadata.Extra); !got {
		t.Fatalf("forked usage lost the reasoning accounting: %#v", forked.Metadata.Extra["token_usage_info"])
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	params := &turn.TurnStartParams{Config: map[string]any{"model_auto_compact_token_limit": 81}}
	if status := router.compactTokenStatusForTurn("thread-fork-reasoning", "gpt-5.4", params); status.ShouldCompact {
		t.Fatalf("fork must not re-charge reasoning the source already accounted for: %#v", status)
	}
}

// Rust #57839: the response's own accounting decision is stored with the usage
// it was computed from, so a later resume reads it instead of the model default.
func TestPersistCompactTokenStatusStoresReasoningAccountingLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	now := fixedTime()
	if err := store.Create(&session.Record{
		ID: "thread-persist-reasoning", SessionID: "thread-persist-reasoning",
		CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{Model: "gpt-5.4"},
	}); err != nil {
		t.Fatalf("Create record error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	reported := false
	usage := model.AgentUsage{InputTokens: 10, OutputTokens: 2, TotalTokens: 12}
	if _, _, err := router.persistCompactTokenStatus("thread-persist-reasoning", "gpt-5.4", &turn.TurnStartParams{}, usage, usage, &reported); err != nil {
		t.Fatalf("persistCompactTokenStatus() error = %v", err)
	}
	record, err := store.Read("thread-persist-reasoning", true, true)
	if err != nil {
		t.Fatalf("Read record error = %v", err)
	}
	info, _ := record.Metadata.Extra["token_usage_info"].(map[string]any)
	value, ok := info["last_usage_includes_reasoning"].(bool)
	if !ok || value {
		t.Fatalf("token_usage_info.last_usage_includes_reasoning = %#v, want the reported false", info["last_usage_includes_reasoning"])
	}
}
