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
