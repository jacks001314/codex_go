package appserver

import (
	"os"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/rollout"
	"codex_go/session"
	"codex_go/utils"
)

// Rust #62827: a thread whose configuration enables Sync Elastic budgeting
// checkpoints the resolved recorder limits on its thread settings snapshot so a
// resume restores them without recomputing from a possibly changed config.
func TestCheckpointThreadSettingsPersistsGuardianRetentionLikeRust(t *testing.T) {
	home := t.TempDir()
	body := `[features.guardianv2]
sync_elastic_budgeting = true
[features.guardianv2.sync_elastic_sections.trusted_skills]
min_total_section_tokens = 113000
max_section_entry_tokens = 1
[features.guardianv2.sync_elastic_sections.retained_user_instructions]
min_total_section_tokens = 900
max_section_entry_tokens = 250
`
	if err := os.WriteFile(config.ConfigPath(home), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	store := session.NewStore(t.TempDir())
	now := time.Now().UTC()
	cwd := t.TempDir()
	threadID := session.ThreadID("thread-retention-snapshot")
	record := &session.Record{
		ID: threadID, SessionID: string(threadID), CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{CWD: cwd, Model: "gpt-5", ApprovalPolicy: "never", Extra: map[string]any{}},
	}
	if err := store.Create(record); err != nil {
		t.Fatal(err)
	}
	threadRouter := NewRouter(store)
	if err := threadRouter.createThreadRollout(record, now); err != nil {
		t.Fatalf("createThreadRollout() error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: threadRouter, Config: config.NewConfigService(home)})
	defer router.Close()
	router.checkpointThreadSettings(string(threadID))

	path := router.services.ThreadRouter.threadRolloutPath(record)
	lines, _, err := rollout.Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	snapshot, ok := rollout.LatestPersistedOwnedThreadSettings(string(threadID), lines)
	if !ok {
		t.Fatal("no owned thread settings snapshot was checkpointed")
	}
	want := utils.ApproxBytesForTokens(250)
	if snapshot.GuardianRetention == nil || snapshot.GuardianRetention.AssistantRecordBytes == nil ||
		*snapshot.GuardianRetention.AssistantRecordBytes != want {
		t.Fatalf("guardian retention = %#v, want assistant_record_bytes %d", snapshot.GuardianRetention, want)
	}
}
