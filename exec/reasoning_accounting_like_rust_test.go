package exec

import (
	"encoding/json"
	"testing"

	"codex_go/config"
	"codex_go/protocol"
	"codex_go/session"
)

// Rust #57839: the reasoning-inclusion decision is saved with the usage it was
// computed from, so exec-resumed threads keep the accounting across restarts.
func TestExecCompactStatusHonorsSavedReasoningAccountingLikeRust(t *testing.T) {
	cfg, _ := config.Load("")
	cfg.Values["model_auto_compact_token_limit"] = int64(81)
	raw, err := json.Marshal(map[string]any{
		"type": "reasoning", "summary": []any{}, "encrypted_content": repeatString("A", 4_000),
	})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name         string
		extra        map[string]any
		wantCompacts bool
	}{
		{
			name: "usage info includes reasoning",
			extra: map[string]any{
				"token_usage_info": map[string]any{
					"total_token_usage":             map[string]any{"total_tokens": int64(80)},
					"last_token_usage":              map[string]any{"total_tokens": int64(80)},
					"last_usage_includes_reasoning": true,
				},
			},
			wantCompacts: false,
		},
		{
			name: "usage info excludes reasoning",
			extra: map[string]any{
				"token_usage_info": map[string]any{
					"total_token_usage":             map[string]any{"total_tokens": int64(80)},
					"last_token_usage":              map[string]any{"total_tokens": int64(80)},
					"last_usage_includes_reasoning": false,
				},
			},
			wantCompacts: true,
		},
		{
			name:         "legacy bit beside the usage map",
			extra:        map[string]any{"server_reasoning_included": true},
			wantCompacts: false,
		},
		{
			name:         "legacy count without either bit",
			extra:        map[string]any{},
			wantCompacts: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			extra := map[string]any{
				"last_token_usage":  map[string]any{"total_tokens": int64(80)},
				"total_token_usage": map[string]any{"total_tokens": int64(80)},
			}
			for key, value := range tc.extra {
				extra[key] = value
			}
			record := &session.Record{
				ID:       "thread-exec-reasoning",
				Metadata: session.Metadata{Model: "gpt-5.4", Extra: extra},
				Items: []session.Item{
					{ID: "u1", Type: "message", Role: "user", Text: "first", CreatedAt: fixedExecTime()},
					{ID: "r1", Type: "reasoning", Raw: raw, CreatedAt: fixedExecTime()},
					{ID: "u2", Type: "message", Role: "user", Text: "next", CreatedAt: fixedExecTime()},
					{ID: "a1", Type: "agent_message", Role: "assistant", Text: "answer", CreatedAt: fixedExecTime()},
				},
			}
			status := execCompactStatus(record, "gpt-5.4", cfg)
			if status.ShouldCompact != tc.wantCompacts {
				t.Fatalf("ShouldCompact = %v (active %d), want %v", status.ShouldCompact, status.ActiveContextTokens, tc.wantCompacts)
			}
		})
	}
}

// Rust #57839: a written usage record carries its own accounting decision so a
// later resume does not have to re-derive it.
func TestExecTokenUsageMetadataStoresReasoningAccountingLikeRust(t *testing.T) {
	extra := map[string]any{"server_reasoning_included": true}
	out := execTokenUsageMetadata(extra, &protocol.ThreadTokenUsage{Last: protocol.Usage{TotalTokens: 10}})
	info, _ := out["token_usage_info"].(map[string]any)
	if value, ok := info["last_usage_includes_reasoning"].(bool); !ok || !value {
		t.Fatalf("token_usage_info = %#v, want last_usage_includes_reasoning true", info)
	}
	if value := execTokenUsageIncludesReasoning(out); !value {
		t.Fatalf("written usage lost the accounting decision: %#v", out["token_usage_info"])
	}
}

func repeatString(value string, count int) string {
	out := make([]byte, 0, len(value)*count)
	for i := 0; i < count; i++ {
		out = append(out, value...)
	}
	return string(out)
}
