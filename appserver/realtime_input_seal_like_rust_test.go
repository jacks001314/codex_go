package appserver

// Mirrors Rust #62271's retry-safe accepted-input seal: the input lands in
// durable history at most once even when a seal is retried after an uncertain
// flush.

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/rollout"
	"codex_go/session"
)

func TestSealRealtimeInputAppendsOnceAcrossRetryLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(filepath.Join(home, "sessions"))
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		ThreadStatus: NewThreadStatusManager(),
	})
	t.Cleanup(func() { _ = router.Close() })

	start := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: t.TempDir()}))
	if start.Error != nil {
		t.Fatalf("thread/start: %+v", start.Error)
	}
	threadID := start.Result.(*ThreadStartResponse).Thread.ID
	item := session.Item{
		ID:             "user-seal-1",
		ResponseItemID: "msg_seal_1",
		Type:           "message",
		Role:           "user",
		Text:           "accepted input",
		CreatedAt:      time.Now().UTC(),
	}
	if err := router.sealRealtimeInput(threadID, item); err != nil {
		t.Fatalf("first seal: %v", err)
	}
	// The retry a caller performs after an uncertain flush must not append the
	// input a second time.
	if err := router.sealRealtimeInput(threadID, item); err != nil {
		t.Fatalf("retry seal: %v", err)
	}
	if err := router.sealRealtimeInput(threadID, item); err != nil {
		t.Fatalf("second retry seal: %v", err)
	}

	threadHome := router.codexHomeForRollout()
	path, err := rollout.FindThreadPath(threadHome, threadID, false)
	if err != nil || strings.TrimSpace(path) == "" {
		t.Fatalf("find rollout = %q, %v", path, err)
	}
	lines, _, err := rollout.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	sealed := 0
	for index := range lines {
		line := &lines[index]
		if line.Type == "item" || line.Type == "response_item" {
			if strings.TrimSpace(line.ItemID) == item.ID || strings.Contains(string(line.Item), item.ResponseItemID) {
				sealed++
			}
		}
	}
	if sealed != 1 {
		t.Fatalf("sealed input lines = %d, want exactly 1", sealed)
	}
}
