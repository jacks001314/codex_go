package appserver

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"codex_go/rollout"
	"codex_go/session"
)

// Mirrors Rust
// `codex-rs/app-server/src/request_processors/feedback_rollout_history_tests.rs::includes_nested_history_bases`
// (#47653): nested `history_base` references are followed and each ancestor's
// prefix up to the recorded byte offset is attached.
func TestFeedbackHistoryBaseAttachmentsIncludesNestedBasesLikeRust(t *testing.T) {
	home := t.TempDir()
	grandparent, grandparentEnd := writeHistoryRolloutForTest(t, home, "0192f0aa-0000-7000-8000-0000000000c0", nil)
	parent, parentEnd := writeHistoryRolloutForTest(t, home, "0192f0aa-0000-7000-8000-0000000000c1", &grandparentEnd)
	child, _ := writeHistoryRolloutForTest(t, home, "0192f0aa-0000-7000-8000-0000000000c2", &parentEnd)

	attachments := FeedbackHistoryBaseAttachments(home, []FeedbackAttachmentPath{{Path: child}})
	if len(attachments) != 2 {
		t.Fatalf("attachments = %d, want parent and grandparent", len(attachments))
	}
	for i, want := range []string{parent, grandparent} {
		data, err := os.ReadFile(want)
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", want, err)
		}
		if attachments[i].Filename != filepath.Base(want) {
			t.Fatalf("attachment[%d].Filename = %q, want %q", i, attachments[i].Filename, filepath.Base(want))
		}
		if attachments[i].ContentType != "application/jsonl" {
			t.Fatalf("attachment[%d].ContentType = %q, want application/jsonl", i, attachments[i].ContentType)
		}
		if string(attachments[i].Buffer) != string(data) {
			t.Fatalf("attachment[%d] buffer = %q, want %q", i, string(attachments[i].Buffer), string(data))
		}
	}
}

// Rust skips an ancestor that is already selected for upload, so a report never
// carries the same rollout twice.
func TestFeedbackHistoryBaseAttachmentsSkipsSelectedRolloutsLikeRust(t *testing.T) {
	home := t.TempDir()
	first, firstEnd := writeHistoryRolloutForTest(t, home, "0192f0aa-0000-7000-8000-0000000000d0", nil)
	second, _ := writeHistoryRolloutForTest(t, home, "0192f0aa-0000-7000-8000-0000000000d1", &firstEnd)

	attachments := FeedbackHistoryBaseAttachments(home, []FeedbackAttachmentPath{{Path: first}, {Path: second}})
	if len(attachments) != 0 {
		t.Fatalf("attachments = %d, want none: the only ancestor is already selected", len(attachments))
	}
}

// writeHistoryRolloutForTest writes one pageable rollout and returns its path
// plus the history position covering the bytes it wrote.
func writeHistoryRolloutForTest(t *testing.T, home string, threadID string, base *rollout.HistoryPosition) (string, rollout.HistoryPosition) {
	t.Helper()
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	recorder, err := rollout.NewRecorder(&rollout.CreateParams{
		CodexHome:     home,
		ThreadID:      threadID,
		Source:        "cli",
		CWD:           "/repo",
		ModelProvider: "openai",
		HistoryMode:   "paginated",
		HistoryBase:   base,
		Now:           now,
	})
	if err != nil {
		t.Fatalf("NewRecorder(%s) error = %v", threadID, err)
	}
	if err := recorder.AppendItem(rollout.Item{ID: "user-1", Type: "user_message", Data: map[string]any{"text": "history", "turn_id": "turn-1"}}); err != nil {
		t.Fatalf("AppendItem() error = %v", err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	path, err := rollout.FindThreadPath(home, threadID, false)
	if err != nil {
		t.Fatalf("FindThreadPath(%s) error = %v", threadID, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%s) error = %v", path, err)
	}
	ordinal := uint64(0)
	if base != nil {
		ordinal = base.EndOrdinalExclusive
	}
	return path, rollout.HistoryPosition{
		ThreadID:            threadID,
		EndOrdinalExclusive: ordinal + 1,
		EndByteOffset:       uint64(info.Size()),
	}
}

// Mirrors the Rust app-server integration test for #47653: a feedback report
// that includes logs carries the inherited parent history in its rollout
// archive, even though the parent rollout was never selected explicitly.
func TestRuntimeRouterFeedbackAttachesInheritedHistoryLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(home)
	threadRouter := NewRouter(store)
	grandparentPath, grandparentEnd := writeHistoryRolloutForTest(t, home, "0192f0aa-0000-7000-8000-0000000000e0", nil)
	parentID := "feedback-parent"
	parentPath, _ := writeHistoryRolloutForTest(t, home, parentID, &grandparentEnd)
	now := time.Now().UTC()
	if err := store.Save(&session.Record{
		ID:        session.ThreadID(parentID),
		SessionID: parentID,
		CreatedAt: now,
		UpdatedAt: now,
		Metadata:  session.Metadata{CWD: home, ModelProvider: "openai"},
	}); err != nil {
		t.Fatalf("Save(%s) error = %v", parentID, err)
	}
	snapshot := &FeedbackSnapshot{Diagnostics: NewFeedbackDiagnostics(nil)}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: threadRouter, Feedback: snapshot})
	response := router.Handle(requestWithParams(t, IntID(1), MethodFeedbackUpload, FeedbackUploadParams{
		Classification: "bug",
		ThreadID:       stringPointerAppserver(parentID),
		IncludeLogs:    true,
	}))
	if response.Error != nil {
		t.Fatalf("feedback upload error = %+v", response.Error)
	}
	prepared := snapshot.LastPrepared
	if prepared == nil {
		t.Fatalf("feedback upload was not prepared")
	}
	archive := feedbackAttachmentByName(prepared.Attachments, FeedbackRolloutArchiveFilename)
	if archive == nil {
		t.Fatalf("feedback attachments = %#v", prepared.Attachments)
	}
	entries := readRolloutArchiveLikeRust(t, archive.Buffer)
	if len(entries) != 2 {
		t.Fatalf("rollout archive entries = %v, want the selected rollout plus its history base", entryNames(entries))
	}
	grandparentData, err := os.ReadFile(grandparentPath)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", grandparentPath, err)
	}
	if entries[filepath.Base(grandparentPath)] != string(grandparentData) {
		t.Fatalf("grandparent archive entry = %q, want %q", entries[filepath.Base(grandparentPath)], string(grandparentData))
	}
	if _, ok := entries[filepath.Base(parentPath)]; !ok {
		t.Fatalf("selected rollout missing from archive: %v", entryNames(entries))
	}
}

func entryNames(entries map[string]string) []string {
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	return names
}
