package app

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/rollout"
	"codex_go/sandbox"
)

// seedResumeApproveForMeThread writes a Rust-shaped rollout for a local
// conversation so the CLI resume path can load it without a Go snapshot.
func seedResumeApproveForMeThread(t *testing.T, home, threadID, cwd string) {
	t.Helper()
	now := time.Now().UTC()
	recorder, err := rollout.NewRecorder(&rollout.CreateParams{
		CodexHome: home, ThreadID: threadID, Source: "cli", CWD: cwd,
		ModelProvider: "openai", HistoryMode: "paginated", Now: now,
	})
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	payload, _ := json.Marshal(map[string]any{"type": "user_message", "message": "hello"})
	if err := recorder.AppendLine(rollout.Line{
		Type:      "event_msg",
		Timestamp: now.Add(time.Second).Format(time.RFC3339Nano),
		Payload:   payload,
	}); err != nil {
		t.Fatalf("AppendLine() error = %v", err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

// TestResumeApproveForMeAppliesInitialResumePresetLikeRust covers Rust #63589's
// embedded resume half: the preset is resolved for the selected conversation
// and checkpointed as that thread's settings (approval on-request, the auto
// reviewer and workspace-write) instead of entering the launch configuration.
func TestResumeApproveForMeAppliesInitialResumePresetLikeRust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	t.Setenv("TERM", "xterm-256color")
	const threadID = "0198f100-0000-7000-8000-0000000000a1"
	seedResumeApproveForMeThread(t, home, threadID, home)

	var stdout, stderr bytes.Buffer
	if err := Run(context.Background(), []string{"resume", threadID, "--approve-for-me", "--cd", home}, newTerminalReader(""), terminalOut(&stdout), &stderr); err != nil {
		t.Fatalf("resume --approve-for-me error = %v (stderr %q)", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"action": "resumed"`) {
		t.Fatalf("resume output = %q", stdout.String())
	}
	path, found, err := rollout.FindRolloutPathByRolloutID(home, threadID)
	if err != nil || !found {
		t.Fatalf("rollout lookup = %q/%v/%v", path, found, err)
	}
	lines, _, err := rollout.Load(path)
	if err != nil {
		t.Fatalf("rollout.Load() error = %v", err)
	}
	settings, ok := rollout.LatestPersistedResumeSettings(threadID, lines)
	if !ok {
		t.Fatal("resume did not checkpoint thread settings")
	}
	if settings.ApprovalPolicy != string(sandbox.ApprovalOnRequest) {
		t.Fatalf("approval policy = %q, want on-request", settings.ApprovalPolicy)
	}
	if settings.ApprovalsReviewer != "auto_review" {
		t.Fatalf("approvals reviewer = %q, want auto_review", settings.ApprovalsReviewer)
	}
	profile, err := sandbox.ParseRuntimePermissionProfileJSON(string(settings.UnnamedPermissionProfile))
	if err != nil {
		t.Fatalf("ParseRuntimePermissionProfileJSON() error = %v (%s)", err, settings.UnnamedPermissionProfile)
	}
	if profile.SandboxPolicy == nil || profile.SandboxPolicy.Kind != sandbox.SandboxWorkspaceWrite {
		t.Fatalf("sandbox policy = %+v, want workspace-write", profile.SandboxPolicy)
	}
}

// TestResumeApproveForMeRejectsOtherWorkspaceLikeRust mirrors
// resume_permissions.rs initial_resume_update: a conversation saved for another
// workspace cannot take the launch preset.
func TestResumeApproveForMeRejectsOtherWorkspaceLikeRust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	t.Setenv("TERM", "xterm-256color")
	const threadID = "0198f100-0000-7000-8000-0000000000a2"
	seedResumeApproveForMeThread(t, home, threadID, t.TempDir())

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"resume", threadID, "--approve-for-me", "--cd", home}, newTerminalReader(""), terminalOut(&stdout), &stderr)
	if err == nil || !strings.Contains(err.Error(), resumeApproveForMeWorkspaceMessage) {
		t.Fatalf("resume error = %v, want %q", err, resumeApproveForMeWorkspaceMessage)
	}
}

// TestResumeApproveForMeRequiresSelectionLikeRust mirrors the picker half: the
// preset needs a selected conversation, and the picker has not chosen one.
func TestResumeApproveForMeRequiresSelectionLikeRust(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("TERM", "xterm-256color")
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"resume", "--approve-for-me"}, newTerminalReader(""), terminalOut(&stdout), &stderr)
	if err == nil || !strings.Contains(err.Error(), resumeApproveForMeNoSelectionMessage) {
		t.Fatalf("resume error = %v, want %q", err, resumeApproveForMeNoSelectionMessage)
	}
}

// TestResumeApproveForMeRejectsRemoteLikeRust mirrors lib.rs: a remote task
// keeps the permissions saved on its server, so the preset is refused before
// any request is sent.
func TestResumeApproveForMeRejectsRemoteLikeRust(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("TERM", "xterm-256color")
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"resume", "0198f100-0000-7000-8000-0000000000a3", "--approve-for-me", "--remote", "ws://127.0.0.1:1"}, newTerminalReader(""), terminalOut(&stdout), &stderr)
	if err == nil || !strings.Contains(err.Error(), remoteResumePermissionOverrideMessage) {
		t.Fatalf("resume error = %v, want %q", err, remoteResumePermissionOverrideMessage)
	}
}

// TestResumeApproveForMeResolvesManagedRequirementsLikeRust covers Rust
// #63589's managed-requirement half: the preset resolves under the resumed
// conversation's requirements, so a managed allow-list replaces the preset's
// on-request / auto-review / workspace-write values with the allowed ones.
func TestResumeApproveForMeResolvesManagedRequirementsLikeRust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	t.Setenv("TERM", "xterm-256color")
	const threadID = "0198f100-0000-7000-8000-0000000000b1"
	seedResumeApproveForMeThread(t, home, threadID, home)
	requirements := strings.Join([]string{
		"allowed_approval_policies = [\"never\"]",
		"allowed_approvals_reviewers = [\"user\"]",
		"allowed_sandbox_modes = [\"read-only\"]",
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(home, "requirements.toml"), []byte(requirements), 0o600); err != nil {
		t.Fatalf("WriteFile(requirements.toml) error = %v", err)
	}

	var stdout, stderr bytes.Buffer
	if err := Run(context.Background(), []string{"resume", threadID, "--approve-for-me", "--cd", home}, newTerminalReader(""), terminalOut(&stdout), &stderr); err != nil {
		t.Fatalf("resume --approve-for-me error = %v (stderr %q)", err, stderr.String())
	}
	path, found, err := rollout.FindRolloutPathByRolloutID(home, threadID)
	if err != nil || !found {
		t.Fatalf("rollout lookup = %q/%v/%v", path, found, err)
	}
	lines, _, err := rollout.Load(path)
	if err != nil {
		t.Fatalf("rollout.Load() error = %v", err)
	}
	settings, ok := rollout.LatestPersistedResumeSettings(threadID, lines)
	if !ok {
		t.Fatal("resume did not checkpoint thread settings")
	}
	if settings.ApprovalPolicy != string(sandbox.ApprovalNever) {
		t.Fatalf("approval policy = %q, want never (managed default)", settings.ApprovalPolicy)
	}
	if settings.ApprovalsReviewer != "user" {
		t.Fatalf("approvals reviewer = %q, want user (managed default)", settings.ApprovalsReviewer)
	}
	profile, err := sandbox.ParseRuntimePermissionProfileJSON(string(settings.UnnamedPermissionProfile))
	if err != nil {
		t.Fatalf("ParseRuntimePermissionProfileJSON() error = %v (%s)", err, settings.UnnamedPermissionProfile)
	}
	if profile.SandboxPolicy == nil || profile.SandboxPolicy.Kind != sandbox.SandboxReadOnly {
		t.Fatalf("sandbox policy = %+v, want read-only", profile.SandboxPolicy)
	}
}

// TestResumeApproveForMeRejectsUnverifiableRequirementsLikeRust mirrors Rust
// #63589: a malformed managed restriction stops the resume instead of
// checkpointing a preset the requirements cannot confirm.
func TestResumeApproveForMeRejectsUnverifiableRequirementsLikeRust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	t.Setenv("TERM", "xterm-256color")
	const threadID = "0198f100-0000-7000-8000-0000000000b2"
	seedResumeApproveForMeThread(t, home, threadID, home)
	if err := os.WriteFile(filepath.Join(home, "requirements.toml"), []byte("allowed_approval_policies = []\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(requirements.toml) error = %v", err)
	}

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"resume", threadID, "--approve-for-me", "--cd", home}, newTerminalReader(""), terminalOut(&stdout), &stderr)
	if err == nil {
		t.Fatalf("resume --approve-for-me accepted empty allowed_approval_policies (stdout %q)", stdout.String())
	}
	if !strings.Contains(err.Error(), "requirements") {
		t.Fatalf("error = %v, want a requirements failure", err)
	}
	path, found, lookupErr := rollout.FindRolloutPathByRolloutID(home, threadID)
	if lookupErr == nil && found {
		lines, _, loadErr := rollout.Load(path)
		if loadErr == nil {
			if _, ok := rollout.LatestPersistedResumeSettings(threadID, lines); ok {
				t.Fatal("resume checkpointed preset settings despite unverifiable requirements")
			}
		}
	}
}
