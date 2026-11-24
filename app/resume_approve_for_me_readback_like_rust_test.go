package app

import (
	"path/filepath"
	"testing"
	"time"

	"codex_go/rollout"
	"codex_go/sandbox"
	"codex_go/session"
)

// TestConfirmResumeApproveForMeSettingsReadBackLikeRust mirrors Rust #63589's
// server confirmation: the resumed conversation's metadata and newest owned
// settings snapshot must both show the preset, and a conversation that reports
// something else stops the resume before the initial prompt.
func TestConfirmResumeApproveForMeSettingsReadBackLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(filepath.Join(home, "sessions"))
	const threadID = "0198f100-0000-7000-8000-0000000000b1"
	record := &session.Record{ID: session.ThreadID(threadID)}
	record.Metadata.CWD = home
	if err := store.Create(record); err != nil {
		t.Fatalf("store.Create() error = %v", err)
	}
	seedResumeApproveForMeThread(t, home, threadID, home)

	policy := string(sandbox.ApprovalOnRequest)
	sandboxMode := string(sandbox.SandboxWorkspaceWrite)
	if _, err := store.UpdateMetadata(session.ThreadID(threadID), &session.MetadataPatch{
		ApprovalPolicy: &policy,
		SandboxPolicy:  &sandboxMode,
	}, true); err != nil {
		t.Fatalf("UpdateMetadata() error = %v", err)
	}
	path, found, err := rollout.FindRolloutPathByRolloutID(home, threadID)
	if err != nil || !found {
		t.Fatalf("rollout lookup = %q/%v/%v", path, found, err)
	}
	profile, ok := resumeApproveForMePermissionProfile(sandbox.SandboxWorkspaceWrite)
	if !ok {
		t.Fatal("workspace-write has no permission profile")
	}
	profileJSON, err := sandbox.RuntimePermissionProfileJSON(profile)
	if err != nil {
		t.Fatalf("RuntimePermissionProfileJSON() error = %v", err)
	}
	reviewer := "auto_review"
	roots := []string{home}
	settings := rollout.ThreadSettingsApplied{
		ApprovalPolicy:             policy,
		ApprovalsReviewer:          reviewer,
		RequestedApprovalsReviewer: &reviewer,
		PermissionProfile:          []byte(profileJSON),
		CWD:                        home,
		RuntimeWorkspaceRoots:      &roots,
	}
	recorder, err := rollout.Resume(path)
	if err != nil {
		t.Fatalf("rollout.Resume() error = %v", err)
	}
	if err := recorder.AppendThreadSettingsAppliedSnapshot(threadID, settings, time.Now().UTC()); err != nil {
		t.Fatalf("AppendThreadSettingsAppliedSnapshot() error = %v", err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if err := confirmResumeApproveForMeSettings(store, session.ThreadID(threadID), path, policy, sandboxMode, settings); err != nil {
		t.Fatalf("confirmResumeApproveForMeSettings() error = %v", err)
	}

	never := string(sandbox.ApprovalNever)
	if _, err := store.UpdateMetadata(session.ThreadID(threadID), &session.MetadataPatch{ApprovalPolicy: &never}, true); err != nil {
		t.Fatalf("UpdateMetadata(never) error = %v", err)
	}
	if err := confirmResumeApproveForMeSettings(store, session.ThreadID(threadID), path, policy, sandboxMode, settings); err == nil {
		t.Fatal("readback accepted a conversation that reports a different approval policy")
	}
}
