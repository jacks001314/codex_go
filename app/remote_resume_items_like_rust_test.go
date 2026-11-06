package app

import (
	"strings"
	"testing"

	"codex_go/appserver"
)

// Rust #46710: a resumed remote thread reuses the live rich history cells for
// its persisted tool items instead of flattening them to status text.
func TestRemoteThreadMessagesRestoreRichToolCellsLikeRust(t *testing.T) {
	thread := &appserver.Thread{
		CWD: "/workspace",
		Turns: []appserver.Turn{{Items: []appserver.ThreadItem{
			{
				ID:     "cmd-1",
				Type:   "commandExecution",
				Status: "completed",
				Data: map[string]any{
					"command":          "ls -la",
					"aggregatedOutput": "file-one\n",
					"exitCode":         0,
				},
			},
			{
				ID:     "patch-1",
				Type:   "fileChange",
				Status: "completed",
				Data: map[string]any{
					"changes": []any{map[string]any{
						"path": "src/main.rs",
						"kind": map[string]any{"type": "update"},
						"diff": "@@ -1 +1 @@\n-a\n+b\n",
					}},
				},
			},
			{
				ID:     "dyn-1",
				Type:   "dynamicToolCall",
				Status: "completed",
				Data: map[string]any{
					"tool":      "task_create",
					"arguments": map[string]any{"title": "demo"},
					"contentItems": []any{
						map[string]any{"type": "inputText", "text": "created"},
					},
				},
			},
			{
				ID:     "mcp-1",
				Type:   "mcpToolCall",
				Status: "in_progress",
				Data: map[string]any{
					"server": "docs",
					"tool":   "lookup",
				},
			},
		}}},
	}
	messages := remoteTUIThreadMessagesFromThread(thread, reasoningProjectionChatWidget, false)
	if len(messages) != 4 {
		t.Fatalf("messages = %d, want 4: %#v", len(messages), messages)
	}
	if !strings.Contains(messages[0].Text, "ls -la") || !strings.Contains(messages[0].RawText, "file-one") {
		t.Fatalf("command cell = %#v", messages[0])
	}
	if !strings.Contains(messages[1].Text, "main.rs") || !strings.Contains(messages[1].Text, "Edited") {
		t.Fatalf("patch cell = %q", messages[1].Text)
	}
	if len(messages[1].ActivityIDs) != 1 || messages[1].ActivityIDs[0] != "patch:patch-1" {
		t.Fatalf("patch activity ids = %#v", messages[1].ActivityIDs)
	}
	if !strings.Contains(messages[2].Text, "Called task_create") || !strings.Contains(messages[2].Text, "created") {
		t.Fatalf("dynamic cell = %q", messages[2].Text)
	}
	if !strings.Contains(messages[3].Text, "mcp tool: docs/lookup \u00b7 In progress at last update") {
		t.Fatalf("pending MCP cell = %q", messages[3].Text)
	}
}
