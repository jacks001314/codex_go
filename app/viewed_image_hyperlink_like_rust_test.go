package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/appserver"
	codextui "codex_go/tui"
)

// Rust parity: codex-rs/tui/src/app/history_pagination.rs (#58446) keeps the
// viewed-image link on cells loaded from older history, while an SSH session
// cannot open a local file.
func TestRemoteHistoryViewedImageRowLinksLocalFileLikeRust(t *testing.T) {
	if remoteTUIImageLinksOverSSH() {
		t.Skip("this terminal reports an SSH session")
	}
	path := filepath.Join(t.TempDir(), "diagram.png")
	if err := os.WriteFile(path, []byte("png"), 0o644); err != nil {
		t.Fatalf("write image: %v", err)
	}
	destination, ok := codextui.ViewedImageDestination(path)
	if !ok {
		t.Fatalf("the fixture image produced no destination: %q", path)
	}

	thread := &appserver.Thread{Turns: []appserver.Turn{{
		Items: []appserver.ThreadItem{{ID: "view-1", Type: "imageView", Data: map[string]any{"path": path}}},
	}}}
	messages := remoteTUIThreadMessagesFromThread(thread, reasoningProjectionChatWidget, false)
	if len(messages) != 1 {
		t.Fatalf("messages = %#v, want one viewed-image row", messages)
	}
	if !strings.Contains(messages[0].Text, "\x1b]8;;"+destination+"\x07diagram.png") {
		t.Fatalf("history row = %q, want an OSC-8 file link to %q", messages[0].Text, destination)
	}
}
