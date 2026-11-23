package rollout

import (
	"path/filepath"
	"testing"
	"time"

	"codex_go/retainedctx"
)

// Rust #62827: the resolved Guardian recorder policy rides the thread-owned
// settings snapshot, survives decode, and participates in checkpoint equality.
// Fixed-mode and older snapshots omit it.
func TestThreadSettingsSnapshotPersistsGuardianRetentionLikeRust(t *testing.T) {
	recorder, err := NewRecorder(&CreateParams{
		Path:     filepath.Join(t.TempDir(), "rollout.jsonl"),
		ThreadID: "thread-retention",
		CWD:      "/w",
	})
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	path := recorder.Path()
	recordBytes := 4000
	settings := ThreadSettingsApplied{
		ApprovalPolicy:    "never",
		CWD:               "/repo",
		GuardianRetention: &retainedctx.GuardianRetentionPolicy{AssistantRecordBytes: &recordBytes},
	}
	now := time.Now()
	if err := recorder.AppendThreadSettingsAppliedSnapshot("thread-retention", settings, now); err != nil {
		t.Fatalf("AppendThreadSettingsAppliedSnapshot() error = %v", err)
	}
	if err := recorder.AppendThreadSettingsAppliedSnapshot("thread-plain", ThreadSettingsApplied{ApprovalPolicy: "never", CWD: "/repo"}, now); err != nil {
		t.Fatalf("AppendThreadSettingsAppliedSnapshot(plain) error = %v", err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	lines, _, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	snapshot, ok := LatestPersistedOwnedThreadSettings("thread-retention", lines)
	if !ok {
		t.Fatal("LatestPersistedOwnedThreadSettings() found no owned snapshot")
	}
	if snapshot.GuardianRetention == nil || snapshot.GuardianRetention.AssistantRecordBytes == nil ||
		*snapshot.GuardianRetention.AssistantRecordBytes != recordBytes {
		t.Fatalf("restored guardian retention = %#v, want assistant_record_bytes %d", snapshot.GuardianRetention, recordBytes)
	}
	if !ThreadSettingsAppliedEqual(settings, settings) {
		t.Fatal("identical snapshots reported unequal")
	}
	otherBytes := 12
	other := ThreadSettingsApplied{
		ApprovalPolicy:    "never",
		CWD:               "/repo",
		GuardianRetention: &retainedctx.GuardianRetentionPolicy{AssistantRecordBytes: &otherBytes},
	}
	if ThreadSettingsAppliedEqual(settings, other) {
		t.Fatal("different guardian retention reported equal")
	}
	if ThreadSettingsAppliedEqual(settings, ThreadSettingsApplied{ApprovalPolicy: "never", CWD: "/repo"}) {
		t.Fatal("retained policy reported equal to a snapshot without one")
	}
	plain, ok := LatestPersistedOwnedThreadSettings("thread-plain", lines)
	if !ok || plain.GuardianRetention != nil {
		t.Fatalf("fixed-mode snapshot retention = %#v, want nil", plain.GuardianRetention)
	}
}
