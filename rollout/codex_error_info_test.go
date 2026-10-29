package rollout

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRecordFromPathToleratesUnknownCodexErrorClassifications mirrors Rust
// #46482's rollout_line_decoder_preserves_terminal_records_with_unknown_errors:
// an unknown classification (bare string or tagged payload) becomes "other" so
// the saved record stays readable, while a known classification with an invalid
// payload makes the record unreadable.
func TestRecordFromPathToleratesUnknownCodexErrorClassifications(t *testing.T) {
	for _, testCase := range []struct {
		name           string
		errorInfo      string
		wantStatus     string
		wantClassified string
	}{
		{
			name:           "unknown bare string",
			errorInfo:      `"future_error"`,
			wantStatus:     "failed",
			wantClassified: CodexErrorInfoOther,
		},
		{
			name:           "unknown tagged payload",
			errorInfo:      `{"future_error":{"detail":"new payload"}}`,
			wantStatus:     "failed",
			wantClassified: CodexErrorInfoOther,
		},
		{
			name:           "known classification preserved",
			errorInfo:      `{"response_stream_disconnected":{"http_status_code":503}}`,
			wantStatus:     "failed",
			wantClassified: "",
		},
		{
			name:       "known tag with invalid payload is unreadable",
			errorInfo:  `{"active_turn_not_steerable":{"turn_kind":"unknown"}}`,
			wantStatus: "inProgress",
		},
		{
			name:       "multi-key object is unreadable",
			errorInfo:  `{"future_error":null,"other":null}`,
			wantStatus: "inProgress",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			home := t.TempDir()
			path := filepath.Join(home, SessionsSubdir, "rollout-2026-07-27T01-02-03-thread-1.jsonl")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatalf("MkdirAll() error = %v", err)
			}
			data := strings.Join([]string{
				`{"timestamp":"2026-07-27T01:02:03Z","type":"session_meta","payload":{"id":"thread-1","timestamp":"2026-07-27T01:02:03Z"}}`,
				`{"timestamp":"2026-07-27T01:02:04Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-a","started_at":10}}`,
				`{"timestamp":"2026-07-27T01:02:05Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-a","error":{"message":"The request was blocked.","codex_error_info":` + testCase.errorInfo + `},"completed_at":20,"duration_ms":10000}}`,
				``,
			}, "\n")
			if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
				t.Fatalf("WriteFile() error = %v", err)
			}
			record, err := RecordFromPath(path, false)
			if err != nil {
				t.Fatalf("RecordFromPath() error = %v", err)
			}
			if len(record.Metadata.RolloutTurns) != 1 {
				t.Fatalf("rollout turns = %#v", record.Metadata.RolloutTurns)
			}
			turn := record.Metadata.RolloutTurns[0]
			if turn.Status != testCase.wantStatus {
				t.Fatalf("turn status = %q, want %q (turn %#v)", turn.Status, testCase.wantStatus, turn)
			}
			if testCase.wantStatus != "failed" {
				if turn.ErrorMessage != "" || turn.CodexErrorInfo != nil {
					t.Fatalf("unreadable record applied an error: %#v", turn)
				}
				return
			}
			if turn.ErrorMessage != "The request was blocked." {
				t.Fatalf("turn error message = %q", turn.ErrorMessage)
			}
			if testCase.wantClassified == CodexErrorInfoOther {
				if turn.CodexErrorInfo != CodexErrorInfoOther {
					t.Fatalf("codexErrorInfo = %#v, want other", turn.CodexErrorInfo)
				}
				return
			}
			encoded, _ := json.Marshal(turn.CodexErrorInfo)
			if !strings.Contains(string(encoded), "response_stream_disconnected") {
				t.Fatalf("known classification was rewritten: %s", string(encoded))
			}
		})
	}
}

// Rust deserializes CodexErrorInfo as an externally tagged enum, so only a bare
// known tag, a single-key known tag with a valid payload, or an unknown single
// tag is representable. Empty/multi-key objects, unit tags carrying a payload,
// payload tags written as bare strings, and non-string/non-object JSON are
// unreadable rather than being classified as other.
func TestCodexErrorInfoRejectsNonEnumShapesLikeRust(t *testing.T) {
	for _, raw := range []any{
		map[string]any{},
		map[string]any{"future_error": nil, "other": nil},
		map[string]any{"other": map[string]any{"detail": "new payload"}},
		map[string]any{"context_window_exceeded": 1.0},
		"response_stream_disconnected",
		42.0,
		true,
		[]any{"other"},
	} {
		if classified, ok := normalizeCodexErrorInfoClassification(raw); ok {
			t.Fatalf("%#v classified as %#v, want unreadable", raw, classified)
		}
	}
}

// Rust #57452 adds policy-restricted capacity as a payload-free rollout
// classification. Both the core snake_case spelling and the app-server
// camelCase spelling stay readable and are preserved verbatim.
func TestCodexErrorInfoPreservesPolicyCapacityTagsLikeRust(t *testing.T) {
	for _, tag := range []string{"policy_capacity_unavailable", "policyCapacityUnavailable"} {
		classified, ok := normalizeCodexErrorInfoClassification(tag)
		if !ok || classified != tag {
			t.Fatalf("bare %q normalized to %#v (ok=%v), want itself", tag, classified, ok)
		}
		classified, ok = normalizeCodexErrorInfoClassification(map[string]any{tag: nil})
		if !ok {
			t.Fatalf("tagged %q was rejected", tag)
		}
		if _, ok := classified.(map[string]any)[tag]; !ok {
			t.Fatalf("tagged %q normalized to %#v", tag, classified)
		}
	}
}
