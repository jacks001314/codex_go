package app

import (
	"encoding/json"
	"testing"
	"time"

	bubbletea "charm.land/bubbletea/v2"

	"codex_go/appserver"
	codextui "codex_go/tui"
	codextea "codex_go/tui/tea"
)

// Rust App::notice_sidebar_file_item: a completed patch reaches the chooser's
// Files group as soon as its item completes, resolved against the workspace.
func TestRemoteTUIPromotesCompletedPatchToSidebarFilesLikeRust(t *testing.T) {
	messages := make(chan bubbletea.Msg, 8)
	state := codextui.NewState(nil)
	state.SetThreadID("thread-main")
	state.CWD = "/workspace"
	client := &remoteAppServerTUIClient{state: state, messages: messages}
	params, err := json.Marshal(appserver.ItemCompletedNotification{
		ThreadID: "thread-main",
		TurnID:   "turn-1",
		Item: appserver.ThreadItemPayload{
			"id":     "patch-1",
			"type":   "fileChange",
			"status": "completed",
			"changes": []any{
				map[string]any{"path": "src/app.ts", "kind": map[string]any{"type": "update"}},
				map[string]any{"path": "src/old.ts", "kind": map[string]any{"type": "delete"}},
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal notification: %v", err)
	}
	if err := client.handleNotification(remoteAppServerMessage{
		Method: string(appserver.NotificationItemCompleted),
		Params: params,
	}); err != nil {
		t.Fatalf("handle item completed: %v", err)
	}
	promoted := waitForSidebarPromotion(t, messages)
	if promoted.ParentThreadID != "thread-main" {
		t.Fatalf("parent = %q", promoted.ParentThreadID)
	}
	if len(promoted.Entries) != 1 || promoted.Entries[0].Path != "/workspace/src/app.ts" {
		t.Fatalf("entries = %+v, want only the updated file under the workspace", promoted.Entries)
	}
}

// Rust item_entries: only completed image generations contribute, and the
// saved path is resolved the same way.
func TestRemoteTUIPromotesGeneratedImageLikeRust(t *testing.T) {
	messages := make(chan bubbletea.Msg, 8)
	state := codextui.NewState(nil)
	state.SetThreadID("thread-main")
	state.CWD = "/workspace"
	client := &remoteAppServerTUIClient{state: state, messages: messages}
	params, err := json.Marshal(appserver.ItemCompletedNotification{
		ThreadID: "thread-main",
		TurnID:   "turn-1",
		Item: appserver.ThreadItemPayload{
			"id":        "image-1",
			"type":      "imageGeneration",
			"status":    "completed",
			"savedPath": "/tmp/generated.png",
		},
	})
	if err != nil {
		t.Fatalf("marshal notification: %v", err)
	}
	if err := client.handleNotification(remoteAppServerMessage{
		Method: string(appserver.NotificationItemCompleted),
		Params: params,
	}); err != nil {
		t.Fatalf("handle item completed: %v", err)
	}
	promoted := waitForSidebarPromotion(t, messages)
	if len(promoted.Entries) != 1 || promoted.Entries[0].Path != "/tmp/generated.png" {
		t.Fatalf("entries = %+v, want the generated image", promoted.Entries)
	}
}

func waitForSidebarPromotion(t *testing.T, messages <-chan bubbletea.Msg) codextea.SidebarFilePromotedMsg {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case message := <-messages:
			if promoted, ok := message.(codextea.SidebarFilePromotedMsg); ok {
				return promoted
			}
		case <-deadline:
			t.Fatal("no SidebarFilePromotedMsg reached the TUI")
		}
	}
}
