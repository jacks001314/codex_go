package recordreplay

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"codex_go/rollout"
)

// goldenPath is the frozen structural digest of the Rust oss-story.jsonl
// recording at the pinned baseline. Upstream recording drift breaks the
// digest comparison and is reported as a static-layer drift.
const goldenPath = "testdata/oss-story-digest.json"

func TestParseRustStoryRecording(t *testing.T) {
	path, err := DefaultStoryRecordingPath()
	if err != nil {
		t.Fatalf("resolve Rust recording: %v", err)
	}
	events, err := Parse(path)
	if err != nil {
		t.Fatalf("Parse(%s): %v", path, err)
	}
	if len(events) != 8041 {
		t.Fatalf("recording event count = %d, want 8041 (pinned baseline)", len(events))
	}

	// Structural invariants of the recording's observable surface.
	var sessionStart, sessionEnd, logLines int
	appVariants := map[string]int{}
	msgTypes := map[string]int{}
	keyEvents := 0
	for _, ev := range events {
		switch ev.Kind {
		case "session_start":
			sessionStart++
		case "session_end":
			sessionEnd++
		case "app_event":
			appVariants[ev.Variant]++
		case "codex_event":
			msgTypes[ev.MsgType]++
		case "key_event":
			keyEvents++
		case "log_line":
			logLines++
		}
	}
	if sessionStart != 1 || sessionEnd != 1 {
		t.Fatalf("session markers = start:%d end:%d, want 1/1", sessionStart, sessionEnd)
	}
	if keyEvents != 104 {
		t.Fatalf("key events = %d, want 104", keyEvents)
	}
	if logLines != 3 {
		t.Fatalf("log lines = %d, want 3", logLines)
	}
	wantMsgTypes := map[string]int{
		"session_configured":                1,
		"task_started":                      3,
		"agent_reasoning_raw_content_delta": 130,
		"agent_message_delta":               1379,
		"agent_message":                     3,
		"task_complete":                     3,
		"shutdown_complete":                 1,
	}
	for kind, want := range wantMsgTypes {
		if msgTypes[kind] != want {
			t.Fatalf("codex msg type %s count = %d, want %d", kind, msgTypes[kind], want)
		}
	}
	for kind, count := range msgTypes {
		if _, ok := wantMsgTypes[kind]; !ok {
			t.Fatalf("unexpected codex msg type %s (%d)", kind, count)
		}
	}

	// Task (turn) lifecycle surface.
	lifecycles := TaskLifecycles(events)
	if len(lifecycles) != 3 {
		t.Fatalf("task lifecycles = %d, want 3", len(lifecycles))
	}
	wantIDs := []string{"1", "3", "5"}
	for i, lc := range lifecycles {
		if lc.ID != wantIDs[i] {
			t.Fatalf("task lifecycle %d id = %q, want %q", i, lc.ID, wantIDs[i])
		}
		if !lc.HasComplete {
			t.Fatalf("task %s missing task_complete", lc.ID)
		}
	}

	// Digest must match the frozen golden (static contract).
	digest := Summarize(events)
	got, err := json.MarshalIndent(digest, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent(digest): %v", err)
	}
	if os.Getenv("RECORDREPLAY_UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(goldenPath, append(got, '\n'), 0o644); err != nil {
			t.Fatalf("WriteFile(golden): %v", err)
		}
		t.Logf("wrote %s", goldenPath)
		return
	}
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("ReadFile(golden): %v (set RECORDREPLAY_UPDATE_GOLDEN=1 to generate)", err)
	}
	if !bytes.Equal(bytes.TrimSpace(got), bytes.TrimSpace(want)) {
		t.Fatalf("digest drifted from frozen golden:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestReplayRustStoryRecordingThroughGoRecorder(t *testing.T) {
	events, err := storyRecordingEvents(t)
	if err != nil {
		t.Fatalf("load recording: %v", err)
	}
	result, err := Replay(events, t.TempDir())
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	// The persisted structure must reproduce the recording's task sequence
	// (event sequence + item states).
	if err := ValidateRecordingReplayCrossCheck(events, result); err != nil {
		t.Fatalf("replay cross-check: %v", err)
	}
	if len(result.TaskStarts) != 3 || len(result.TaskCompletes) != 3 || len(result.ItemIDs) != 3 {
		t.Fatalf("persisted lines = starts:%d completes:%d items:%d, want 3/3/3",
			len(result.TaskStarts), len(result.TaskCompletes), len(result.ItemIDs))
	}

	// Recoverability: resume the same rollout and continue appending without
	// corrupting the frozen structure.
	resumed, err := rollout.Resume(result.Path)
	if err != nil {
		t.Fatalf("Resume(%s): %v", result.Path, err)
	}
	if err := resumed.AppendTurnStarted("9", nowForTest()); err != nil {
		t.Fatalf("resumed AppendTurnStarted: %v", err)
	}
	if err := resumed.Close(); err != nil {
		t.Fatalf("resumed Close: %v", err)
	}
	lines, _, err := rollout.Load(result.Path)
	if err != nil {
		t.Fatalf("Load after resume: %v", err)
	}
	found := false
	for _, line := range lines {
		if line.Type != "event_msg" || len(line.Payload) == 0 {
			continue
		}
		var payload struct {
			Type   string `json:"type"`
			TurnID string `json:"turn_id"`
		}
		if err := json.Unmarshal(line.Payload, &payload); err != nil {
			continue
		}
		if payload.Type == "task_started" && payload.TurnID == "9" {
			found = true
		}
	}
	if !found {
		t.Fatalf("resumed task_started(9) not persisted after Resume")
	}
}

func storyRecordingEvents(t *testing.T) ([]Event, error) {
	t.Helper()
	path, err := DefaultStoryRecordingPath()
	if err != nil {
		return nil, err
	}
	return Parse(path)
}

func nowForTest() time.Time {
	return time.Unix(1700000000, 0).UTC()
}
