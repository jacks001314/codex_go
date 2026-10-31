package rollout

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
)

// These expectations come from the real codex-cli 0.166 binary: the source
// records below were migrated with `migrate-rollouts --apply --json` and both
// implementations produce these bytes and this bytes_processed value (see
// .tmp-usealign/out/r76-events.txt).

func writeCanonicalMigrationRawRollout(t *testing.T, source string) (string, string) {
	t.Helper()
	home := t.TempDir()
	path := filepath.Join(home, SessionsSubdir, "rollout-2025-01-02T03-04-05-"+canonicalMigrationThreadID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return home, path
}

func TestCanonicalizeRolloutDropsTransientAndRetiredLegacyEventsLikeRust(t *testing.T) {
	cases := []struct {
		name           string
		source         string
		want           string
		bytesProcessed uint64
	}{
		{
			name: "transient",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"event_msg","payload":{"type":"task_started","turn_id":"t1","started_at":1735787046}}
{"timestamp":"2025-01-02T03:04:07.000Z","type":"event_msg","payload":{"type":"exec_command_begin","call_id":"c1","turn_id":"t1","command":["ls"],"cwd":"C:\\work"}}
{"timestamp":"2025-01-02T03:04:08.000Z","type":"event_msg","payload":{"type":"exec_command_output_delta","call_id":"c1","chunk":"partial"}}
{"timestamp":"2025-01-02T03:04:09.000Z","type":"event_msg","payload":{"type":"exec_command_end","call_id":"c1","turn_id":"t1","command":["ls"],"cwd":"C:\\work","parsed_cmd":[],"source":"agent","stdout":"out","stderr":"","aggregated_output":"out","exit_code":0,"duration":{"secs":1,"nanos":0},"formatted_output":"","status":"completed"}}
{"timestamp":"2025-01-02T03:04:10.000Z","type":"event_msg","payload":{"type":"mcp_tool_call_begin","call_id":"m1","invocation":{"server":"s","tool":"t","arguments":{}}}}
{"timestamp":"2025-01-02T03:04:11.000Z","type":"event_msg","payload":{"type":"plan_update","plan":[{"step":"a","status":"pending"}]}}
{"timestamp":"2025-01-02T03:04:12.000Z","type":"event_msg","payload":{"type":"item_started","thread_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","turn_id":"t1","item":{"type":"AgentMessage","id":"i1","content":[]}}}
{"timestamp":"2025-01-02T03:04:13.000Z","type":"event_msg","payload":{"type":"error","message":"boom"}}
{"timestamp":"2025-01-02T03:04:14.000Z","type":"event_msg","payload":{"type":"warning","message":"careful"}}
{"timestamp":"2025-01-02T03:04:15.000Z","type":"event_msg","payload":{"type":"turn_diff","unified_diff":"--- a\n+++ b\n"}}
{"timestamp":"2025-01-02T03:04:16.000Z","type":"event_msg","payload":{"type":"token_count","input_tokens":10,"output_tokens":2}}
{"timestamp":"2025-01-02T03:04:17.000Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"t1","completed_at":1735787047}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":1,"type":"event_msg","payload":{"type":"task_started","turn_id":"t1","started_at":1735787046,"model_context_window":null,"collaboration_mode_kind":"default"}}
{"timestamp":"2025-01-02T03:04:09.000Z","ordinal":2,"type":"event_msg","payload":{"type":"item_completed","thread_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","turn_id":"t1","item":{"type":"CommandExecution","id":"c1","command":["ls"],"cwd":"file:///C:/work","parsed_cmd":[],"source":"agent","status":"completed","aggregated_output":"out","exit_code":0,"duration":{"secs":1,"nanos":0}},"completed_at_ms":1735787049000}}
{"timestamp":"2025-01-02T03:04:16.000Z","ordinal":3,"type":"event_msg","payload":{"type":"token_count","info":null,"rate_limits":null}}
{"timestamp":"2025-01-02T03:04:17.000Z","ordinal":4,"type":"event_msg","payload":{"type":"task_complete","turn_id":"t1","last_agent_message":null,"completed_at":1735787047}}
`,
			bytesProcessed: 5003,
		},
		{
			name: "patch_ok",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"event_msg","payload":{"type":"task_started","turn_id":"t1","started_at":1735787046}}
{"timestamp":"2025-01-02T03:04:07.000Z","type":"event_msg","payload":{"type":"patch_apply_end","call_id":"p1","turn_id":"t1","stdout":"done","stderr":"","success":true,"changes":{"a.txt":{"type":"add","content":"hi"}},"status":"completed"}}
{"timestamp":"2025-01-02T03:04:08.000Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"t1","completed_at":1735787047}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":1,"type":"event_msg","payload":{"type":"task_started","turn_id":"t1","started_at":1735787046,"model_context_window":null,"collaboration_mode_kind":"default"}}
{"timestamp":"2025-01-02T03:04:07.000Z","ordinal":2,"type":"event_msg","payload":{"type":"item_completed","thread_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","turn_id":"t1","item":{"type":"FileChange","id":"p1","changes":{"a.txt":{"type":"add","content":"hi"}},"status":"completed","stdout":"done"},"completed_at_ms":1735787047000}}
{"timestamp":"2025-01-02T03:04:08.000Z","ordinal":3,"type":"event_msg","payload":{"type":"task_complete","turn_id":"t1","last_agent_message":null,"completed_at":1735787047}}
`,
			bytesProcessed: 3176,
		},
		{
			name: "patch_legacy",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"event_msg","payload":{"type":"task_started","turn_id":"t1","started_at":1735787046}}
{"timestamp":"2025-01-02T03:04:07.000Z","type":"event_msg","payload":{"type":"patch_apply_end","call_id":"p1","turn_id":"t1","stdout":"","stderr":"","success":true,"changes":{}}}
{"timestamp":"2025-01-02T03:04:08.000Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"t1","completed_at":1735787047}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":1,"type":"event_msg","payload":{"type":"task_started","turn_id":"t1","started_at":1735787046,"model_context_window":null,"collaboration_mode_kind":"default"}}
{"timestamp":"2025-01-02T03:04:08.000Z","ordinal":2,"type":"event_msg","payload":{"type":"task_complete","turn_id":"t1","last_agent_message":null,"completed_at":1735787047}}
`,
			bytesProcessed: 2450,
		},
		{
			name: "item_completed_legacy",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"event_msg","payload":{"type":"task_started","turn_id":"t1","started_at":1735787046}}
{"timestamp":"2025-01-02T03:04:07.000Z","type":"event_msg","payload":{"type":"item_completed","turn_id":"t1","item":{"type":"AgentMessage","content":[{"type":"Text","text":"yo"}]},"completed_at_ms":1735787047000}}
{"timestamp":"2025-01-02T03:04:08.000Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"t1","completed_at":1735787047}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":1,"type":"event_msg","payload":{"type":"task_started","turn_id":"t1","started_at":1735787046,"model_context_window":null,"collaboration_mode_kind":"default"}}
{"timestamp":"2025-01-02T03:04:08.000Z","ordinal":2,"type":"event_msg","payload":{"type":"task_complete","turn_id":"t1","last_agent_message":null,"completed_at":1735787047}}
`,
			bytesProcessed: 2485,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home, path := writeCanonicalMigrationRawRollout(t, tc.source)
			bytesProcessed, err := CanonicalizeRolloutWithStats(home, path)
			if err != nil {
				t.Fatalf("CanonicalizeRolloutWithStats: %v", err)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("ReadFile: %v", err)
			}
			if string(got) != tc.want {
				t.Fatalf("migrated rollout mismatch\n got: %s\nwant: %s", got, tc.want)
			}
			if bytesProcessed != tc.bytesProcessed {
				t.Fatalf("bytes_processed = %d, want %d", bytesProcessed, tc.bytesProcessed)
			}
		})
	}
}

// Rust's migration charges a compressed rollout as the compressed file plus two
// decompressed passes, plus the header record and the two writes of the staged
// paginated rollout.
func TestCanonicalizeRolloutAccountsCompressedSourceBytesLikeRust(t *testing.T) {
	lines := []string{
		canonicalMigrationLines["meta"][0],
		canonicalMigrationLine(6, "event_msg", `{"type":"user_message","message":"compressed hello"}`),
		canonicalMigrationLine(7, "event_msg", `{"type":"agent_message","message":"compressed answer"}`),
	}
	home, plain := writeCanonicalMigrationRollout(t, lines)
	plainInfo, err := os.Stat(plain)
	if err != nil {
		t.Fatalf("Stat(plain): %v", err)
	}
	compressed := plain + ".zst"
	writeCompressedTestRollout(t, plain, compressed)
	if err := os.Remove(plain); err != nil {
		t.Fatalf("Remove(plain): %v", err)
	}
	compressedInfo, err := os.Stat(compressed)
	if err != nil {
		t.Fatalf("Stat(compressed): %v", err)
	}

	bytesProcessed, err := CanonicalizeRolloutWithStats(home, compressed)
	if err != nil {
		t.Fatalf("CanonicalizeRolloutWithStats: %v", err)
	}

	file, err := os.Open(compressed)
	if err != nil {
		t.Fatalf("Open(compressed): %v", err)
	}
	defer func() { _ = file.Close() }()
	decoder, err := zstd.NewReader(file)
	if err != nil {
		t.Fatalf("zstd.NewReader: %v", err)
	}
	defer decoder.Close()
	output, err := io.ReadAll(decoder)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !strings.Contains(string(output), `"history_mode":"paginated"`) {
		t.Fatalf("migrated rollout is not paginated: %s", output)
	}
	headerBytes := uint64(len(lines[0])) + 1
	want := uint64(compressedInfo.Size()) + 2*uint64(plainInfo.Size()) + headerBytes + 2*uint64(len(output))
	if bytesProcessed != want {
		t.Fatalf("bytes_processed = %d, want %d", bytesProcessed, want)
	}
}
