package appserver

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"codex_go/rollout"
	"codex_go/sandbox"
	"codex_go/session"
)

func newPersistedResumeSettingsFixture(t *testing.T) (*Router, *session.Record, string) {
	t.Helper()
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	now := fixedTime()
	cwd := t.TempDir()
	threadID := session.ThreadID("thread-persisted-settings")
	record := &session.Record{
		ID:        threadID,
		SessionID: string(threadID),
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata: session.Metadata{
			CWD:            cwd,
			Model:          "gpt-5",
			ApprovalPolicy: "never",
			HistoryMode:    string(ThreadHistoryLegacy),
			SessionPrefix:  session.PrefixForSessionID(string(threadID)),
			Extra:          map[string]any{},
			RolloutTurns:   []session.TurnSnapshot{{ID: "turn-1", Status: string(TurnStatusCompleted)}},
		},
	}
	if err := store.Create(record); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := router.createThreadRollout(record, now); err != nil {
		t.Fatalf("createThreadRollout() error = %v", err)
	}
	return router, record, cwd
}

func appendPersistedResumeHistory(t *testing.T, router *Router, record *session.Record, cwd string) {
	t.Helper()
	recorder, err := rollout.Resume(router.threadRolloutPath(record))
	if err != nil {
		t.Fatalf("rollout.Resume() error = %v", err)
	}
	defer recorder.Close()
	staleContext, err := json.Marshal(map[string]any{
		"turn_id":                   "turn-1",
		"approval_policy":           "never",
		"approvals_reviewer":        "user",
		"active_permission_profile": map[string]any{"id": "read-only"},
	})
	if err != nil {
		t.Fatalf("Marshal(turn_context) error = %v", err)
	}
	if err := recorder.AppendLine(rollout.Line{Type: "turn_context", TurnContext: staleContext}); err != nil {
		t.Fatalf("AppendLine(turn_context) error = %v", err)
	}
	// The thread acknowledged new permissions after the stale turn context.
	if err := recorder.AppendThreadSettingsAppliedSnapshot(string(record.ID), rollout.ThreadSettingsApplied{
		ApprovalPolicy:          "on-request",
		ApprovalsReviewer:       "auto-review",
		ActivePermissionProfile: json.RawMessage(`{"id":"dev"}`),
		PermissionProfile:       json.RawMessage(`{"file_system":{"entries":[],"type":"restricted"},"network":"enabled","type":"managed"}`),
		CWD:                     cwd,
	}, fixedTime().Add(time.Minute)); err != nil {
		t.Fatalf("AppendThreadSettingsAppliedSnapshot() error = %v", err)
	}
}

func TestColdResumeRestoresOwnedSettingsOverStaleTurnContextLikeRust(t *testing.T) {
	router, record, cwd := newPersistedResumeSettingsFixture(t)
	appendPersistedResumeHistory(t, router, record, cwd)

	response, err := router.handleThreadResume(requestWithParams(t, IntID(1), MethodThreadResume, ThreadResumeParams{
		ThreadID: string(record.ID),
	}))
	if err != nil {
		t.Fatalf("thread/resume error = %v", err)
	}
	if policy, _ := response.ApprovalPolicy.(string); policy != "on-request" {
		t.Fatalf("approval policy = %#v, want the owned snapshot's on-request", response.ApprovalPolicy)
	}
	if response.ApprovalsReviewer == nil || *response.ApprovalsReviewer != "auto-review" {
		t.Fatalf("approvals reviewer = %#v, want auto-review", response.ApprovalsReviewer)
	}
	if response.ActivePermissionProfile == nil || response.ActivePermissionProfile.ID != "dev" {
		t.Fatalf("active permission profile = %#v, want dev", response.ActivePermissionProfile)
	}
	updated, err := router.store.Read(record.ID, true, true)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if updated.Metadata.ActivePermissionProfile != "dev" {
		t.Fatalf("persisted active permission profile = %q, want dev", updated.Metadata.ActivePermissionProfile)
	}
	if reviewer := stringFromMap(threadRecordConfigOverrides(updated), "approvals_reviewer"); reviewer != "auto-review" {
		t.Fatalf("persisted approvals_reviewer override = %q, want auto-review", reviewer)
	}
}

func TestColdResumeExplicitSettingsBeatOwnedSnapshotLikeRust(t *testing.T) {
	router, record, cwd := newPersistedResumeSettingsFixture(t)
	appendPersistedResumeHistory(t, router, record, cwd)

	readOnly := sandbox.BuiltInPermissionProfileReadOnly
	reviewer := "user"
	response, err := router.handleThreadResume(requestWithParams(t, IntID(2), MethodThreadResume, ThreadResumeParams{
		ThreadID:          string(record.ID),
		ApprovalPolicy:    "never",
		ApprovalsReviewer: &reviewer,
		Permissions:       &readOnly,
	}))
	if err != nil {
		t.Fatalf("thread/resume error = %v", err)
	}
	if policy, _ := response.ApprovalPolicy.(string); policy != "never" {
		t.Fatalf("approval policy = %#v, want the explicit never", response.ApprovalPolicy)
	}
	if response.ApprovalsReviewer == nil || *response.ApprovalsReviewer != "user" {
		t.Fatalf("approvals reviewer = %#v, want the explicit user", response.ApprovalsReviewer)
	}
	if response.ActivePermissionProfile == nil || response.ActivePermissionProfile.ID != readOnly {
		t.Fatalf("active permission profile = %#v, want %s", response.ActivePermissionProfile, readOnly)
	}
	updated, err := router.store.Read(record.ID, true, true)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if updated.Metadata.ActivePermissionProfile != readOnly {
		t.Fatalf("persisted active permission profile = %q, want %s", updated.Metadata.ActivePermissionProfile, readOnly)
	}
	if reviewer := stringFromMap(threadRecordConfigOverrides(updated), "approvals_reviewer"); reviewer != "" {
		t.Fatalf("persisted approvals_reviewer override = %q, want none", reviewer)
	}
}

func TestColdForkRestoresOwnedSettingsOverStaleTurnContextLikeRust(t *testing.T) {
	router, record, cwd := newPersistedResumeSettingsFixture(t)
	appendPersistedResumeHistory(t, router, record, cwd)

	response, err := router.handleThreadFork(requestWithParams(t, IntID(3), MethodThreadFork, ThreadForkParams{
		ThreadID:     string(record.ID),
		ExcludeTurns: true,
	}))
	if err != nil {
		t.Fatalf("thread/fork error = %v", err)
	}
	if policy, _ := response.ApprovalPolicy.(string); policy != "on-request" {
		t.Fatalf("approval policy = %#v, want the owned snapshot's on-request", response.ApprovalPolicy)
	}
	if response.ApprovalsReviewer == nil || *response.ApprovalsReviewer != "auto-review" {
		t.Fatalf("approvals reviewer = %#v, want auto-review", response.ApprovalsReviewer)
	}
	if response.ActivePermissionProfile == nil || response.ActivePermissionProfile.ID != "dev" {
		t.Fatalf("active permission profile = %#v, want dev", response.ActivePermissionProfile)
	}
	if response.Thread == nil {
		t.Fatal("forked thread is nil")
	}
	forked, err := router.store.Read(session.ThreadID(response.Thread.ID), true, true)
	if err != nil {
		t.Fatalf("Read(fork) error = %v", err)
	}
	if forked.Metadata.ActivePermissionProfile != "dev" {
		t.Fatalf("forked active permission profile = %q, want dev", forked.Metadata.ActivePermissionProfile)
	}
	if reviewer := stringFromMap(threadRecordConfigOverrides(forked), "approvals_reviewer"); reviewer != "auto-review" {
		t.Fatalf("forked approvals_reviewer override = %q, want auto-review", reviewer)
	}
}

func TestColdResumeRestoresOwnedUnnamedPermissionProfileLikeRust(t *testing.T) {
	router, record, cwd := newPersistedResumeSettingsFixture(t)
	recorder, err := rollout.Resume(router.threadRolloutPath(record))
	if err != nil {
		t.Fatalf("rollout.Resume() error = %v", err)
	}
	staleContext, err := json.Marshal(map[string]any{
		"turn_id":            "turn-1",
		"approval_policy":    "never",
		"permission_profile": map[string]any{"type": "disabled"},
	})
	if err != nil {
		t.Fatalf("Marshal(turn_context) error = %v", err)
	}
	if err := recorder.AppendLine(rollout.Line{Type: "turn_context", TurnContext: staleContext}); err != nil {
		t.Fatalf("AppendLine(turn_context) error = %v", err)
	}
	if err := recorder.AppendThreadSettingsAppliedSnapshot(string(record.ID), rollout.ThreadSettingsApplied{
		ApprovalPolicy:    "on-request",
		PermissionProfile: json.RawMessage(`{"type":"disabled"}`),
		CWD:               cwd,
	}, fixedTime().Add(time.Minute)); err != nil {
		t.Fatalf("AppendThreadSettingsAppliedSnapshot() error = %v", err)
	}
	_ = recorder.Close()

	response, err := router.handleThreadResume(requestWithParams(t, IntID(4), MethodThreadResume, ThreadResumeParams{
		ThreadID: string(record.ID),
	}))
	if err != nil {
		t.Fatalf("thread/resume error = %v", err)
	}
	if response.ActivePermissionProfile != nil {
		t.Fatalf("active permission profile = %#v, want the unnamed profile", response.ActivePermissionProfile)
	}
	updated, err := router.store.Read(record.ID, true, true)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	raw, ok := updated.Metadata.Extra["permission_profile"]
	if !ok {
		t.Fatalf("permission_profile metadata = %#v, want the owned unnamed profile", updated.Metadata.Extra)
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("Marshal(permission_profile) error = %v", err)
	}
	if !strings.Contains(string(encoded), `"disabled"`) {
		t.Fatalf("permission_profile metadata = %s, want the owned profile", encoded)
	}
}
