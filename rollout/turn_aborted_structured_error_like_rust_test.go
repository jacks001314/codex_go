package rollout

import (
	"encoding/json"
	"testing"

	"codex_go/session"
)

// TestTurnAbortedStructuredErrorRoundTripsLikeRust pins Rust #48796: strict
// Guardian circuit-breaker interruptions persist an ErrorEvent on turn_aborted
// and restore it into the saved interrupted turn.
func TestTurnAbortedStructuredErrorRoundTripsLikeRust(t *testing.T) {
	now := fixedTime()
	recorder, err := NewRecorder(&CreateParams{
		CodexHome: t.TempDir(), SessionID: "session-1", ThreadID: "thread-1",
		Source: "cli", ThreadSource: "user", Originator: "codex",
		CWD: "/repo", ModelProvider: "openai", HistoryMode: "legacy", MemoryMode: "disabled",
		Now: now,
	})
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	if err := recorder.AppendTurnStarted("turn-1", now); err != nil {
		t.Fatalf("AppendTurnStarted() error = %v", err)
	}
	wantMessage := "Guardian denied the action three times."
	if err := recorder.AppendTurnAbortedWithRootAndError("", "turn-1", "interrupted", &TurnAbortedError{
		Message: wantMessage, CodexErrorInfo: "tooManyDenials",
	}, now.Add(5e9), 5000); err != nil {
		t.Fatalf("AppendTurnAbortedWithRootAndError() error = %v", err)
	}
	path := recorder.Path()
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	lines, parseErrors, err := Load(path)
	if err != nil || parseErrors != 0 {
		t.Fatalf("Load() error/parseErrors = %v/%d", err, parseErrors)
	}
	var abortPayload map[string]any
	for _, line := range lines {
		if line.Type != "event_msg" {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal(line.Payload, &payload); err != nil {
			t.Fatalf("Unmarshal(payload) error = %v", err)
		}
		if payload["type"] == "turn_aborted" {
			abortPayload = payload
			break
		}
	}
	encoded, _ := json.Marshal(abortPayload)
	storedError, _ := abortPayload["error"].(map[string]any)
	if storedError["message"] != wantMessage || storedError["codex_error_info"] != "tooManyDenials" {
		t.Fatalf("turn_aborted payload = %s", encoded)
	}

	record, err := RecordFromPath(path, false)
	if err != nil {
		t.Fatalf("RecordFromPath() error = %v", err)
	}
	if len(record.Metadata.RolloutTurns) != 1 {
		t.Fatalf("rollout turns = %#v", record.Metadata.RolloutTurns)
	}
	snapshot := record.Metadata.RolloutTurns[0]
	if snapshot.ID != "turn-1" || snapshot.Status != "interrupted" || snapshot.ErrorMessage != wantMessage || snapshot.CodexErrorInfo != "tooManyDenials" {
		t.Fatalf("saved interrupted turn = %#v", snapshot)
	}

	replayed, err := NewRecorder(&CreateParams{
		CodexHome: t.TempDir(), SessionID: "session-2", ThreadID: "thread-2",
		Source: "cli", ThreadSource: "user", Originator: "codex",
		CWD: "/repo", ModelProvider: "openai", HistoryMode: "legacy", MemoryMode: "disabled",
		Now: now,
	})
	if err != nil {
		t.Fatalf("NewRecorder(replayed) error = %v", err)
	}
	if err := appendReplayedTurnTerminal(replayed, "turn-1", session.TurnSnapshot{
		ID: "turn-1", Status: "interrupted", ErrorMessage: wantMessage, CodexErrorInfo: "tooManyDenials",
	}, now); err != nil {
		t.Fatalf("appendReplayedTurnTerminal() error = %v", err)
	}
	replayedPath := replayed.Path()
	if err := replayed.Close(); err != nil {
		t.Fatalf("Close(replayed) error = %v", err)
	}
	replayedLines, _, err := Load(replayedPath)
	if err != nil {
		t.Fatalf("Load(replayed) error = %v", err)
	}
	for _, line := range replayedLines {
		if line.Type != "event_msg" {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal(line.Payload, &payload); err != nil {
			t.Fatalf("Unmarshal(replayed payload) error = %v", err)
		}
		if payload["type"] == "turn_aborted" && payload["reason"] != "interrupted" {
			t.Fatalf("replayed reason = %#v, want interrupted", payload["reason"])
		}
	}
}
