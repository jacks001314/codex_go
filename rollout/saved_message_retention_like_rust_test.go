package rollout

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"codex_go/retainedctx"
)

// Rust #62828: the recorder policy is recovered from the latest owned settings
// snapshot, or from a compaction checkpoint when replay starts there.
func TestSavedMessageRetentionRecoversOwnedPolicyLikeRust(t *testing.T) {
	recorder, err := NewRecorder(&CreateParams{
		Path:     filepath.Join(t.TempDir(), "rollout.jsonl"),
		ThreadID: "thread-owner",
		CWD:      "/w",
	})
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	path := recorder.Path()
	recordBytes := 5000
	now := time.Now()
	if err := recorder.AppendThreadSettingsAppliedSnapshot("thread-owner", ThreadSettingsApplied{
		ApprovalPolicy:    "never",
		CWD:               "/repo",
		GuardianRetention: &retainedctx.GuardianRetentionPolicy{AssistantRecordBytes: &recordBytes},
	}, now); err != nil {
		t.Fatalf("AppendThreadSettingsAppliedSnapshot() error = %v", err)
	}
	if err := recorder.AppendThreadSettingsAppliedSnapshot("thread-other", ThreadSettingsApplied{
		ApprovalPolicy: "never", CWD: "/other",
	}, now); err != nil {
		t.Fatalf("AppendThreadSettingsAppliedSnapshot(other) error = %v", err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	lines, _, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	retention := SavedMessageRetention("thread-owner", lines)
	if retention == nil || retention.ThreadID != "thread-owner" || retention.AssistantRecordBytes == nil ||
		*retention.AssistantRecordBytes != recordBytes {
		t.Fatalf("SavedMessageRetention() = %#v", retention)
	}
	if other := SavedMessageRetention("thread-unknown", lines); other != nil {
		t.Fatalf("unknown thread retention = %#v", other)
	}

	// A compaction checkpoint supplies the policy when the settings snapshot is
	// not part of the replay window.
	checkpointLimit := 7777
	checkpoint := (&retainedctx.RetainedContext{}).WithRetention(&retainedctx.MessageRetention{
		ThreadID:                "thread-owner",
		GuardianRetentionPolicy: retainedctx.GuardianRetentionPolicy{AssistantRecordBytes: &checkpointLimit},
	})
	compactRecorder, err := NewRecorder(&CreateParams{
		Path:     filepath.Join(t.TempDir(), "compacted.jsonl"),
		ThreadID: "thread-owner",
		CWD:      "/w",
	})
	if err != nil {
		t.Fatalf("NewRecorder(compacted) error = %v", err)
	}
	compactedPath := compactRecorder.Path()
	if err := compactRecorder.AppendCompactedWithCheckpoint("summary", nil, checkpoint, nil, nil, "", nil, nil, now); err != nil {
		t.Fatalf("AppendCompactedWithCheckpoint() error = %v", err)
	}
	if err := compactRecorder.Close(); err != nil {
		t.Fatalf("Close(compacted) error = %v", err)
	}
	compactedLines, _, err := Load(compactedPath)
	if err != nil {
		t.Fatalf("Load(compacted) error = %v", err)
	}
	retention = SavedMessageRetention("thread-owner", compactedLines)
	if retention == nil || retention.AssistantRecordBytes == nil || *retention.AssistantRecordBytes != checkpointLimit {
		t.Fatalf("checkpoint retention = %#v, want %d", retention, checkpointLimit)
	}
	raw, _ := json.Marshal(retention)
	if string(raw) != `{"thread_id":"thread-owner","assistant_record_bytes":7777}` {
		t.Fatalf("retention wire = %s", raw)
	}
}
