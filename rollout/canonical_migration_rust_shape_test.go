package rollout

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These expectations were captured from the real codex-cli 0.166 binary with
// `migrate-rollouts --apply --json`; the migrated bytes and the reported
// bytes_processed must stay identical (see .tmp-usealign/out/r75-migrate-compare.txt).

const canonicalMigrationThreadID = "0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"

func canonicalMigrationTimestamp(second int) string {
	return "2025-01-02T03:04:" + twoDigits(second) + ".000Z"
}

func twoDigits(value int) string {
	if value < 10 {
		return "0" + string(rune('0'+value))
	}
	return string(rune('0'+value/10)) + string(rune('0'+value%10))
}

// canonicalMigrationLines are raw JSONL records: legacy rollouts keep Rust's
// serde field order, so the fixture must too.
var canonicalMigrationLines = map[string][]string{
	"meta": {`{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}`},
}

func canonicalMigrationLine(second int, lineType string, payload string) string {
	return `{"timestamp":"` + canonicalMigrationTimestamp(second) + `","type":"` + lineType + `","payload":` + payload + `}`
}

func writeCanonicalMigrationRollout(t *testing.T, lines []string) (string, string) {
	t.Helper()
	home := t.TempDir()
	path := filepath.Join(home, SessionsSubdir, "rollout-2025-01-02T03-04-05-"+canonicalMigrationThreadID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	data := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return home, path
}

const canonicalMigrationHead = `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}`

func TestCanonicalizeRolloutReplaysResponseItemsAndNormalizesTokenCountLikeRust(t *testing.T) {
	lines := []string{
		canonicalMigrationLines["meta"][0],
		canonicalMigrationLine(6, "response_item", `{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}`),
		canonicalMigrationLine(7, "response_item", `{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hi there"}]}`),
		canonicalMigrationLine(8, "event_msg", `{"type":"token_count","input_tokens":10,"output_tokens":2}`),
	}
	home, path := writeCanonicalMigrationRollout(t, lines)
	bytesProcessed, err := CanonicalizeRolloutWithStats(home, path)
	if err != nil {
		t.Fatalf("CanonicalizeRolloutWithStats: %v", err)
	}
	want := canonicalMigrationHead + "\n" +
		`{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":1,"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}}` + "\n" +
		`{"timestamp":"2025-01-02T03:04:07.000Z","ordinal":2,"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hi there"}]}}` + "\n" +
		`{"timestamp":"2025-01-02T03:04:08.000Z","ordinal":3,"type":"event_msg","payload":{"type":"token_count","info":null,"rate_limits":null}}` + "\n"
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != want {
		t.Fatalf("migrated rollout mismatch\n got: %s\nwant: %s", got, want)
	}
	if bytesProcessed != 2648 {
		t.Fatalf("bytes_processed = %d, want 2648", bytesProcessed)
	}
}

func TestCanonicalizeRolloutSynthesizesImplicitTurnsLikeRust(t *testing.T) {
	lines := []string{
		canonicalMigrationLines["meta"][0],
		canonicalMigrationLine(6, "event_msg", `{"type":"user_message","message":"run ls"}`),
		canonicalMigrationLine(7, "event_msg", `{"type":"agent_message","message":"sure"}`),
		canonicalMigrationLine(8, "event_msg", `{"type":"token_count","input_tokens":10,"output_tokens":2}`),
	}
	home, path := writeCanonicalMigrationRollout(t, lines)
	bytesProcessed, err := CanonicalizeRolloutWithStats(home, path)
	if err != nil {
		t.Fatalf("CanonicalizeRolloutWithStats: %v", err)
	}
	want := canonicalMigrationHead + "\n" +
		`{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":1,"type":"event_msg","payload":{"type":"task_started","turn_id":"rollout-1","started_at":1735787046,"model_context_window":null,"collaboration_mode_kind":"default"}}` + "\n" +
		`{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":2,"type":"event_msg","payload":{"type":"item_completed","thread_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","turn_id":"rollout-1","item":{"type":"UserMessage","id":"item-1","content":[{"type":"text","text":"run ls","text_elements":[]}]},"completed_at_ms":1735787046000}}` + "\n" +
		`{"timestamp":"2025-01-02T03:04:07.000Z","ordinal":3,"type":"event_msg","payload":{"type":"item_completed","thread_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","turn_id":"rollout-1","item":{"type":"AgentMessage","id":"item-2","content":[{"type":"Text","text":"sure"}]},"completed_at_ms":1735787047000}}` + "\n" +
		`{"timestamp":"2025-01-02T03:04:08.000Z","ordinal":4,"type":"event_msg","payload":{"type":"token_count","info":null,"rate_limits":null}}` + "\n" +
		`{"timestamp":"2025-01-02T03:04:08.000Z","ordinal":5,"type":"event_msg","payload":{"type":"task_complete","turn_id":"rollout-1","last_agent_message":null,"completed_at":1735787048}}` + "\n"
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != want {
		t.Fatalf("migrated rollout mismatch\n got: %s\nwant: %s", got, want)
	}
	if bytesProcessed != 3896 {
		t.Fatalf("bytes_processed = %d, want 3896", bytesProcessed)
	}
}

func TestCanonicalizeRolloutConvertsCommandExecutionLikeRust(t *testing.T) {
	lines := []string{
		canonicalMigrationLines["meta"][0],
		canonicalMigrationLine(6, "event_msg", `{"type":"task_started","turn_id":"t1","started_at":1735787046}`),
		canonicalMigrationLine(7, "response_item", `{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}`),
		canonicalMigrationLine(8, "event_msg", `{"type":"exec_command_end","call_id":"c1","turn_id":"t1","command":["bash","-lc","ls"],"cwd":"C:\\work","parsed_cmd":[],"source":"agent","stdout":"out","stderr":"","aggregated_output":"out","exit_code":0,"duration":{"secs":1,"nanos":0},"formatted_output":"","status":"completed"}`),
		canonicalMigrationLine(9, "event_msg", `{"type":"task_complete","turn_id":"t1","completed_at":1735787047,"last_agent_message":null}`),
	}
	home, path := writeCanonicalMigrationRollout(t, lines)
	bytesProcessed, err := CanonicalizeRolloutWithStats(home, path)
	if err != nil {
		t.Fatalf("CanonicalizeRolloutWithStats: %v", err)
	}
	want := canonicalMigrationHead + "\n" +
		`{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":1,"type":"event_msg","payload":{"type":"task_started","turn_id":"t1","started_at":1735787046,"model_context_window":null,"collaboration_mode_kind":"default"}}` + "\n" +
		`{"timestamp":"2025-01-02T03:04:07.000Z","ordinal":2,"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}}` + "\n" +
		`{"timestamp":"2025-01-02T03:04:08.000Z","ordinal":3,"type":"event_msg","payload":{"type":"item_completed","thread_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","turn_id":"t1","item":{"type":"CommandExecution","id":"c1","command":["bash","-lc","ls"],"cwd":"file:///C:/work","parsed_cmd":[],"source":"agent","status":"completed","aggregated_output":"out","exit_code":0,"duration":{"secs":1,"nanos":0}},"completed_at_ms":1735787048000}}` + "\n" +
		`{"timestamp":"2025-01-02T03:04:09.000Z","ordinal":4,"type":"event_msg","payload":{"type":"task_complete","turn_id":"t1","last_agent_message":null,"completed_at":1735787047}}` + "\n"
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != want {
		t.Fatalf("migrated rollout mismatch\n got: %s\nwant: %s", got, want)
	}
	if bytesProcessed != 3992 {
		t.Fatalf("bytes_processed = %d, want 3992", bytesProcessed)
	}
}

func TestCanonicalizeRolloutAccumulatesReasoningLikeRust(t *testing.T) {
	lines := []string{
		canonicalMigrationLines["meta"][0],
		canonicalMigrationLine(6, "event_msg", `{"type":"agent_reasoning","text":"thinking"}`),
		canonicalMigrationLine(7, "event_msg", `{"type":"agent_reasoning_raw_content","text":"raw"}`),
	}
	home, path := writeCanonicalMigrationRollout(t, lines)
	bytesProcessed, err := CanonicalizeRolloutWithStats(home, path)
	if err != nil {
		t.Fatalf("CanonicalizeRolloutWithStats: %v", err)
	}
	want := canonicalMigrationHead + "\n" +
		`{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":1,"type":"event_msg","payload":{"type":"task_started","turn_id":"rollout-1","started_at":1735787046,"model_context_window":null,"collaboration_mode_kind":"default"}}` + "\n" +
		`{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":2,"type":"event_msg","payload":{"type":"item_completed","thread_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","turn_id":"rollout-1","item":{"type":"Reasoning","id":"item-1","summary_text":["thinking"],"raw_content":[]},"completed_at_ms":1735787046000}}` + "\n" +
		`{"timestamp":"2025-01-02T03:04:07.000Z","ordinal":3,"type":"event_msg","payload":{"type":"item_completed","thread_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","turn_id":"rollout-1","item":{"type":"Reasoning","id":"item-1","summary_text":["thinking"],"raw_content":["raw"]},"completed_at_ms":1735787047000}}` + "\n" +
		`{"timestamp":"2025-01-02T03:04:07.000Z","ordinal":4,"type":"event_msg","payload":{"type":"task_complete","turn_id":"rollout-1","last_agent_message":null,"completed_at":1735787047}}` + "\n"
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != want {
		t.Fatalf("migrated rollout mismatch\n got: %s\nwant: %s", got, want)
	}
	if bytesProcessed != 3477 {
		t.Fatalf("bytes_processed = %d, want 3477", bytesProcessed)
	}
}

func TestCanonicalizeRolloutCountsRollbackConversionPassesLikeRust(t *testing.T) {
	lines := []string{
		canonicalMigrationLines["meta"][0],
		canonicalMigrationLine(6, "event_msg", `{"type":"task_started","turn_id":"t1","started_at":1735787046}`),
		canonicalMigrationLine(7, "event_msg", `{"type":"user_message","message":"first"}`),
		canonicalMigrationLine(8, "event_msg", `{"type":"agent_message","message":"answer-1"}`),
		canonicalMigrationLine(9, "event_msg", `{"type":"task_complete","turn_id":"t1","completed_at":1735787047}`),
		canonicalMigrationLine(10, "event_msg", `{"type":"task_started","turn_id":"t2","started_at":1735787048}`),
		canonicalMigrationLine(11, "event_msg", `{"type":"user_message","message":"rolled back"}`),
		canonicalMigrationLine(12, "event_msg", `{"type":"thread_rolled_back","num_turns":1}`),
	}
	home, path := writeCanonicalMigrationRollout(t, lines)
	bytesProcessed, err := CanonicalizeRolloutWithStats(home, path)
	if err != nil {
		t.Fatalf("CanonicalizeRolloutWithStats: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if strings.Contains(string(got), "rolled back") {
		t.Fatalf("rolled-back instruction survived canonicalization: %s", got)
	}
	if !strings.Contains(string(got), `"turn_id":"t1"`) {
		t.Fatalf("surviving turn lost in canonicalization: %s", got)
	}
	if bytesProcessed != 8183 {
		t.Fatalf("bytes_processed = %d, want 8183", bytesProcessed)
	}
}

// TestCanonicalizeRolloutDatesTaskCompleteFromLastWrittenRecordLikeRust pins the
// rule Rust's read_rollout_record implements: an undecodable line or a record
// the paginated policy drops leaves last_timestamp untouched, so the
// synthesized task_complete keeps the time of the last record actually written.
func TestCanonicalizeRolloutDatesTaskCompleteFromLastWrittenRecordLikeRust(t *testing.T) {
	lines := []string{
		canonicalMigrationLines["meta"][0],
		canonicalMigrationLine(6, "event_msg", `{"type":"user_message","message":"hello 0","kind":"plain"}`),
		canonicalMigrationLine(7, "response_item", `{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hi 0"}]}`),
		canonicalMigrationLine(10, "event_msg", `{"type":"totally_unknown_event_xyz","foo":1}`),
	}
	home, path := writeCanonicalMigrationRollout(t, lines)
	if _, err := CanonicalizeRolloutWithStats(home, path); err != nil {
		t.Fatalf("CanonicalizeRolloutWithStats: %v", err)
	}
	want := canonicalMigrationHead + "\n" +
		`{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":1,"type":"event_msg","payload":{"type":"task_started","turn_id":"rollout-1","started_at":1735787046,"model_context_window":null,"collaboration_mode_kind":"default"}}` + "\n" +
		`{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":2,"type":"event_msg","payload":{"type":"item_completed","thread_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","turn_id":"rollout-1","item":{"type":"UserMessage","id":"item-1","content":[{"type":"text","text":"hello 0","text_elements":[]}]},"completed_at_ms":1735787046000}}` + "\n" +
		`{"timestamp":"2025-01-02T03:04:07.000Z","ordinal":3,"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hi 0"}]}}` + "\n" +
		`{"timestamp":"2025-01-02T03:04:07.000Z","ordinal":4,"type":"event_msg","payload":{"type":"task_complete","turn_id":"rollout-1","last_agent_message":null,"completed_at":1735787047}}` + "\n"
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != want {
		t.Fatalf("migrated rollout mismatch\n got: %s\nwant: %s", got, want)
	}
}

// TestCanonicalizeRolloutDropsUndecodableExecRecordsLikeRust covers the legacy
// exec_command_end decode: a drive-letter cwd is normalized through
// LegacyAppPathString into a file: URI, while a record whose cwd cannot be
// represented is dropped without dating the synthesized task_complete.
func TestCanonicalizeRolloutDropsUndecodableExecRecordsLikeRust(t *testing.T) {
	lines := []string{
		canonicalMigrationLines["meta"][0],
		canonicalMigrationLine(6, "event_msg", `{"type":"user_message","message":"hello 0","kind":"plain"}`),
		canonicalMigrationLine(7, "response_item", `{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hi 0"}]}`),
		canonicalMigrationLine(8, "event_msg", `{"type":"exec_command_end","call_id":"c1","turn_id":"t1","command":["bash","-lc","ls"],"cwd":"C:/work","parsed_cmd":[],"source":"agent","stdout":"out","stderr":"","aggregated_output":"out","exit_code":0,"duration":{"secs":1,"nanos":0},"status":"completed"}`),
		canonicalMigrationLine(9, "event_msg", `{"type":"exec_command_end","call_id":"c2","turn_id":"t1","command":["ls"],"cwd":"work","parsed_cmd":[],"source":"agent","exit_code":0,"duration":{"secs":0,"nanos":0},"status":"completed"}`),
	}
	home, path := writeCanonicalMigrationRollout(t, lines)
	if _, err := CanonicalizeRolloutWithStats(home, path); err != nil {
		t.Fatalf("CanonicalizeRolloutWithStats: %v", err)
	}
	want := canonicalMigrationHead + "\n" +
		`{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":1,"type":"event_msg","payload":{"type":"task_started","turn_id":"rollout-1","started_at":1735787046,"model_context_window":null,"collaboration_mode_kind":"default"}}` + "\n" +
		`{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":2,"type":"event_msg","payload":{"type":"item_completed","thread_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","turn_id":"rollout-1","item":{"type":"UserMessage","id":"item-1","content":[{"type":"text","text":"hello 0","text_elements":[]}]},"completed_at_ms":1735787046000}}` + "\n" +
		`{"timestamp":"2025-01-02T03:04:07.000Z","ordinal":3,"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hi 0"}]}}` + "\n" +
		`{"timestamp":"2025-01-02T03:04:08.000Z","ordinal":4,"type":"event_msg","payload":{"type":"item_completed","thread_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","turn_id":"t1","item":{"type":"CommandExecution","id":"c1","command":["bash","-lc","ls"],"cwd":"file:///C:/work","parsed_cmd":[],"source":"agent","status":"completed","aggregated_output":"out","exit_code":0,"duration":{"secs":1,"nanos":0}},"completed_at_ms":1735787048000}}` + "\n" +
		`{"timestamp":"2025-01-02T03:04:08.000Z","ordinal":5,"type":"event_msg","payload":{"type":"task_complete","turn_id":"rollout-1","last_agent_message":null,"completed_at":1735787048}}` + "\n"
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != want {
		t.Fatalf("migrated rollout mismatch\n got: %s\nwant: %s", got, want)
	}
}
