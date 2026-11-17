package appserver

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/runtimeutil"
	"codex_go/session"
	"codex_go/turn"
)

// TestTurnDiffUsesEnvironmentRootAndClosesBeforeCompletionLikeRust pins Rust
// #61820: file changes measured in a turn environment render relative to that
// environment's display root, and the aggregate diff is emitted once more
// immediately before turn/completed, not only when a patch arrives.
func TestTurnDiffUsesEnvironmentRootAndClosesBeforeCompletionLikeRust(t *testing.T) {
	cwd := t.TempDir()
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter:   NewRouter(session.NewStore(t.TempDir())),
		Turns:          turn.NewTurnService(),
		ThreadStatus:   NewThreadStatusManager(),
		ServerRequests: NewServerRequestBroker(),
		Environment:    NewEnvironmentManager(EnvironmentShellInfo{Name: "sh"}, cwd),
	})
	sink := NewNotificationBuffer()
	router.SetNotificationSink(sink)
	const threadID = "thread-turn-diff"
	const turnID = "turn-turn-diff"
	params := &turn.TurnStartParams{ThreadID: threadID, CWD: cwd}
	if err := router.threads.RegisterTurn(threadID, turnID, func() {}, time.Now().UTC().UnixMilli(), params); err != nil {
		t.Fatalf("register turn: %v", err)
	}

	path := filepath.Join(cwd, "diff.txt")
	router.notifyTurnDiffFromSessionItem(threadID, turnID, &session.Item{
		Name: "apply_patch",
		Data: map[string]any{
			"environmentId": localEnvironmentID,
			"success":       true,
			"appliedChanges": []runtimeutil.FileChange{{
				Kind:       runtimeutil.ChangeAdd,
				Path:       path,
				NewContent: "hello\n",
			}},
		},
	})
	first := lastTurnDiffNotification(sink, turnID)
	if first == nil {
		t.Fatal("apply patch did not publish a turn diff")
	}
	if !strings.Contains(first.Diff, "a/diff.txt") || strings.Contains(first.Diff, cwd) {
		t.Fatalf("turn diff did not use the environment display root: %q", first.Diff)
	}

	startedAt := time.Now().UTC().Unix()
	completedAt := startedAt + 1
	durationMS := int64(1000)
	if !router.notifyTurnCompletedOnce(&TurnCompletedNotification{
		ThreadID: threadID,
		Turn:     completedTurnNotificationTurn(turnID, "", TurnStatusCompleted, nil, &startedAt, &completedAt, &durationMS),
	}) {
		t.Fatal("turn completion was not claimed")
	}
	notifications := sink.List()
	if len(notifications) < 2 {
		t.Fatalf("notifications = %d", len(notifications))
	}
	last, previous := notifications[len(notifications)-1], notifications[len(notifications)-2]
	if last.Method != NotificationTurnCompleted || previous.Method != NotificationTurnDiffUpdated {
		t.Fatalf("terminal order = %s then %s, want turn/diff/updated then turn/completed", previous.Method, last.Method)
	}
	final, ok := previous.Params.(*TurnDiffUpdatedNotification)
	if !ok || final.Diff != first.Diff {
		t.Fatalf("final diff = %#v, want %q", previous.Params, first.Diff)
	}
}

func lastTurnDiffNotification(sink *NotificationBuffer, turnID string) *TurnDiffUpdatedNotification {
	if sink == nil {
		return nil
	}
	var last *TurnDiffUpdatedNotification
	for _, notification := range sink.List() {
		if notification.Method != NotificationTurnDiffUpdated {
			continue
		}
		payload, ok := notification.Params.(*TurnDiffUpdatedNotification)
		if !ok || payload == nil || payload.TurnID != turnID {
			continue
		}
		last = payload
	}
	return last
}
