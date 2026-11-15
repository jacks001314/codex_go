package appserver

import (
	"os"
	"path/filepath"
	"testing"

	"codex_go/session"
)

// Mirrors Rust #61359: queued local image and audio paths resolve against the
// thread working directory, absolute paths keep their meaning, and a relative
// path for a thread without a recorded directory is rejected.
func TestThreadQueueResolvesAttachmentsAgainstThreadCWDLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	threadCWD := t.TempDir()
	threadImage := filepath.Join(threadCWD, "image.png")
	if err := os.WriteFile(threadImage, []byte("thread image"), 0o600); err != nil {
		t.Fatalf("WriteFile(image) error = %v", err)
	}
	threadAudio := filepath.Join(threadCWD, "clip.wav")
	if err := os.WriteFile(threadAudio, []byte("thread audio"), 0o600); err != nil {
		t.Fatalf("WriteFile(audio) error = %v", err)
	}
	threadID := session.ThreadID("thread-queue-attachments")
	record := &session.Record{
		ID:        threadID,
		SessionID: string(threadID),
		Metadata:  session.Metadata{HistoryMode: "legacy", CWD: threadCWD},
	}
	if err := store.Create(record); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	add := router.Handle(requestWithParams(t, IntID(1), MethodThreadQueueAdd, ThreadQueueAddParams{
		ThreadID: string(threadID),
		Input:    []any{map[string]any{"type": "localImage", "path": "image.png"}},
	}))
	if add.Error != nil {
		t.Fatalf("queue add error = %+v", add.Error)
	}
	submission := add.Result.(*ThreadQueueAddResponse).QueuedSubmission
	if submission == nil || len(submission.Input) != 1 {
		t.Fatalf("added submission = %#v", submission)
	}
	if got := queuedInputPath(t, submission.Input[0]); got != threadImage {
		t.Fatalf("added image path = %q, want %q", got, threadImage)
	}

	update := router.Handle(requestWithParams(t, IntID(2), MethodThreadQueueUpdate, ThreadQueueUpdateParams{
		ThreadID:           string(threadID),
		QueuedSubmissionID: submission.ID,
		Input: []any{
			map[string]any{"type": "text", "text": "updated"},
			map[string]any{"type": "localAudio", "path": "clip.wav"},
		},
	}))
	if update.Error != nil {
		t.Fatalf("queue update error = %+v", update.Error)
	}
	updated := update.Result.(*ThreadQueueUpdateResponse).QueuedSubmission
	if updated == nil || len(updated.Input) != 2 {
		t.Fatalf("updated submission = %#v", updated)
	}
	if got := queuedInputPath(t, updated.Input[1]); got != threadAudio {
		t.Fatalf("updated audio path = %q, want %q", got, threadAudio)
	}

	absoluteImage := filepath.Join(t.TempDir(), "absolute.png")
	absolute := router.Handle(requestWithParams(t, IntID(3), MethodThreadQueueUpdate, ThreadQueueUpdateParams{
		ThreadID:           string(threadID),
		QueuedSubmissionID: submission.ID,
		Input:              []any{map[string]any{"type": "localImage", "path": absoluteImage}},
	}))
	if absolute.Error != nil {
		t.Fatalf("absolute update error = %+v", absolute.Error)
	}
	if got := queuedInputPath(t, absolute.Result.(*ThreadQueueUpdateResponse).QueuedSubmission.Input[0]); got != absoluteImage {
		t.Fatalf("absolute image path = %q, want %q", got, absoluteImage)
	}
}

func TestThreadQueueRejectsRelativeAttachmentWithoutThreadCWDLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	threadID := session.ThreadID("thread-queue-no-cwd")
	record := &session.Record{ID: threadID, SessionID: string(threadID), Metadata: session.Metadata{HistoryMode: "legacy"}}
	if err := store.Create(record); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadQueueAdd, ThreadQueueAddParams{
		ThreadID: string(threadID),
		Input:    []any{map[string]any{"type": "localImage", "path": "image.png"}},
	}))
	if response.Error == nil || response.Error.Code != -32600 ||
		response.Error.Message != "local queued attachment path cannot be resolved against the thread working directory" {
		t.Fatalf("response = %+v", response.Error)
	}
	submissions, _, err := store.ListQueueSubmissions(threadID, "", 0)
	if err != nil {
		t.Fatalf("ListQueueSubmissions() error = %v", err)
	}
	if len(submissions) != 0 {
		t.Fatalf("queue = %#v, want nothing enqueued", submissions)
	}
}

func queuedInputPath(t *testing.T, item any) string {
	t.Helper()
	entry, ok := item.(map[string]any)
	if !ok {
		t.Fatalf("queued input item = %#v, want a map", item)
	}
	path, _ := entry["path"].(string)
	return path
}
