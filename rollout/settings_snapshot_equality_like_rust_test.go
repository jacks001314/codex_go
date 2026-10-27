package rollout

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

// Rust #56250: resume compares the effective snapshot with the latest owned
// checkpoint, so normalization must match what AppendThreadSettingsAppliedSnapshot
// persists and collaboration mode must survive the decode.
func TestLatestPersistedOwnedThreadSettingsAppliedSupportsCheckpointComparisonLikeRust(t *testing.T) {
	recorder, err := NewRecorder(&CreateParams{
		Path:     filepath.Join(t.TempDir(), "rollout.jsonl"),
		ThreadID: "thread-owner",
		CWD:      "/w",
	})
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	path := recorder.Path()
	roots := []string{"/repo", "/extra"}
	settings := ThreadSettingsApplied{
		ApprovalPolicy:          "  on-request ",
		ApprovalsReviewer:       "user",
		CWD:                     "/repo",
		CollaborationMode:       json.RawMessage(`{"mode":"plan"}`),
		RuntimeWorkspaceRoots:   &roots,
		ActivePermissionProfile: json.RawMessage(`{"id":"profile"}`),
	}
	now := time.Now()
	if err := recorder.AppendThreadSettingsAppliedSnapshot("thread-owner", settings, now); err != nil {
		t.Fatalf("AppendThreadSettingsAppliedSnapshot() error = %v", err)
	}
	if err := recorder.AppendThreadSettingsAppliedSnapshot("thread-other", ThreadSettingsApplied{ApprovalPolicy: "never", CWD: "/other"}, now); err != nil {
		t.Fatalf("AppendThreadSettingsAppliedSnapshot(other) error = %v", err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	lines, _, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	persisted, ok := LatestPersistedOwnedThreadSettingsApplied("thread-owner", lines)
	if !ok {
		t.Fatal("LatestPersistedOwnedThreadSettingsApplied() found no owned snapshot")
	}
	if persisted.CollaborationMode == nil {
		t.Fatalf("persisted collaboration mode = %s", persisted.CollaborationMode)
	}
	if !ThreadSettingsAppliedEqual(persisted, settings) {
		t.Fatalf("persisted snapshot = %#v, want an equal snapshot", persisted)
	}
	if ThreadSettingsAppliedEqual(persisted, ThreadSettingsApplied{ApprovalPolicy: "never", CWD: "/repo", CollaborationMode: json.RawMessage(`{"mode":"plan"}`), RuntimeWorkspaceRoots: &roots, ActivePermissionProfile: json.RawMessage(`{"id":"profile"}`)}) {
		t.Fatal("different approval policy reported equal")
	}
	if ThreadSettingsAppliedEqual(ThreadSettingsApplied{RuntimeWorkspaceRoots: &[]string{}}, ThreadSettingsApplied{}) {
		t.Fatal("explicit empty roots reported equal to an omitted selection")
	}
}
