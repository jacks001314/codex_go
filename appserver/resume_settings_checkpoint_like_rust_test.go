package appserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/session"
)

// TestResumeSkipsUnchangedSettingsCheckpointLikeRust pins Rust #56250: resuming
// a materialized thread without overrides must not append another
// thread_settings_applied event, while a changed resume still checkpoints.
func TestResumeSkipsUnchangedSettingsCheckpointLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(filepath.Join(home, "sessions"))
	router := NewRouter(store)
	router.SetClock(func() time.Time { return fixedTime() })
	threadID := "thread-resume-checkpoint"
	repo := t.TempDir()
	record := &session.Record{
		ID:        session.ThreadID(threadID),
		SessionID: threadID,
		CreatedAt: fixedTime(),
		UpdatedAt: fixedTime(),
		RecencyAt: fixedTime(),
		Metadata: session.Metadata{
			CWD:            repo,
			ApprovalPolicy: "on-request",
			Model:          "gpt-5",
			ModelProvider:  "openai",
			SessionPrefix:  session.PrefixForSessionID(threadID),
		},
	}
	if err := store.Save(record); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := router.createThreadRollout(record, fixedTime()); err != nil {
		t.Fatalf("createThreadRollout() error = %v", err)
	}

	resume := func(id int64, policy any) {
		t.Helper()
		response := router.Handle(requestWithParams(t, IntID(id), MethodThreadResume, ThreadResumeParams{
			ThreadID:       threadID,
			ApprovalPolicy: policy,
		}))
		if response.Error != nil {
			t.Fatalf("resume error: %+v", response.Error)
		}
	}

	resume(1, nil)
	first := countThreadSettingsAppliedEvents(t, router.threadRolloutPath(record))
	if first != 1 {
		t.Fatalf("first resume checkpoints = %d, want 1", first)
	}

	resume(2, nil)
	if unchanged := countThreadSettingsAppliedEvents(t, router.threadRolloutPath(record)); unchanged != first {
		t.Fatalf("unchanged resume appended a checkpoint: %d -> %d", first, unchanged)
	}

	resume(3, "never")
	if changed := countThreadSettingsAppliedEvents(t, router.threadRolloutPath(record)); changed != first+1 {
		t.Fatalf("changed resume checkpoints = %d, want %d", changed, first+1)
	}
}

func countThreadSettingsAppliedEvents(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", path, err)
	}
	count := 0
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var event struct {
			Payload struct {
				Type string `json:"type"`
			} `json:"payload"`
		}
		if json.Unmarshal([]byte(line), &event) == nil && event.Payload.Type == "thread_settings_applied" {
			count++
		}
	}
	return count
}
