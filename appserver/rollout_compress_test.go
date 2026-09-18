package appserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"codex_go/rollout"
	"codex_go/session"
	"codex_go/turn"
)

// TestRuntimeRouterRolloutCompressSchedulesBackgroundPass mirrors Rust's
// `rollout_compress_runs_after_startup_with_compression_disabled` (#46020):
// `rollout/compress` immediately acknowledges the trigger and the background
// pass compresses cold local rollouts.
func TestRuntimeRouterRolloutCompressSchedulesBackgroundPass(t *testing.T) {
	home := t.TempDir()
	sessions := filepath.Join(home, rollout.SessionsSubdir)
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	plainPath := filepath.Join(sessions, "rollout-2025-01-01T00-00-00-compress.jsonl")
	writeRolloutCompressTestRollout(t, plainPath, "compress")
	cold := time.Now().Add(-8 * 24 * time.Hour)
	if err := os.Chtimes(plainPath, cold, cold); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(session.NewStore(sessions)),
		Turns:        turn.NewTurnService(),
		Agent:        newRecordingRuntimeAgent("ok"),
		ThreadStatus: NewThreadStatusManager(),
	})

	response := router.Handle(&Request{JSONRPC: "2.0", ID: IntID(1), Method: MethodRolloutCompress})
	if response.Error != nil {
		t.Fatalf("rollout/compress error: %+v", response.Error)
	}
	if _, ok := response.Result.(*RolloutCompressResponse); !ok {
		t.Fatalf("rollout/compress result = %#v, want RolloutCompressResponse", response.Result)
	}

	compressed := plainPath + ".zst"
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(compressed); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("background pass did not compress %s", plainPath)
}

// TestRolloutCompressRequiresExperimentalCapability mirrors Rust's
// `rollout_compress_requires_experimental_capability`: the request is gated on
// the experimental API capability like every other experimental method.
func TestRolloutCompressRequiresExperimentalCapability(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(session.NewStore(t.TempDir())),
		Turns:        turn.NewTurnService(),
		Agent:        newRecordingRuntimeAgent("ok"),
		ThreadStatus: NewThreadStatusManager(),
	})
	router.rememberConnectionExperimentalAPI("conn-1", nil)
	if reason := experimentalAPIReasonForRequest(&Request{Method: MethodRolloutCompress}); reason != string(MethodRolloutCompress) {
		t.Fatalf("experimental reason = %q, want %q", reason, MethodRolloutCompress)
	}
	if !experimentalAPIMethod(MethodRolloutCompress) {
		t.Fatal("MethodRolloutCompress should be an experimental method")
	}
	err := router.rejectExperimentalAPIDisabled(&Request{Method: MethodRolloutCompress, ConnectionID: "conn-1"})
	if err == nil || err.Error() != "rollout/compress requires experimentalApi capability" {
		t.Fatalf("rejectExperimentalAPIDisabled() = %v", err)
	}
}

func writeRolloutCompressTestRollout(t *testing.T, path string, threadID string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	now := "2025-01-01T00:00:00Z"
	meta := map[string]any{
		"id": threadID, "session_id": "s", "timestamp": now, "cwd": "/w",
		"originator": "o", "model": "m", "cli_version": "v",
	}
	data, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("Marshal(meta): %v", err)
	}
	line, err := json.Marshal(map[string]any{
		"type": "session_meta", "timestamp": now, "payload": json.RawMessage(data),
	})
	if err != nil {
		t.Fatalf("Marshal(line): %v", err)
	}
	if err := os.WriteFile(path, append(line, '\n'), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}
