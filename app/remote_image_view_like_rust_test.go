package app

import (
	"path/filepath"
	"testing"

	"codex_go/appserver"
)

func TestRemoteImageViewItemCarriesLocalArtifactPathLikeRust(t *testing.T) {
	item := remoteProtocolItemFromPayload(appserver.ThreadItemPayload{
		"id":   "img-view-1",
		"type": "imageView",
		"path": "/workspace/diagram.png",
	}, true)
	if item.Type != "imageView" || item.Path != "/workspace/diagram.png" {
		t.Fatalf("imageView item = %#v", item)
	}
}

func TestRemoteHistoryImageItemsKeepPreviewPathsLikeRust(t *testing.T) {
	path := filepath.Join(t.TempDir(), "diagram.png")
	thread := &appserver.Thread{Turns: []appserver.Turn{{
		Items: []appserver.ThreadItem{
			{ID: "view-1", Type: "imageView", Data: map[string]any{"path": path}},
			{
				ID:     "gen-1",
				Type:   "imageGeneration",
				Status: "completed",
				Data: map[string]any{
					"revisedPrompt": "a red cube",
					"savedPath":     path,
				},
			},
		},
	}}}
	messages := remoteTUIThreadMessagesFromThread(thread, reasoningProjectionChatWidget, false)
	if len(messages) != 2 {
		t.Fatalf("messages = %#v", messages)
	}
	if messages[0].ImagePath != path {
		t.Fatalf("viewed image path = %q, want %q", messages[0].ImagePath, path)
	}
	if ids := messages[0].ActivityIDs; len(ids) != 1 || ids[0] != "image-view:view-1" {
		t.Fatalf("viewed image activity ids = %#v", ids)
	}
	if messages[1].ImagePath != path {
		t.Fatalf("generated image path = %q, want %q", messages[1].ImagePath, path)
	}
}
