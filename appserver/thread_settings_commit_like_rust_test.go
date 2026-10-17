package appserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/config"
	"codex_go/session"
)

func TestUpdateSettingsWithCommitKeepsItsSnapshotLikeRust(t *testing.T) {
	service := NewThreadExtraService()
	committedPolicy := "never"
	committedCWD := "/committed"
	_, committed, err := service.UpdateSettingsWithCommit(&SettingsUpdateParams{
		ThreadID:       "thread-commit",
		ApprovalPolicy: &committedPolicy,
		CWD:            &committedCWD,
	})
	if err != nil {
		t.Fatalf("UpdateSettingsWithCommit() error = %v", err)
	}
	if committed == nil || committed.ApprovalPolicy != "never" || committed.CWD != "/committed" {
		t.Fatalf("committed settings = %#v", committed)
	}

	// A later write must not mutate the snapshot this call committed.
	laterPolicy := "on-request"
	laterCWD := "/later"
	if _, err := service.UpdateSettings(&SettingsUpdateParams{
		ThreadID:       "thread-commit",
		ApprovalPolicy: &laterPolicy,
		CWD:            &laterCWD,
	}); err != nil {
		t.Fatalf("UpdateSettings(later) error = %v", err)
	}
	if committed.ApprovalPolicy != "never" || committed.CWD != "/committed" {
		t.Fatalf("committed snapshot changed after a later write: %#v", committed)
	}
	if current := service.Settings("thread-commit"); current == nil || current.ApprovalPolicy != "on-request" || current.CWD != "/later" {
		t.Fatalf("current settings = %#v", current)
	}
}

// TestPersistThreadSettingsUsesCommittedSnapshotLikeRust pins Rust #40645: the
// ThreadSettingsApplied snapshot comes from the commit that the operation made,
// even when another settings write lands before the post-commit work runs.
func TestPersistThreadSettingsUsesCommittedSnapshotLikeRust(t *testing.T) {
	home := t.TempDir()
	now := fixedTime()
	threadID := session.ThreadID("thread-settings-commit")
	store := session.NewStore(filepath.Join(home, "sessions"))
	threadRouter := NewRouter(store)
	record := &session.Record{
		ID: threadID, SessionID: string(threadID), CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{CWD: t.TempDir(), SessionPrefix: session.PrefixForSessionID(string(threadID))},
	}
	if err := store.Create(record); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := threadRouter.createThreadRollout(record, now); err != nil {
		t.Fatalf("createThreadRollout() error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: threadRouter,
		Config:       config.NewConfigService(home),
	})
	defer router.Close()
	service := router.requireThreadExtras()

	policy := "never"
	cwd := "/committed"
	_, committed, err := service.UpdateSettingsWithCommit(&SettingsUpdateParams{
		ThreadID:       string(threadID),
		ApprovalPolicy: &policy,
		CWD:            &cwd,
	})
	if err != nil {
		t.Fatalf("UpdateSettingsWithCommit() error = %v", err)
	}
	// Another settings write commits before this operation's post-commit work.
	laterPolicy := "on-request"
	laterCWD := "/later"
	if _, _, err := service.UpdateSettingsWithCommit(&SettingsUpdateParams{
		ThreadID:       string(threadID),
		ApprovalPolicy: &laterPolicy,
		CWD:            &laterCWD,
	}); err != nil {
		t.Fatalf("UpdateSettingsWithCommit(later) error = %v", err)
	}
	if err := router.persistThreadSettingsUpdate(&SettingsUpdateParams{
		ThreadID:       string(threadID),
		ApprovalPolicy: &policy,
		CWD:            &cwd,
	}, committed); err != nil {
		t.Fatalf("persistThreadSettingsUpdate() error = %v", err)
	}

	data, err := os.ReadFile(threadRouter.threadRolloutPath(record))
	if err != nil {
		t.Fatalf("ReadFile(rollout) error = %v", err)
	}
	var applied map[string]any
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var event map[string]any
		if json.Unmarshal([]byte(line), &event) != nil {
			continue
		}
		payload, _ := event["payload"].(map[string]any)
		if payload["type"] != "thread_settings_applied" {
			continue
		}
		applied = payload
	}
	if applied == nil {
		t.Fatalf("rollout has no thread_settings_applied event:\n%s", data)
	}
	settings, _ := applied["thread_settings"].(map[string]any)
	if settings["approval_policy"] != "never" || settings["cwd"] != "/committed" {
		t.Fatalf("thread_settings_applied = %#v, want the committed snapshot", applied)
	}
}
