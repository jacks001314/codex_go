package rollout

import (
	"bytes"
	"codex_go/session"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRecorderCreateAppendLoad(t *testing.T) {
	home := t.TempDir()
	now := fixedTime()
	recorder, err := NewRecorder(&CreateParams{
		CodexHome:        home,
		SessionID:        "session-1",
		ThreadID:         "thread-1",
		ForkedFromID:     "thread-root",
		Source:           "cli",
		ThreadSource:     "user",
		Originator:       "codex_vscode",
		CWD:              "/repo",
		ModelProvider:    "openai",
		HistoryMode:      "legacy",
		MemoryMode:       "disabled",
		BaseInstructions: "base",
		AgentPath:        "/worker",
		DynamicTools: []json.RawMessage{
			json.RawMessage(`{"type":"function","name":"demo","description":"demo","inputSchema":{"type":"object"}}`),
		},
		SelectedCapabilityRoots: []json.RawMessage{
			json.RawMessage(`{"id":"cap-root","location":{"type":"environment","environmentId":"env-1","path":"/skills"}}`),
		},
		MultiAgentVersion: "v2",
		ContextWindow:     json.RawMessage(`{"window_number":1,"window_id":"window-1"}`),
		Now:               now,
	})
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	if err := recorder.AppendItem(Item{Type: "user_message", Data: map[string]any{"text": "hello"}}); err != nil {
		t.Fatalf("AppendItem() error = %v", err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	lines, parseErrors, err := Load(recorder.Path())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if parseErrors != 0 || len(lines) != 2 {
		t.Fatalf("Load() len/errors = %d/%d", len(lines), parseErrors)
	}
	if lines[0].Meta.ID != "thread-1" {
		t.Fatalf("meta = %#v", lines[0].Meta)
	}
	if lines[0].Meta.ForkedFromID != "thread-root" || lines[0].Meta.ThreadSource != "user" || lines[0].Meta.Originator != "codex_vscode" || lines[0].Meta.MemoryMode != "disabled" || lines[0].Meta.BaseInstructions != "base" {
		t.Fatalf("meta extended fields = %#v", lines[0].Meta)
	}
	if lines[0].Meta.AgentPath != "/worker" || len(lines[0].Meta.DynamicTools) != 1 || len(lines[0].Meta.SelectedCapabilityRoots) != 1 || lines[0].Meta.MultiAgentVersion != "v2" || len(lines[0].Meta.ContextWindow) == 0 {
		t.Fatalf("meta high fidelity fields = %#v", lines[0].Meta)
	}
	record, err := RecordFromPath(recorder.Path(), false)
	if err != nil {
		t.Fatalf("RecordFromPath() error = %v", err)
	}
	if record.Metadata.AgentPath != "/worker" || len(record.Metadata.DynamicTools) != 1 || len(record.Metadata.SelectedCapabilityRoots) != 1 || record.Metadata.MultiAgentVersion != "v2" || len(record.Metadata.ContextWindow) == 0 {
		t.Fatalf("record metadata high fidelity fields = %#v", record.Metadata)
	}
}

func TestLatestPersistedApprovalPolicyMatchesRustResumePrecedence(t *testing.T) {
	settings := func(policy string) Line {
		raw, _ := json.Marshal(map[string]any{
			"type":            "thread_settings_applied",
			"thread_settings": map[string]string{"approval_policy": policy},
		})
		return Line{Type: "event_msg", Payload: raw}
	}
	turnStarted := func(id string) Line {
		raw, _ := json.Marshal(map[string]any{"type": "turn_started", "turn_id": id})
		return Line{Type: "event_msg", Payload: raw}
	}
	turnContext := func(id, policy string) Line {
		raw, _ := json.Marshal(map[string]any{"turn_id": id, "approval_policy": policy})
		return Line{Type: "turn_context", TurnContext: raw}
	}
	tests := []struct {
		name  string
		lines []Line
		want  string
	}{
		{"later settings snapshot wins", []Line{turnContext("turn-1", "never"), settings("on-request")}, "on-request"},
		{"settings in same turn beats stale context", []Line{turnStarted("turn-1"), settings("never"), turnContext("turn-1", "on-request")}, "never"},
		{"newer turn context wins", []Line{settings("never"), turnStarted("turn-2"), turnContext("turn-2", "on-request")}, "on-request"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := LatestPersistedApprovalPolicy(test.lines)
			if !ok || got != test.want {
				t.Fatalf("LatestPersistedApprovalPolicy() = %q, %v; want %q, true", got, ok, test.want)
			}
		})
	}
}

func TestRecorderWritesRustRequiredSessionMetaFields(t *testing.T) {
	recorder, err := NewRecorder(&CreateParams{
		CodexHome: t.TempDir(),
		ThreadID:  "thread-rust-meta",
		Now:       fixedTime(),
	})
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	raw, err := os.ReadFile(recorder.Path())
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	var line struct {
		Payload map[string]json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(raw), &line); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if !rustRequiredSessionMetaStrings(line.Payload) {
		t.Fatalf("session_meta missing Rust-required strings: %s", raw)
	}
	var sessionID string
	if err := json.Unmarshal(line.Payload["session_id"], &sessionID); err != nil || sessionID != "thread-rust-meta" {
		t.Fatalf("session_id = %q, %v", sessionID, err)
	}
}

func TestEnsureRustCompatibleSessionMetaAppendsOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout-2026-08-06T15-00-00-thread-legacy.jsonl")
	legacy := `{"timestamp":"2026-08-06T15:00:00Z","type":"session_meta","payload":{"id":"thread-legacy","timestamp":"2026-08-06T15:00:00Z","cwd":"D:\\repo","source":"cli"}}` + "\n"
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	compatiblePath, err := EnsureRustCompatibleSessionMeta(path)
	if err != nil {
		t.Fatalf("EnsureRustCompatibleSessionMeta() error = %v", err)
	}
	if compatiblePath != path {
		t.Fatalf("compatible path = %q, want %q", compatiblePath, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if got := len(bytes.Split(bytes.TrimSpace(data), []byte{'\n'})); got != 2 {
		t.Fatalf("line count = %d, want 2: %s", got, data)
	}
	if ok, err := hasRustCompatibleSessionMeta(path); err != nil || !ok {
		t.Fatalf("hasRustCompatibleSessionMeta() = %v, %v", ok, err)
	}
	before := len(data)
	if _, err := EnsureRustCompatibleSessionMeta(path); err != nil {
		t.Fatalf("second EnsureRustCompatibleSessionMeta() error = %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("second ReadFile() error = %v", err)
	}
	if len(after) != before {
		t.Fatalf("second ensure changed rollout size from %d to %d", before, len(after))
	}
}

func TestSessionMetaBaseInstructionsAcceptsRustObjectAndLegacyString(t *testing.T) {
	for name, raw := range map[string]string{
		"rust object":   `{"id":"thread-1","timestamp":"2026-06-29T01:02:03Z","base_instructions":{"text":"rust base"}}`,
		"legacy string": `{"id":"thread-1","timestamp":"2026-06-29T01:02:03Z","base_instructions":"legacy base"}`,
		"provenance":    `{"id":"thread-1","timestamp":"2026-06-29T01:02:03Z","base_instructions":{"text":"model base","provenance":{"type":"model","model":"gpt-5.2"}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			var meta SessionMeta
			if err := json.Unmarshal([]byte(raw), &meta); err != nil {
				t.Fatalf("json.Unmarshal() error = %v", err)
			}
			want := "rust base"
			if name == "legacy string" {
				want = "legacy base"
			} else if name == "provenance" {
				want = "model base"
			}
			if meta.BaseInstructions != want {
				t.Fatalf("BaseInstructions = %q, want %q", meta.BaseInstructions, want)
			}
			if name == "provenance" {
				if meta.BaseInstructionsProvenance == nil || meta.BaseInstructionsProvenance.Type != "model" || meta.BaseInstructionsProvenance.Model != "gpt-5.2" {
					t.Fatalf("provenance = %#v", meta.BaseInstructionsProvenance)
				}
			}
		})
	}
}

func TestSessionMetaBaseInstructionsMarshalPreservesProvenance(t *testing.T) {
	raw, err := json.Marshal(SessionMeta{ID: "thread-1", Timestamp: "2026-06-29T01:02:03Z", BaseInstructions: "base", BaseInstructionsProvenance: &session.BaseInstructionsProvenance{Type: "model", Model: "gpt-5.2"}})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	base := decoded["base_instructions"].(map[string]any)
	provenance := base["provenance"].(map[string]any)
	if base["text"] != "base" || provenance["type"] != "model" || provenance["model"] != "gpt-5.2" {
		t.Fatalf("base_instructions = %#v", base)
	}
}

func TestSessionMetaBaseInstructionsMarshalUsesRustObject(t *testing.T) {
	raw, err := json.Marshal(SessionMeta{ID: "thread-1", Timestamp: "2026-06-29T01:02:03Z", BaseInstructions: "base"})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	base, ok := decoded["base_instructions"].(map[string]any)
	if !ok || base["text"] != "base" {
		t.Fatalf("base_instructions = %#v, want object with text", decoded["base_instructions"])
	}
}

func TestLoadAcceptsJSONLLinesBeyondScannerDefaultLimit(t *testing.T) {
	// A single rollout JSONL record can exceed bufio.Scanner's default 64KB
	// token limit (e.g. a large tool output embedded in a response item).
	// Loading such a rollout must not fail with "bufio.Scanner: token too
	// long", otherwise resuming the thread through the app-server fails.
	home := t.TempDir()
	path := filepath.Join(home, "rollout-long-line.jsonl")
	now := fixedTime()
	metaLine, err := json.Marshal(Line{
		Type: "session_meta",
		Meta: &SessionMeta{
			ID:        "thread-1",
			SessionID: "session-1",
			Timestamp: now.UTC().Format(time.RFC3339Nano),
			CWD:       "/repo",
		},
	})
	if err != nil {
		t.Fatalf("marshal meta line error = %v", err)
	}
	bigText := strings.Repeat("x", 256*1024)
	itemLine, err := json.Marshal(Line{
		Type:    "response_item",
		Payload: json.RawMessage(`{"text":` + strconv.Quote(bigText) + `}`),
	})
	if err != nil {
		t.Fatalf("marshal item line error = %v", err)
	}
	if len(itemLine) <= 64*1024 {
		t.Fatalf("test line is %d bytes, want > 64KB", len(itemLine))
	}
	data := append(metaLine, '\n')
	data = append(data, itemLine...)
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write rollout error = %v", err)
	}

	lines, parseErrors, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if parseErrors != 0 || len(lines) != 2 {
		t.Fatalf("Load() len/errors = %d/%d", len(lines), parseErrors)
	}
	var payload struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(lines[1].Payload, &payload); err != nil {
		t.Fatalf("unmarshal payload error = %v", err)
	}
	if payload.Text != bigText {
		t.Fatalf("payload text length = %d, want %d", len(payload.Text), len(bigText))
	}
	if _, err := RecordFromPath(path, false); err != nil {
		t.Fatalf("RecordFromPath() error = %v", err)
	}
}

func TestExistingRolloutPathPrefersPlainAndFallsBackToCompressed(t *testing.T) {
	home := t.TempDir()
	plain := filepath.Join(home, "rollout-2026-07-31T01-02-03-thread.jsonl")
	compressed := plain + ".zst"
	if _, found := ExistingRolloutPath(plain); found {
		t.Fatal("missing rollout unexpectedly resolved")
	}
	if err := os.WriteFile(compressed, []byte("compressed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, found := ExistingRolloutPath(plain); !found || got != compressed {
		t.Fatalf("compressed resolution = %q found=%v", got, found)
	}
	if err := os.WriteFile(plain, []byte("plain"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, found := ExistingRolloutPath(compressed); !found || got != plain {
		t.Fatalf("plain resolution = %q found=%v", got, found)
	}
}

func TestNewRecorderRequiresParams(t *testing.T) {
	if _, err := NewRecorder(nil); err == nil {
		t.Fatal("NewRecorder(nil) error = nil")
	}
}

func TestPathForThreadUsesRustNestedLocalDateLayout(t *testing.T) {
	home := t.TempDir()
	local := time.FixedZone("UTC+8", 8*60*60)
	now := time.Date(2026, 7, 31, 1, 2, 3, 0, local)
	want := filepath.Join(home, SessionsSubdir, "2026", "07", "31", "rollout-2026-07-31T01-02-03-thread-1.jsonl")
	if got := PathForThread(home, "thread-1", now); got != want {
		t.Fatalf("PathForThread() = %q, want %q", got, want)
	}
	recorder, err := NewRecorder(&CreateParams{CodexHome: home, ThreadID: "thread-1", Now: now})
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	defer recorder.Close()
	if recorder.Path() != want {
		t.Fatalf("recorder path = %q, want %q", recorder.Path(), want)
	}
	lines := mustLoadLines(t, recorder.Path())
	if got := lines[0].Meta.Timestamp; got != "2026-07-30T17:02:03Z" {
		t.Fatalf("session timestamp = %q, want UTC timestamp", got)
	}
}

func TestUnarchiveTouchesRestoredRolloutModTime(t *testing.T) {
	home := t.TempDir()
	archivedPath := filepath.Join(home, ArchivedSessionsSubdir, "rollout-2025-01-03T13-00-00-thread-1.jsonl")
	if err := os.MkdirAll(filepath.Dir(archivedPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	data := strings.Join([]string{
		`{"timestamp":"2025-01-03T13:00:00Z","type":"session_meta","payload":{"id":"thread-1","timestamp":"2025-01-03T13:00:00Z","cwd":"/repo","source":"cli","model_provider":"openai"}}`,
		`{"timestamp":"2025-01-03T13:00:05Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}}`,
		``,
	}, "\n")
	if err := os.WriteFile(archivedPath, []byte(data), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	old := time.Unix(1, 0).UTC()
	if err := os.Chtimes(archivedPath, old, old); err != nil {
		t.Fatalf("Chtimes() error = %v", err)
	}

	restoredPath, err := Unarchive(archivedPath, home)
	if err != nil {
		t.Fatalf("Unarchive() error = %v", err)
	}
	info, err := os.Stat(restoredPath)
	if err != nil {
		t.Fatalf("Stat(restored) error = %v", err)
	}
	if !info.ModTime().After(old) {
		t.Fatalf("restored mtime = %v, want after %v", info.ModTime(), old)
	}
	record, err := RecordFromPath(restoredPath, false)
	if err != nil {
		t.Fatalf("RecordFromPath() error = %v", err)
	}
	if !record.UpdatedAt.After(old) {
		t.Fatalf("record.UpdatedAt = %v, want after %v", record.UpdatedAt, old)
	}
	wantRecency := time.Date(2025, 1, 3, 13, 0, 5, 0, time.UTC)
	if !record.RecencyAt.Equal(wantRecency) {
		t.Fatalf("record.RecencyAt = %v, want %v", record.RecencyAt, wantRecency)
	}
}

func TestLoadRustStylePayloadLinesAndRecordFromPath(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, SessionsSubdir, "rollout-2026-06-29T01-02-03-thread-1.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	data := strings.Join([]string{
		`{"timestamp":"2026-06-29T01:02:03Z","type":"session_meta","payload":{"id":"thread-1","session_id":"session-1","forked_from_id":"thread-root","parent_thread_id":"thread-parent","timestamp":"2026-06-29T01:02:03Z","cwd":"/repo","source":"cli","thread_source":"user","originator":"codex_vscode","model_provider":"openai","history_mode":"legacy","memory_mode":"disabled","base_instructions":"base","cli_version":"0.0.0","extra":{"previous_response_id":"resp-prev","last_response_id":"resp-last"}}}`,
		`{"timestamp":"2026-06-29T01:02:03Z","type":"turn_context","payload":{"turn_id":"turn-1","cwd":"/repo/context","approval_policy":"never","sandbox_policy":"danger_full_access","model":"gpt-5","multi_agent_version":"v2","network":{"allowed_domains":["api.example.com"],"denied_domains":[]}}}`,
		`{"timestamp":"2026-06-29T01:02:03Z","type":"world_state","payload":{"full":true,"state":{"workspaces":{"/repo/context":{"has_changes":true}}}}}`,
		`{"timestamp":"2026-06-29T01:02:04Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}}`,
		``,
	}, "\n")
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	lines, parseErrors, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if parseErrors != 0 || len(lines) != 4 {
		t.Fatalf("Load() len/errors = %d/%d", len(lines), parseErrors)
	}
	if lines[0].Meta == nil || lines[0].Meta.ID != "thread-1" {
		t.Fatalf("meta line = %#v", lines[0])
	}
	if len(lines[1].TurnContext) == 0 || len(lines[2].WorldState) == 0 {
		t.Fatalf("context/world lines = %#v / %#v", lines[1], lines[2])
	}
	if lines[3].Type != "item" || len(lines[3].Item) == 0 {
		t.Fatalf("response item line = %#v", lines[3])
	}
	record, err := RecordFromPath(path, false)
	if err != nil {
		t.Fatalf("RecordFromPath() error = %v", err)
	}
	if record.ID != "thread-1" || record.SessionID != "session-1" || record.Preview != "hello" {
		t.Fatalf("record = %#v", record)
	}
	if record.ForkedFromID != "thread-root" || record.ParentThreadID != "thread-parent" {
		t.Fatalf("lineage = forked:%q parent:%q", record.ForkedFromID, record.ParentThreadID)
	}
	if record.Metadata.ThreadSource != "user" || record.Metadata.Originator != "codex_vscode" || record.Metadata.MemoryMode != "disabled" || record.Metadata.BaseInstructions != "base" {
		t.Fatalf("metadata = %#v", record.Metadata)
	}
	if record.Metadata.PreviousResponseID != "resp-prev" || record.Metadata.LastResponseID != "resp-last" {
		t.Fatalf("response ids = %q/%q", record.Metadata.PreviousResponseID, record.Metadata.LastResponseID)
	}
	if record.Metadata.CWD != "/repo/context" || record.Metadata.Model != "gpt-5" || record.Metadata.ApprovalPolicy != "never" || record.Metadata.SandboxPolicy != "danger_full_access" || record.Metadata.MultiAgentVersion != "v2" {
		t.Fatalf("turn context metadata = %#v", record.Metadata)
	}
	if len(record.Metadata.TurnContext) == 0 || len(record.Metadata.WorldState) == 0 {
		t.Fatalf("turn context/world state raw missing = %#v", record.Metadata)
	}
	if len(record.Items) != 1 || record.Items[0].Text != "hello" || record.Items[0].Role != "user" {
		t.Fatalf("items = %#v", record.Items)
	}
}

func TestRecordFromPathUsesLatestSessionMetaMetadata(t *testing.T) {
	home := t.TempDir()
	now := fixedTime()
	recorder, err := NewRecorder(&CreateParams{
		CodexHome:        home,
		SessionID:        "session-1",
		ThreadID:         "thread-1",
		Source:           "cli",
		CWD:              "/repo/old",
		ModelProvider:    "openai",
		Git:              map[string]string{"branch": "old"},
		Now:              now,
		HistoryMode:      "legacy",
		MemoryMode:       "disabled",
		BaseInstructions: "base",
	})
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	if err := recorder.AppendLine(Line{
		Type:      "session_meta",
		Timestamp: now.Add(time.Minute).Format(time.RFC3339Nano),
		Meta: &SessionMeta{
			ID:               "thread-1",
			SessionID:        "session-1",
			Timestamp:        now.Format(time.RFC3339),
			CWD:              "/repo/new",
			Source:           "cli",
			ModelProvider:    "openai",
			Git:              map[string]string{"branch": "new"},
			HistoryMode:      "legacy",
			MemoryMode:       "disabled",
			BaseInstructions: "base",
		},
	}); err != nil {
		t.Fatalf("AppendLine(session_meta) error = %v", err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	record, err := RecordFromPath(recorder.Path(), false)
	if err != nil {
		t.Fatalf("RecordFromPath() error = %v", err)
	}
	if !record.CreatedAt.Equal(now) {
		t.Fatalf("CreatedAt = %v, want %v", record.CreatedAt, now)
	}
	if record.Metadata.CWD != "/repo/new" || record.Metadata.Git["branch"] != "new" {
		t.Fatalf("metadata = %#v", record.Metadata)
	}
}

func TestRecordFromPathReplaysRustEventMsgTurns(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, SessionsSubdir, "rollout-2026-06-29T01-02-03-thread-1.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	data := strings.Join([]string{
		`{"timestamp":"2026-06-29T01:02:03Z","type":"session_meta","payload":{"id":"thread-1","timestamp":"2026-06-29T01:02:03Z"}}`,
		`{"timestamp":"2026-06-29T01:02:04Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1","started_at":1700000000,"model_context_window":128000}}`,
		`{"timestamp":"2026-06-29T01:02:05Z","type":"event_msg","payload":{"type":"user_message","client_id":"client-1","message":"hello event","images":["data:image/png;base64,a"],"image_details":["high"]}}`,
		`{"timestamp":"2026-06-29T01:02:06Z","type":"event_msg","payload":{"type":"agent_message","message":"answer event"}}`,
		`{"timestamp":"2026-06-29T01:02:07Z","type":"event_msg","payload":{"type":"turn_aborted","turn_id":"turn-1","reason":"interrupted","completed_at":1700000005,"duration_ms":5000}}`,
		``,
	}, "\n")
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	record, err := RecordFromPath(path, false)
	if err != nil {
		t.Fatalf("RecordFromPath() error = %v", err)
	}
	if len(record.Items) != 2 {
		t.Fatalf("items len = %d, want 2: %#v", len(record.Items), record.Items)
	}
	if record.Preview != "hello event" || record.Items[0].ID != "item-1" || record.Items[0].Text != "hello event" || record.Items[0].Metadata["turnId"] != "turn-1" {
		t.Fatalf("user item/preview = %#v / %q", record.Items[0], record.Preview)
	}
	if len(record.Items[0].Content) != 2 || record.Items[0].Content[1].ImageURL == "" || record.Items[0].Content[1].Detail == nil || *record.Items[0].Content[1].Detail != "high" {
		t.Fatalf("user content = %#v", record.Items[0].Content)
	}
	if record.Items[1].Type != "agent_message" || record.Items[1].Text != "answer event" || record.Items[1].Metadata["turnId"] != "turn-1" {
		t.Fatalf("agent item = %#v", record.Items[1])
	}
	if len(record.Metadata.RolloutTurns) != 1 {
		t.Fatalf("rollout turns = %#v", record.Metadata.RolloutTurns)
	}
	turn := record.Metadata.RolloutTurns[0]
	if turn.ID != "turn-1" || turn.Status != "interrupted" || turn.StartedAt == nil || *turn.StartedAt != 1700000000 || turn.CompletedAt == nil || *turn.CompletedAt != 1700000005 || turn.DurationMS == nil || *turn.DurationMS != 5000 {
		t.Fatalf("rollout turn = %#v", turn)
	}
}

func TestRecordFromPathReplaysAnonymousTurnAbortedBoundary(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, SessionsSubdir, "rollout-2026-06-29T01-02-03-thread-1.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	data := strings.Join([]string{
		`{"timestamp":"2026-06-29T01:02:03Z","type":"session_meta","payload":{"id":"thread-1","timestamp":"2026-06-29T01:02:03Z"}}`,
		`{"timestamp":"2026-06-29T01:02:04Z","type":"item","item":{"type":"message","role":"user","content":[{"type":"input_text","text":"<turn_aborted>\nThe user interrupted the previous turn on purpose.\n</turn_aborted>"}]}}`,
		`{"timestamp":"2026-06-29T01:02:05Z","type":"event_msg","payload":{"type":"turn_aborted","reason":"interrupted"}}`,
		``,
	}, "\n")
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	record, err := RecordFromPath(path, false)
	if err != nil {
		t.Fatalf("RecordFromPath() error = %v", err)
	}
	if len(record.Items) != 1 || !strings.Contains(record.Items[0].Text, "<turn_aborted>") {
		t.Fatalf("items = %#v", record.Items)
	}
	if len(record.Metadata.RolloutTurns) != 1 {
		t.Fatalf("rollout turns = %#v", record.Metadata.RolloutTurns)
	}
	turn := record.Metadata.RolloutTurns[0]
	if turn.ID != "turn-1" || turn.Status != "interrupted" || turn.CompletedAt != nil || turn.DurationMS != nil {
		t.Fatalf("rollout turn = %#v", turn)
	}
}

func TestRecordFromPathReplaysRustTokenCountMetadata(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, SessionsSubdir, "rollout-2026-06-29T01-02-03-thread-1.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	data := strings.Join([]string{
		`{"timestamp":"2026-06-29T01:02:03Z","type":"session_meta","payload":{"id":"thread-1","timestamp":"2026-06-29T01:02:03Z"}}`,
		`{"timestamp":"2026-06-29T01:02:04Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-usage"}}`,
		`{"timestamp":"2026-06-29T01:02:05Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":180,"cached_input_tokens":40,"output_tokens":50,"reasoning_output_tokens":15,"total_tokens":230},"last_token_usage":{"input_tokens":90,"cached_input_tokens":30,"output_tokens":40,"reasoning_output_tokens":12,"total_tokens":130},"model_context_window":200000},"rate_limits":{"primary":{"used_percent":12.5,"window_minutes":60},"secondary":{"used_percent":0.000000000001,"window_minutes":10080}}}}`,
		`{"timestamp":"2026-06-29T01:02:06Z","type":"event_msg","payload":{"type":"agent_message","message":"answer after fractional rate limits"}}`,
		`{"timestamp":"2026-06-29T01:02:07Z","type":"event_msg","payload":{"type":"turn_aborted","turn_id":"turn-usage","reason":"interrupted"}}`,
		``,
	}, "\n")
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	record, err := RecordFromPath(path, false)
	if err != nil {
		t.Fatalf("RecordFromPath() error = %v", err)
	}
	if record.Metadata.Extra["token_usage_turn_id"] != "turn-usage" {
		t.Fatalf("token usage turn id = %#v", record.Metadata.Extra["token_usage_turn_id"])
	}
	info, ok := record.Metadata.Extra["token_usage_info"].(map[string]any)
	if !ok {
		t.Fatalf("token usage info = %#v", record.Metadata.Extra["token_usage_info"])
	}
	last, ok := info["last_token_usage"].(map[string]any)
	if !ok || last["total_tokens"] != float64(130) {
		t.Fatalf("last token usage = %#v", info["last_token_usage"])
	}
	if len(record.Items) != 1 || record.Items[0].Text != "answer after fractional rate limits" {
		t.Fatalf("items after fractional rate limits = %#v", record.Items)
	}
}

func TestRecordFromPathReplaysRustErrorTurn(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, SessionsSubdir, "rollout-2026-06-29T01-02-03-thread-1.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	data := strings.Join([]string{
		`{"timestamp":"2026-06-29T01:02:03Z","type":"session_meta","payload":{"id":"thread-1","timestamp":"2026-06-29T01:02:03Z"}}`,
		`{"timestamp":"2026-06-29T01:02:04Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-failed","started_at":1700000000}}`,
		`{"timestamp":"2026-06-29T01:02:05Z","type":"event_msg","payload":{"type":"user_message","message":"please fail"}}`,
		`{"timestamp":"2026-06-29T01:02:06Z","type":"event_msg","payload":{"type":"error","message":"simulated failure"}}`,
		`{"timestamp":"2026-06-29T01:02:07Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-failed","completed_at":1700000006,"duration_ms":6000}}`,
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
	if turn.ID != "turn-failed" || turn.Status != "failed" || turn.ErrorMessage != "simulated failure" {
		t.Fatalf("rollout turn = %#v", turn)
	}
	if turn.StartedAt == nil || *turn.StartedAt != 1700000000 || turn.CompletedAt == nil || *turn.CompletedAt != 1700000006 || turn.DurationMS == nil || *turn.DurationMS != 6000 {
		t.Fatalf("rollout turn timing = %#v", turn)
	}
}

func TestRecordFromPathReplaysTurnCompleteEmbeddedError(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, SessionsSubdir, "rollout-2026-07-27T01-02-03-thread-1.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	data := strings.Join([]string{
		`{"timestamp":"2026-07-27T01:02:03Z","type":"session_meta","payload":{"id":"thread-1","timestamp":"2026-07-27T01:02:03Z"}}`,
		`{"timestamp":"2026-07-27T01:02:04Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-a","started_at":10}}`,
		`{"timestamp":"2026-07-27T01:02:05Z","type":"event_msg","payload":{"type":"user_message","message":"first"}}`,
		`{"timestamp":"2026-07-27T01:02:06Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-b","started_at":30}}`,
		`{"timestamp":"2026-07-27T01:02:07Z","type":"event_msg","payload":{"type":"user_message","message":"second"}}`,
		`{"timestamp":"2026-07-27T01:02:08Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-a","error":{"message":"Selected model is at capacity.","codex_error_info":"serverOverloaded"},"completed_at":20,"duration_ms":10000}}`,
		``,
	}, "\n")
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	record, err := RecordFromPath(path, false)
	if err != nil {
		t.Fatalf("RecordFromPath() error = %v", err)
	}
	if len(record.Metadata.RolloutTurns) != 2 {
		t.Fatalf("rollout turns = %#v", record.Metadata.RolloutTurns)
	}
	failed := record.Metadata.RolloutTurns[0]
	if failed.ID != "turn-a" || failed.Status != "failed" || failed.ErrorMessage != "Selected model is at capacity." || failed.CodexErrorInfo != "serverOverloaded" {
		t.Fatalf("failed rollout turn = %#v", failed)
	}
	if failed.CompletedAt == nil || *failed.CompletedAt != 20 || failed.DurationMS == nil || *failed.DurationMS != 10000 {
		t.Fatalf("failed rollout turn timing = %#v", failed)
	}
	active := record.Metadata.RolloutTurns[1]
	if active.ID != "turn-b" || active.Status != "inProgress" || active.ErrorMessage != "" {
		t.Fatalf("active rollout turn = %#v", active)
	}
}

func TestRecordFromPathReplaysRustItemCompletedThreadItems(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, SessionsSubdir, "rollout-2026-06-29T01-02-03-thread-1.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	data := strings.Join([]string{
		`{"timestamp":"2026-06-29T01:02:03Z","type":"session_meta","payload":{"id":"thread-1","timestamp":"2026-06-29T01:02:03Z"}}`,
		`{"timestamp":"2026-06-29T01:02:04Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1","started_at":1700000000}}`,
		`{"timestamp":"2026-06-29T01:02:05Z","type":"event_msg","payload":{"type":"item_completed","thread_id":"thread-1","turn_id":"turn-1","started_at_ms":900,"completed_at_ms":1000,"item":{"type":"Sleep","id":"sleep-1","duration_ms":1000}}}`,
		`{"timestamp":"2026-06-29T01:02:06Z","type":"event_msg","payload":{"type":"item_completed","thread_id":"thread-1","turn_id":"turn-1","startedAtMs":1500,"completedAtMs":2000,"item":{"type":"CommandExecution","id":"exec-1","process_id":"pid-1","command":["echo","hello world"],"cwd":"/tmp","parsed_cmd":[{"type":"unknown","cmd":"echo hello world"}],"source":"agent","status":"completed","stdout":"hello world\n","stderr":"","aggregated_output":"hello world\n","exit_code":0,"duration":{"secs":0,"nanos":12000000},"formatted_output":"hello world\n"}}}`,
		`{"timestamp":"2026-06-29T01:02:07Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-1","completed_at":1700000006,"duration_ms":6000}}`,
		``,
	}, "\n")
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	record, err := RecordFromPath(path, false)
	if err != nil {
		t.Fatalf("RecordFromPath() error = %v", err)
	}
	if len(record.Items) != 2 {
		t.Fatalf("items len = %d, want 2: %#v", len(record.Items), record.Items)
	}
	sleep := record.Items[0]
	if sleep.Type != "sleep" || sleep.ID != "sleep-1" || sleep.Metadata["turnId"] != "turn-1" || sleep.CreatedAt.UnixMilli() != 1000 {
		t.Fatalf("sleep item = %#v", sleep)
	}
	if duration, ok := rolloutInt64FromAny(sleep.Data["duration_ms"]); !ok || duration != 1000 {
		t.Fatalf("sleep duration = %#v", sleep.Data)
	}
	if started, ok := rolloutInt64FromAny(sleep.Data["startedAtMs"]); !ok || started != 900 {
		t.Fatalf("sleep startedAtMs = %#v", sleep.Data)
	}
	if completed, ok := rolloutInt64FromAny(sleep.Data["completed_at_ms"]); !ok || completed != 1000 {
		t.Fatalf("sleep completed_at_ms = %#v", sleep.Data)
	}
	command := record.Items[1]
	if command.Type != "commandExecution" || command.ID != "exec-1" || command.Metadata["turnId"] != "turn-1" || command.CreatedAt.UnixMilli() != 2000 {
		t.Fatalf("command item = %#v", command)
	}
	if command.Data["command"] != "echo 'hello world'" || command.Data["cwd"] != "/tmp" || command.Text != "hello world\n" {
		t.Fatalf("command data/text = %#v / %q", command.Data, command.Text)
	}
	if duration, ok := rolloutInt64FromAny(command.Data["durationMs"]); !ok || duration != 12 {
		t.Fatalf("command duration = %#v", command.Data)
	}
	if started, ok := rolloutInt64FromAny(command.Data["started_at_ms"]); !ok || started != 1500 {
		t.Fatalf("command started_at_ms = %#v", command.Data)
	}
	if completed, ok := rolloutInt64FromAny(command.Data["completedAtMs"]); !ok || completed != 2000 {
		t.Fatalf("command completedAtMs = %#v", command.Data)
	}
	actions, ok := command.Data["commandActions"].([]map[string]any)
	if !ok || len(actions) != 1 || actions[0]["type"] != "unknown" || actions[0]["command"] != "echo hello world" {
		t.Fatalf("command actions = %#v", command.Data["commandActions"])
	}
	if len(record.Metadata.RolloutTurns) != 1 || record.Metadata.RolloutTurns[0].ID != "turn-1" || record.Metadata.RolloutTurns[0].Status != "completed" {
		t.Fatalf("rollout turns = %#v", record.Metadata.RolloutTurns)
	}
}

func TestRolloutReadCommandActionsPreserveExecutorPathsLikeRust(t *testing.T) {
	for _, test := range []struct {
		cwd  string
		path string
		want string
	}{
		{cwd: "file:///home/alice/repo", path: "src/main.rs", want: "/home/alice/repo/src/main.rs"},
		{cwd: "file:///C:/Users/Alice%20Smith/repo", path: `src\main.rs`, want: `C:\Users\Alice Smith\repo\src\main.rs`},
		{cwd: "file:///C:/Users/Alice%20Smith/repo", path: `C:src\main.rs`, want: `C:\Users\Alice Smith\repo\src\main.rs`},
		{cwd: "file://server/share/repo", path: `src\main.rs`, want: `\\server\share\repo\src\main.rs`},
	} {
		got := rolloutCommandActionFromMap(map[string]any{
			"type": "read",
			"cmd":  "cat " + test.path,
			"name": "main.rs",
			"path": test.path,
		}, test.cwd)
		if got["path"] != test.want {
			t.Fatalf("rollout read path for %q + %q = %#v, want %q", test.cwd, test.path, got["path"], test.want)
		}
	}
}

func TestRecordFromPathNormalizesRustComplexThreadItems(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, SessionsSubdir, "rollout-2026-06-29T01-02-03-thread-1.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	data := strings.Join([]string{
		`{"timestamp":"2026-06-29T01:02:03Z","type":"session_meta","payload":{"id":"thread-1","timestamp":"2026-06-29T01:02:03Z"}}`,
		`{"timestamp":"2026-06-29T01:02:04Z","type":"response_item","payload":{"type":"mcpToolCall","id":"mcp-1","server":"memory","tool":"create_entities","status":"completed","arguments":{"entity":"Ada"},"result":{"content":[{"type":"text","text":"ok"}]}}}`,
		`{"timestamp":"2026-06-29T01:02:05Z","type":"response_item","payload":{"type":"dynamicToolCall","id":"dyn-1","namespace":"codex_app","tool":"demo_tool","status":"completed","arguments":{"city":"Paris"},"contentItems":[{"type":"inputText","text":"dynamic-ok"}],"success":true}}`,
		`{"timestamp":"2026-06-29T01:02:06Z","type":"response_item","payload":{"type":"fileChange","id":"patch-1","status":"completed","changes":{"src/old.txt":{"type":"update","movePath":"src/new.txt","unifiedDiff":"@@\n-old\n+new\n"}}}}`,
		`{"timestamp":"2026-06-29T01:02:07Z","type":"response_item","payload":{"type":"commandExecution","id":"cmd-1","command":"echo hi","cwd":"/repo","status":"completed","aggregatedOutput":"hi\n","exitCode":0,"durationMs":12}}`,
		`{"timestamp":"2026-06-29T01:02:08Z","type":"response_item","payload":{"type":"custom_tool_call","id":"patch-call","call_id":"call-patch","name":"apply_patch","input":"*** Begin Patch\n*** End Patch\n"}}`,
		``,
	}, "\n")
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	record, err := RecordFromPath(path, false)
	if err != nil {
		t.Fatalf("RecordFromPath() error = %v", err)
	}
	if len(record.Items) != 5 {
		t.Fatalf("items len = %d, want 5: %#v", len(record.Items), record.Items)
	}
	mcp := record.Items[0]
	if mcp.Data["mcpToolCall"] != true || mcp.Data["server"] != "memory" || mcp.Data["tool"] != "create_entities" {
		t.Fatalf("mcp item = %#v", mcp)
	}
	dynamic := record.Items[1]
	if dynamic.Data["dynamicToolCall"] != true || dynamic.Data["namespace"] != "codex_app" || dynamic.Data["tool"] != "demo_tool" {
		t.Fatalf("dynamic item = %#v", dynamic)
	}
	fileChange := record.Items[2]
	if fileChange.Data["fileChange"] != true || len(fileChange.Data["changes"].([]map[string]any)) != 1 {
		t.Fatalf("file change item = %#v", fileChange)
	}
	change := fileChange.Data["changes"].([]map[string]any)[0]
	kind := change["kind"].(map[string]any)
	if change["path"] != "src/old.txt" || change["diff"] != "@@\n-old\n+new\n" || kind["type"] != "update" || kind["move_path"] != "src/new.txt" {
		t.Fatalf("normalized file change = %#v", change)
	}
	command := record.Items[3]
	if command.Data["command"] != "echo hi" || command.Data["cwd"] != "/repo" || command.Text != "hi\n" {
		t.Fatalf("command item = %#v", command)
	}
	patchCall := record.Items[4]
	if patchCall.Data["fileChange"] != true || patchCall.Name != "apply_patch" {
		t.Fatalf("patch call item = %#v", patchCall)
	}
}

func TestRecordFromPathAppliesThreadRolledBackEvent(t *testing.T) {
	home := t.TempDir()
	now := fixedTime()
	recorder, err := NewRecorder(&CreateParams{
		CodexHome: home,
		SessionID: "session-1",
		ThreadID:  "thread-1",
		Now:       now,
	})
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	for _, item := range []Item{
		{ID: "u1", Type: "message", Role: "user", Text: "one", Metadata: map[string]any{"turnId": "turn-1"}},
		{ID: "a1", Type: "message", Role: "assistant", Text: "answer one", Metadata: map[string]any{"turnId": "turn-1"}},
		{ID: "u2", Type: "message", Role: "user", Text: "two", Metadata: map[string]any{"turnId": "turn-2"}},
		{ID: "a2", Type: "message", Role: "assistant", Text: "answer two", Metadata: map[string]any{"turnId": "turn-2"}},
	} {
		if err := recorder.AppendItem(item); err != nil {
			t.Fatalf("AppendItem() error = %v", err)
		}
	}
	if err := recorder.AppendThreadRolledBack(1, now.Add(time.Second)); err != nil {
		t.Fatalf("AppendThreadRolledBack() error = %v", err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	lines, parseErrors, err := Load(recorder.Path())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if parseErrors != 0 || len(lines) != 6 {
		t.Fatalf("Load() len/errors = %d/%d", len(lines), parseErrors)
	}
	if lines[len(lines)-1].ThreadRolledBack == nil || lines[len(lines)-1].ThreadRolledBack.NumTurns != 1 {
		t.Fatalf("rollback line = %#v", lines[len(lines)-1])
	}
	inputItems := InputItemsFromLines(lines, true)
	if len(inputItems) != 2 {
		t.Fatalf("input items len = %d, want 2", len(inputItems))
	}

	record, err := RecordFromPath(recorder.Path(), false)
	if err != nil {
		t.Fatalf("RecordFromPath() error = %v", err)
	}
	if len(record.Items) != 2 || record.Items[0].Text != "one" || record.Items[1].Text != "answer one" {
		t.Fatalf("record items = %#v", record.Items)
	}
}

func TestRecordFromPathAppliesCompactedReplacementHistory(t *testing.T) {
	home := t.TempDir()
	now := fixedTime()
	recorder, err := NewRecorder(&CreateParams{
		CodexHome: home,
		SessionID: "session-1",
		ThreadID:  "thread-1",
		Now:       now,
	})
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	if err := recorder.AppendItem(Item{ID: "u1", Type: "message", Role: "user", Text: "old"}); err != nil {
		t.Fatalf("AppendItem() error = %v", err)
	}
	replacement := []Item{
		{ID: "u2", Type: "message", Role: "user", Text: "kept", Metadata: map[string]any{"turnId": "turn-2"}},
		{ID: "summary", Type: "message", Role: "user", Text: "summary", Metadata: map[string]any{"turnId": "turn-summary"}},
	}
	if err := recorder.AppendCompacted("summary", replacement, now.Add(time.Second)); err != nil {
		t.Fatalf("AppendCompacted() error = %v", err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	record, err := RecordFromPath(recorder.Path(), false)
	if err != nil {
		t.Fatalf("RecordFromPath() error = %v", err)
	}
	if len(record.Items) != 2 || record.Items[0].Text != "kept" || record.Items[1].Text != "summary" {
		t.Fatalf("record items = %#v", record.Items)
	}
	inputItems := InputItemsFromLines(mustLoadLines(t, recorder.Path()), true)
	if len(inputItems) != 2 {
		t.Fatalf("input items len = %d, want 2", len(inputItems))
	}
}

func TestLineFromItemCarriesRustStyleFields(t *testing.T) {
	line, err := LineFromItem(&Item{
		ID:         "item-1",
		Type:       "message",
		Role:       "user",
		Text:       "hello",
		ResponseID: "resp-1",
		Metadata:   map[string]any{"turnId": "turn-1"},
	}, fixedTime())
	if err != nil {
		t.Fatalf("LineFromItem() error = %v", err)
	}
	if line.Type != "response_item" || line.ItemID != "item-1" || line.Role != "user" || line.TurnID != "turn-1" || line.ResponseID != "resp-1" {
		t.Fatalf("line = %#v", line)
	}
	if line.Timestamp == "" || len(line.Item) == 0 {
		t.Fatalf("line missing timestamp/raw: %#v", line)
	}
}

func TestLegacySessionItemMarshalUsesRustResponseItemWireShape(t *testing.T) {
	line, err := LineFromItem(&Item{
		ID:      "item-1",
		Type:    "message",
		Role:    "user",
		Content: []ContentPart{{Type: "input_text", Text: "hello"}},
	}, fixedTime())
	if err != nil {
		t.Fatalf("LineFromItem() error = %v", err)
	}
	raw, err := json.Marshal(line)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	var wire map[string]any
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if wire["type"] != "response_item" || wire["payload"] == nil || wire["item"] != nil {
		t.Fatalf("wire line = %#v", wire)
	}
}

func TestAppendSessionItemsWritesCanonicalRustResponsePayload(t *testing.T) {
	home := t.TempDir()
	now := fixedTime()
	recorder, err := NewRecorder(&CreateParams{CodexHome: home, ThreadID: "thread-canonical", Now: now})
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	if err := AppendSessionItems(recorder, []session.Item{{
		ID: "user-1", Type: "message", Role: "user", Text: "hello",
		Content:  []session.ContentPart{{Type: "input_text", Text: "hello"}},
		Metadata: map[string]any{"turnId": "turn-1", "resumed": true},
	}}, now); err != nil {
		t.Fatalf("AppendSessionItems() error = %v", err)
	}
	path := recorder.Path()
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	lines, parseErrors, err := Load(path)
	if err != nil || parseErrors != 0 || len(lines) != 3 {
		t.Fatalf("Load() lines/errors/err = %d/%d/%v", len(lines), parseErrors, err)
	}
	if lines[1].Type != "event_msg" || !strings.Contains(string(lines[1].Payload), `"type":"user_message"`) || !strings.Contains(string(lines[1].Payload), `"message":"hello"`) {
		t.Fatalf("event mirror = %#v", lines[1])
	}
	var payload map[string]any
	if err := json.Unmarshal(lines[2].Item, &payload); err != nil {
		t.Fatalf("payload decode error = %v", err)
	}
	if payload["type"] != "message" || payload["role"] != "user" || payload["id"] != "user-1" {
		t.Fatalf("payload = %#v", payload)
	}
	if _, ok := payload["text"]; ok {
		t.Fatalf("Go-only text field leaked into Rust response item: %#v", payload)
	}
	if _, ok := payload["metadata"]; ok {
		t.Fatalf("Go-only metadata field leaked into Rust response item: %#v", payload)
	}
	passthrough, _ := payload["internal_chat_message_metadata_passthrough"].(map[string]any)
	if passthrough["turn_id"] != "turn-1" {
		t.Fatalf("turn passthrough = %#v", passthrough)
	}
}

func TestEnsureRustCompatibleSessionHistoryBackfillsLegacyEventsOnce(t *testing.T) {
	home := t.TempDir()
	now := fixedTime()
	recorder, err := NewRecorder(&CreateParams{CodexHome: home, ThreadID: "thread-desktop-history", SessionID: "thread-desktop-history", CWD: t.TempDir(), Originator: "codex_cli_rs", CLIVersion: "test", Now: now})
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	legacyItems := []session.Item{
		{ID: "user-1", Type: "message", Role: "user", Text: "hello Desktop", Content: []session.ContentPart{{Type: "input_text", Text: "hello Desktop"}}, CreatedAt: now, Metadata: map[string]any{"turnId": "turn-1"}},
		{ID: "assistant-1", Type: "agent_message", Role: "assistant", Text: "visible answer", Content: []session.ContentPart{{Type: "output_text", Text: "visible answer"}}, CreatedAt: now.Add(time.Second), Metadata: map[string]any{"turnId": "turn-1", "phase": "final_answer"}},
	}
	for i := range legacyItems {
		if err := recorder.AppendItem(*ItemFromSessionItem(&legacyItems[i])); err != nil {
			t.Fatalf("AppendItem(%d) error = %v", i, err)
		}
	}
	path := recorder.Path()
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	record := &session.Record{
		ID:        "thread-desktop-history",
		SessionID: "thread-desktop-history",
		CreatedAt: now,
		UpdatedAt: now.Add(time.Second),
		RecencyAt: now.Add(time.Second),
		Metadata:  session.Metadata{RolloutTurns: []session.TurnSnapshot{{ID: "turn-1", Status: "completed"}}},
		Items:     legacyItems,
	}
	changed, err := EnsureRustCompatibleSessionHistory(path, record)
	if err != nil || !changed {
		t.Fatalf("EnsureRustCompatibleSessionHistory() changed/err = %v/%v", changed, err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	for _, expected := range []string{`"type":"task_started"`, `"type":"user_message"`, `"type":"agent_message"`, `"type":"task_complete"`} {
		if !bytes.Contains(first, []byte(expected)) {
			t.Fatalf("backfilled rollout missing %s: %s", expected, first)
		}
	}
	changed, err = EnsureRustCompatibleSessionHistory(path, record)
	if err != nil || changed {
		t.Fatalf("second EnsureRustCompatibleSessionHistory() changed/err = %v/%v", changed, err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("second ReadFile() error = %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("second compatibility pass changed the rollout")
	}
}

func TestRecordFromPathNormalizesLegacyGoResponseWrapperForResume(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, SessionsSubdir, "rollout-2026-06-29T01-02-03-thread-legacy-wrapper.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	data := strings.Join([]string{
		`{"timestamp":"2026-06-29T01:02:03Z","type":"session_meta","payload":{"id":"thread-legacy-wrapper","timestamp":"2026-06-29T01:02:03Z"}}`,
		`{"timestamp":"2026-06-29T01:02:04Z","type":"response_item","payload":{"id":"user-1","type":"message","role":"user","text":"hello","content":[{"type":"input_text","text":"hello"}],"metadata":{"resumed":true,"turnId":"turn-1"}}}`,
		``,
	}, "\n")
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	record, err := RecordFromPath(path, false)
	if err != nil {
		t.Fatalf("RecordFromPath() error = %v", err)
	}
	inputs := session.InputItemsFromRecord(record, &session.HistoryBuildOptions{IncludeToolOutputs: true})
	if len(inputs) != 1 {
		t.Fatalf("inputs = %#v", inputs)
	}
	message := inputs[0].(map[string]any)
	if message["type"] != "message" || message["role"] != "user" {
		t.Fatalf("message = %#v", message)
	}
	if _, ok := message["text"]; ok {
		t.Fatalf("legacy wrapper text leaked into model input: %#v", message)
	}
	if _, ok := message["metadata"]; ok {
		t.Fatalf("legacy wrapper metadata leaked into model input: %#v", message)
	}
}

func TestRecordFromPathDeduplicatesRustEventAndResponseMessageMirrors(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, SessionsSubdir, "rollout-2026-06-29T01-02-03-thread-mirrors.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	data := strings.Join([]string{
		`{"timestamp":"2026-06-29T01:02:03Z","type":"session_meta","payload":{"id":"thread-mirrors","timestamp":"2026-06-29T01:02:03Z"}}`,
		`{"timestamp":"2026-06-29T01:02:04Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}`,
		`{"timestamp":"2026-06-29T01:02:05Z","type":"event_msg","payload":{"type":"user_message","message":"hello"}}`,
		`{"timestamp":"2026-06-29T01:02:06Z","type":"response_item","payload":{"id":"user-1","type":"message","role":"user","content":[{"type":"input_text","text":"hello"}],"internal_chat_message_metadata_passthrough":{"turn_id":"turn-1"}}}`,
		`{"timestamp":"2026-06-29T01:02:07Z","type":"event_msg","payload":{"type":"agent_message","message":"answer"}}`,
		`{"timestamp":"2026-06-29T01:02:08Z","type":"response_item","payload":{"id":"assistant-1","type":"message","role":"assistant","content":[{"type":"output_text","text":"answer"}],"internal_chat_message_metadata_passthrough":{"turn_id":"turn-1"}}}`,
		`{"timestamp":"2026-06-29T01:02:09Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-1"}}`,
		``,
	}, "\n")
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	record, err := RecordFromPath(path, false)
	if err != nil {
		t.Fatalf("RecordFromPath() error = %v", err)
	}
	if len(record.Items) != 2 || record.Items[0].Text != "hello" || record.Items[1].Text != "answer" {
		t.Fatalf("deduplicated items = %#v", record.Items)
	}
	if record.Items[0].ID != "user-1" || record.Items[1].ID != "assistant-1" {
		t.Fatalf("canonical response items did not replace event mirrors: %#v", record.Items)
	}
}

func TestRecordFromPathRestoresTurnAbortedMarkerKindFromCanonicalMessage(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, SessionsSubdir, "rollout-2026-06-29T01-02-03-thread-aborted-marker.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	data := strings.Join([]string{
		`{"timestamp":"2026-06-29T01:02:03Z","type":"session_meta","payload":{"id":"thread-aborted-marker","timestamp":"2026-06-29T01:02:03Z"}}`,
		`{"timestamp":"2026-06-29T01:02:04Z","type":"response_item","payload":{"id":"abort-1","type":"message","role":"user","content":[{"type":"input_text","text":"<turn_aborted>\nInterrupted.\n</turn_aborted>"}],"internal_chat_message_metadata_passthrough":{"turn_id":"turn-1"}}}`,
		``,
	}, "\n")
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	record, err := RecordFromPath(path, false)
	if err != nil {
		t.Fatalf("RecordFromPath() error = %v", err)
	}
	if len(record.Items) != 1 || record.Items[0].Metadata["kind"] != "turn_aborted" || record.Items[0].Metadata["turnId"] != "turn-1" {
		t.Fatalf("turn-aborted marker = %#v", record.Items)
	}
}

func TestInputItemsFromLinesReconstructsResponsesInput(t *testing.T) {
	raw, _ := json.Marshal(Item{ID: "u1", Type: "message", Role: "user", Text: "hello"})
	lines := []Line{{Type: "item", Item: raw, ItemID: "u1", Role: "user"}}

	items := InputItemsFromLines(lines, true)

	if len(items) != 1 {
		t.Fatalf("items len = %d, want 1", len(items))
	}
	got := items[0].(map[string]any)
	if got["type"] != "message" || got["role"] != "user" {
		t.Fatalf("item = %#v", got)
	}
}

func TestInputItemsFromItemsRemovesOnlyTopLevelPassthroughMetadata(t *testing.T) {
	raw := json.RawMessage(`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello","internal_chat_message_metadata_passthrough":{"nested":true}}],"internal_chat_message_metadata_passthrough":{"turn_id":"turn-secret"}}`)
	items := InputItemsFromItems([]Item{{Raw: raw}}, true)
	if len(items) != 1 {
		t.Fatalf("items = %#v", items)
	}
	item := items[0].(map[string]any)
	if _, ok := item["internal_chat_message_metadata_passthrough"]; ok {
		t.Fatalf("top-level passthrough metadata leaked: %#v", item)
	}
	content := item["content"].([]any)
	nested := content[0].(map[string]any)
	if _, ok := nested["internal_chat_message_metadata_passthrough"]; !ok {
		t.Fatalf("nested passthrough metadata was removed: %#v", nested)
	}
}

func TestResumeAppendsExistingRollout(t *testing.T) {
	home := t.TempDir()
	recorder, err := NewRecorder(&CreateParams{CodexHome: home, ThreadID: "thread-1", Now: fixedTime()})
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	path := recorder.Path()
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	resumed, err := Resume(path)
	if err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	if err := resumed.AppendItem(Item{Type: "user_message", Data: map[string]any{"text": "again"}}); err != nil {
		t.Fatalf("AppendItem() error = %v", err)
	}
	if err := resumed.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	lines, _, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(lines) != 2 {
		t.Fatalf("Load() len = %d, want 2", len(lines))
	}
}

func TestLoadCountsParseErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.jsonl")
	if err := os.WriteFile(path, []byte("{bad\n{\"type\":\"item\"}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	lines, parseErrors, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(lines) != 1 || parseErrors != 1 {
		t.Fatalf("Load() lines/errors = %d/%d, want 1/1", len(lines), parseErrors)
	}
}

func TestBuildThreadItem(t *testing.T) {
	path := writeRollout(t, t.TempDir(), "thread-1", fixedTime(), "hello")
	item, ok := BuildThreadItem(path, false)
	if !ok {
		t.Fatalf("BuildThreadItem() ok = false")
	}
	if item.ThreadID != "thread-1" || item.FirstUserMessage != "hello" || item.Preview != "hello" {
		t.Fatalf("BuildThreadItem() = %#v", item)
	}
}

func TestBuildThreadItemReadsRustEventMsgPreview(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, SessionsSubdir, "rollout-2026-06-29T01-02-03-thread-1.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	data := strings.Join([]string{
		`{"timestamp":"2026-06-29T01:02:03Z","type":"session_meta","payload":{"id":"thread-1","timestamp":"2026-06-29T01:02:03Z","source":"cli","model_provider":"openai","cwd":"/repo"}}`,
		`{"timestamp":"2026-06-29T01:02:04Z","type":"event_msg","payload":{"type":"user_message","message":"event preview target"}}`,
		``,
	}, "\n")
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	item, ok := BuildThreadItem(path, false)
	if !ok {
		t.Fatalf("BuildThreadItem() ok = false")
	}
	if item.FirstUserMessage != "event preview target" || item.Preview != "event preview target" {
		t.Fatalf("BuildThreadItem() = %#v", item)
	}
	page, err := ListThreads(home, ListOptions{Search: "preview target"})
	if err != nil {
		t.Fatalf("ListThreads() error = %v", err)
	}
	if got := threadIDs(page.Items); !reflect.DeepEqual(got, []string{"thread-1"}) {
		t.Fatalf("ListThreads(search) = %v, want thread-1", got)
	}
}

func TestThreadIDFromPath(t *testing.T) {
	path := writeRollout(t, t.TempDir(), "thread-1", fixedTime(), "hello")
	threadID, err := ThreadIDFromPath(path)
	if err != nil {
		t.Fatalf("ThreadIDFromPath() error = %v", err)
	}
	if threadID != "thread-1" {
		t.Fatalf("ThreadIDFromPath() = %q, want thread-1", threadID)
	}
}

func TestThreadIDFromFilename(t *testing.T) {
	tests := []struct {
		name string
		want string
		ok   bool
	}{
		{name: "rollout-2026-06-29T01-02-03-thread-1.jsonl", want: "thread-1", ok: true},
		{name: "rollout-2026-06-29T01-02-03-019f11d6-44c2-7101-bc29-4b31c5d1342b.jsonl", want: "019f11d6-44c2-7101-bc29-4b31c5d1342b", ok: true},
		{name: "rollout-2026-06-29T01-02-03-thread-1.jsonl.zst", want: "thread-1", ok: true},
		{name: "rollout-2026-06-29T01-02-03.jsonl", ok: false},
		{name: "other-2026-06-29T01-02-03-thread-1.jsonl", ok: false},
		{name: "rollout-not-a-timestamp-thread-1.jsonl", ok: false},
		{name: "rollout-2026-06-29T01-02-03-.jsonl", ok: false},
	}
	for _, tt := range tests {
		got, ok := ThreadIDFromFilename(tt.name)
		if got != tt.want || ok != tt.ok {
			t.Fatalf("ThreadIDFromFilename(%q) = %q/%v, want %q/%v", tt.name, got, ok, tt.want, tt.ok)
		}
	}
}

func TestListThreadsFiltersSortsAndPages(t *testing.T) {
	home := t.TempDir()
	writeRollout(t, home, "thread-1", fixedTime(), "alpha")
	writeRollout(t, home, "thread-2", fixedTime().Add(time.Minute), "target beta")
	writeRollout(t, home, "thread-3", fixedTime().Add(2*time.Minute), "target gamma")
	page, err := ListThreads(home, ListOptions{
		PageSize:       1,
		SortKey:        SortCreatedAt,
		SortDirection:  SortAsc,
		Search:         "target",
		ModelProviders: []string{"openai"},
		CWDFilters:     []string{"/repo"},
		AllowedSources: []string{"cli"},
	})
	if err != nil {
		t.Fatalf("ListThreads() error = %v", err)
	}
	if got := threadIDs(page.Items); !reflect.DeepEqual(got, []string{"thread-2"}) {
		t.Fatalf("ListThreads(page1) = %v, want thread-2", got)
	}
	if page.NextCursor == "" {
		t.Fatalf("NextCursor empty, want cursor")
	}
	next, err := ListThreads(home, ListOptions{
		Cursor:        page.NextCursor,
		SortKey:       SortCreatedAt,
		SortDirection: SortAsc,
		Search:        "target",
	})
	if err != nil {
		t.Fatalf("ListThreads(page2) error = %v", err)
	}
	if got := threadIDs(next.Items); !reflect.DeepEqual(got, []string{"thread-3"}) {
		t.Fatalf("ListThreads(page2) = %v, want thread-3", got)
	}
}

func TestArchiveMovesRollout(t *testing.T) {
	home := t.TempDir()
	path := writeRollout(t, home, "thread-1", fixedTime(), "hello")
	archived, err := Archive(path, home)
	if err != nil {
		t.Fatalf("Archive() error = %v", err)
	}
	if filepath.Dir(archived) != filepath.Join(home, ArchivedSessionsSubdir) {
		t.Fatalf("Archive() path = %q", archived)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("source stat = %v, want not exist", err)
	}
	page, err := ListThreads(home, ListOptions{Archived: true})
	if err != nil {
		t.Fatalf("ListThreads(archived) error = %v", err)
	}
	if got := threadIDs(page.Items); !reflect.DeepEqual(got, []string{"thread-1"}) {
		t.Fatalf("archived threads = %v, want thread-1", got)
	}
}

func TestUnarchiveDeleteAndFindThreadPath(t *testing.T) {
	home := t.TempDir()
	path := writeRollout(t, home, "thread-1", fixedTime(), "hello")
	archived, err := Archive(path, home)
	if err != nil {
		t.Fatalf("Archive() error = %v", err)
	}
	found, err := FindThreadPath(home, "thread-1", true)
	if err != nil {
		t.Fatalf("FindThreadPath(archived) error = %v", err)
	}
	if found != archived {
		t.Fatalf("FindThreadPath = %q, want %q", found, archived)
	}
	active, err := Unarchive(archived, home)
	if err != nil {
		t.Fatalf("Unarchive() error = %v", err)
	}
	if filepath.Dir(active) != filepath.Join(home, SessionsSubdir, "2026", "06", "29") {
		t.Fatalf("Unarchive() path = %q", active)
	}
	if err := Delete(active); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := os.Stat(active); !os.IsNotExist(err) {
		t.Fatalf("deleted rollout still exists: %v", err)
	}
}

func TestListThreadsStillReadsLegacyFlatRollout(t *testing.T) {
	home := t.TempDir()
	flatPath := filepath.Join(home, SessionsSubdir, "rollout-2026-06-29T01-02-03-flat-thread.jsonl")
	if err := os.MkdirAll(filepath.Dir(flatPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	contents := "{\"timestamp\":\"2026-06-29T01:02:03Z\",\"type\":\"session_meta\",\"payload\":{\"id\":\"flat-thread\",\"timestamp\":\"2026-06-29T01:02:03Z\",\"cwd\":\"/repo\",\"source\":\"cli\",\"model_provider\":\"openai\"}}\n"
	if err := os.WriteFile(flatPath, []byte(contents), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	page, err := ListThreads(home, ListOptions{})
	if err != nil {
		t.Fatalf("ListThreads() error = %v", err)
	}
	if got := threadIDs(page.Items); !reflect.DeepEqual(got, []string{"flat-thread"}) {
		t.Fatalf("legacy flat threads = %v, want flat-thread", got)
	}
}

func TestParseTimestampFromFilename(t *testing.T) {
	got, ok := ParseTimestampFromFilename("rollout-2026-06-29T01-02-03-thread.jsonl")
	if !ok || !got.Equal(time.Date(2026, 6, 29, 1, 2, 3, 0, time.UTC)) {
		t.Fatalf("ParseTimestampFromFilename() = %s/%v", got, ok)
	}
}

func writeRollout(t *testing.T, home string, threadID string, now time.Time, message string) string {
	t.Helper()
	recorder, err := NewRecorder(&CreateParams{
		CodexHome:     home,
		ThreadID:      threadID,
		Source:        "cli",
		CWD:           "/repo",
		ModelProvider: "openai",
		HistoryMode:   "legacy",
		Now:           now,
	})
	if err != nil {
		t.Fatalf("NewRecorder(%s) error = %v", threadID, err)
	}
	item := Item{Type: "user_message", Data: map[string]any{"text": message}}
	if err := recorder.AppendItem(item); err != nil {
		t.Fatalf("AppendItem() error = %v", err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	return recorder.Path()
}

func fixedTime() time.Time {
	return time.Date(2026, 6, 29, 1, 2, 3, 0, time.UTC)
}

func mustLoadLines(t *testing.T, path string) []Line {
	t.Helper()
	lines, parseErrors, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if parseErrors != 0 {
		t.Fatalf("Load() parse errors = %d", parseErrors)
	}
	return lines
}

func threadIDs(items []ThreadItem) []string {
	out := make([]string, len(items))
	for i := range items {
		out[i] = items[i].ThreadID
	}
	return out
}

func TestUserTextFromRaw(t *testing.T) {
	raw, _ := json.Marshal(Item{Type: "user_message", Data: map[string]any{"text": "hello"}})
	if got := userTextFromRaw(raw); got != "hello" {
		t.Fatalf("userTextFromRaw() = %q, want hello", got)
	}
}
