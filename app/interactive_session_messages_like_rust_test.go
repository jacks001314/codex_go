package app

import (
	"regexp"
	"strings"
	"testing"

	"codex_go/session"
)

// ansiSGR matches the styling the diff renderer keeps on restored patch rows,
// matching the live history cell output.
var ansiSGR = regexp.MustCompile("\\x1b\\[[0-9;]*m")

// Rust #46710: a resumed command keeps both its arguments and its full output
// so the detailed and raw transcript views still have the data.
func TestSessionMessagesKeepToolArgumentsAndOutputLikeRust(t *testing.T) {
	record := &session.Record{
		ID: "thread-tool",
		Items: []session.Item{{
			ID:     "call-1",
			Type:   "command_execution",
			Status: "completed",
			Data: map[string]any{
				"command": "ls -la",
				"output":  "file-one\nfile-two\n",
			},
		}},
	}
	messages := interactiveSessionMessagesFromRecord(record, reasoningProjectionChatWidget, false)
	if len(messages) != 1 {
		t.Fatalf("messages = %d, want 1: %#v", len(messages), messages)
	}
	if !strings.Contains(messages[0].Text, "ls -la") {
		t.Fatalf("resumed compact row dropped the command:\n%s", messages[0].Text)
	}
	for _, want := range []string{"file-one", "file-two"} {
		if !strings.Contains(messages[0].RawText, want) {
			t.Fatalf("resumed raw transcript dropped %q:\n%s", want, messages[0].RawText)
		}
	}
}

// A recorded item whose Text repeats the output must not render it twice.
func TestSessionMessagesDoNotDuplicateRepeatedToolOutputLikeRust(t *testing.T) {
	record := &session.Record{
		ID: "thread-tool",
		Items: []session.Item{{
			Type:   "command_execution",
			Status: "completed",
			Text:   "same output",
			Data: map[string]any{
				"output": "same output",
			},
		}},
	}
	messages := interactiveSessionMessagesFromRecord(record, reasoningProjectionChatWidget, false)
	if len(messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(messages))
	}
	if count := strings.Count(messages[0].RawText, "same output"); count != 1 {
		t.Fatalf("output repeated %d times:\n%s", count, messages[0].RawText)
	}
}

// A command that never finished keeps its last known state without a running
// clock (Rust historical_tool_fallback).
func TestSessionMessagesKeepUnfinishedCommandStatusLikeRust(t *testing.T) {
	record := &session.Record{Items: []session.Item{{
		ID:     "call-1",
		Type:   "command_execution",
		Status: "in_progress",
		Data: map[string]any{
			"command": "go test ./...",
			"output":  "partial line\n",
		},
	}}}
	messages := interactiveSessionMessagesFromRecord(record, reasoningProjectionChatWidget, false)
	if len(messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(messages))
	}
	for _, want := range []string{"$ go test ./...", "In progress at last update", "partial line"} {
		if !strings.Contains(messages[0].Text, want) {
			t.Fatalf("unfinished command dropped %q:\n%s", want, messages[0].Text)
		}
	}
}

func TestSessionMessagesKeepDeclinedCommandStatusLikeRust(t *testing.T) {
	record := &session.Record{Items: []session.Item{{
		ID:     "call-1",
		Type:   "command_execution",
		Status: "declined",
		Data:   map[string]any{"command": "rm -rf build"},
	}}}
	messages := interactiveSessionMessagesFromRecord(record, reasoningProjectionChatWidget, false)
	if len(messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(messages))
	}
	if !strings.Contains(messages[0].Text, "Declined") || !strings.Contains(messages[0].Text, "$ rm -rf build") {
		t.Fatalf("declined command lost its outcome:\n%s", messages[0].Text)
	}
}

// MCP calls restore the rich result cell, and an unfinished call keeps its last
// known status instead of an empty shell row.
func TestSessionMessagesRestoreMcpStatusLikeRust(t *testing.T) {
	pending := interactiveSessionMessagesFromRecord(&session.Record{Items: []session.Item{{
		ID:     "mcp-1",
		Type:   "mcp_tool_call",
		Status: "in_progress",
		Data:   map[string]any{"server": "docs", "tool": "lookup"},
	}}}, reasoningProjectionChatWidget, false)
	if len(pending) != 1 || !strings.Contains(pending[0].Text, "mcp tool: docs/lookup \u00b7 In progress at last update") {
		t.Fatalf("pending MCP call lost its status: %#v", pending)
	}

	missing := interactiveSessionMessagesFromRecord(&session.Record{Items: []session.Item{{
		ID:     "mcp-2",
		Type:   "mcp_tool_call",
		Status: "completed",
		Data:   map[string]any{"server": "docs", "tool": "lookup"},
	}}}, reasoningProjectionChatWidget, false)
	if len(missing) != 1 || !strings.Contains(missing[0].Text, "Completed \u00b7 result unavailable") {
		t.Fatalf("MCP call without a result lost its outcome: %#v", missing)
	}

	done := interactiveSessionMessagesFromRecord(&session.Record{Items: []session.Item{{
		ID:     "mcp-3",
		Type:   "mcp_tool_call",
		Status: "completed",
		Data: map[string]any{
			"server":    "docs",
			"tool":      "lookup",
			"arguments": "{\"q\":\"rust\"}",
			"content":   []any{"first line"},
		},
	}}}, reasoningProjectionChatWidget, false)
	if len(done) != 1 {
		t.Fatalf("messages = %d, want 1", len(done))
	}
	if !strings.Contains(done[0].Text, "docs.lookup") {
		t.Fatalf("completed MCP call was not restored as a rich cell:\n%s", done[0].Text)
	}
	if !strings.Contains(done[0].RawText, "first line") {
		t.Fatalf("completed MCP call lost its result:\n%s", done[0].RawText)
	}
}

// A completed patch restores the rich diff, keeps its activity identity, and
// drops the wire "Moved to:" annotation the move path already renders.
func TestSessionMessagesRestorePatchLikeRust(t *testing.T) {
	record := &session.Record{Items: []session.Item{{
		ID:     "patch-1",
		Type:   "file_change",
		Status: "completed",
		Data: map[string]any{
			"changes": []any{map[string]any{
				"path":     "src/before.rs",
				"kind":     "update",
				"movePath": "src/after.rs",
				"diff":     "@@ -1 +1 @@\n-let before = 1;\n+let after = 2;\n\nMoved to: src/after.rs",
			}},
		},
	}}}
	messages := interactiveSessionMessagesFromRecord(record, reasoningProjectionChatWidget, false)
	if len(messages) != 1 {
		t.Fatalf("messages = %d, want 1: %#v", len(messages), messages)
	}
	message := messages[0]
	plain := ansiSGR.ReplaceAllString(message.Text, "")
	for _, want := range []string{"before.rs", "after.rs", "let after = 2;"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("patch diff dropped %q:\n%s", want, plain)
		}
	}
	if strings.Contains(plain, "Moved to:") {
		t.Fatalf("patch diff kept the redundant move annotation:\n%s", plain)
	}
	if len(message.ActivityIDs) != 1 || message.ActivityIDs[0] != "patch:patch-1" {
		t.Fatalf("patch activity ids = %#v, want [patch:patch-1]", message.ActivityIDs)
	}
}

// Unfinished and failed patches keep their real outcome instead of rendering a
// completed diff.
func TestSessionMessagesKeepUnfinishedPatchOutcomeLikeRust(t *testing.T) {
	record := &session.Record{Items: []session.Item{
		{ID: "p1", Type: "file_change", Status: "in_progress"},
		{ID: "p2", Type: "file_change", Status: "declined"},
		{ID: "p3", Type: "file_change", Status: "failed"},
	}}
	messages := interactiveSessionMessagesFromRecord(record, reasoningProjectionChatWidget, false)
	if len(messages) != 3 {
		t.Fatalf("messages = %d, want 3: %#v", len(messages), messages)
	}
	for index, want := range []string{"Patch application in progress", "Patch application declined", "Failed to apply patch"} {
		if !strings.Contains(messages[index].Text, want) {
			t.Fatalf("patch %d dropped %q:\n%s", index, want, messages[index].Text)
		}
	}
}

// A dynamic tool call keeps a bounded tail preview compactly while the
// transcript and raw views retain every argument and output line.
func TestSessionMessagesRestoreDynamicToolPreviewLikeRust(t *testing.T) {
	output := []string{"line-01", "line-02", "line-03", "line-04", "line-05", "line-06", "line-07", "line-08", "line-09", "line-10"}
	record := &session.Record{Items: []session.Item{{
		ID:     "dyn-1",
		Type:   "dynamic_tool_call",
		Status: "completed",
		Data: map[string]any{
			"name":      "task_create",
			"arguments": "{\"title\":\"demo\"}",
			"output":    strings.Join(output, "\n"),
		},
	}}}
	messages := interactiveSessionMessagesFromRecord(record, reasoningProjectionChatWidget, false)
	if len(messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(messages))
	}
	message := messages[0]
	for _, hidden := range []string{"line-01", "line-07"} {
		if strings.Contains(message.Text, hidden) {
			t.Fatalf("compact preview leaked hidden line %q:\n%s", hidden, message.Text)
		}
	}
	if !strings.Contains(message.Text, "\u2026 7 earlier lines hidden") {
		t.Fatalf("compact preview lost its hidden line count:\n%s", message.Text)
	}
	for _, want := range []string{"line-08", "line-09", "line-10"} {
		if !strings.Contains(message.Text, want) {
			t.Fatalf("compact preview dropped %q:\n%s", want, message.Text)
		}
	}
	detailed := message.TranscriptText
	if detailed == "" {
		detailed = message.RawText
	}
	for _, want := range []string{"Arguments: {\"title\":\"demo\"}", "line-01", "line-10"} {
		if !strings.Contains(detailed, want) {
			t.Fatalf("detailed transcript dropped %q:\n%s", want, detailed)
		}
	}
}

// A persisted function call and its output pair into one rich command row; the
// output item does not render on its own.
func TestSessionMessagesPairFunctionCallOutputLikeRust(t *testing.T) {
	record := &session.Record{Items: []session.Item{
		{
			ID:     "call-9",
			Type:   "function_call",
			Name:   "exec_command",
			CallID: "call-9",
			Data: map[string]any{
				"name":      "exec_command",
				"arguments": "{\"cmd\":\"echo hi\"}",
				"command":   "echo hi",
			},
		},
		{
			ID:     "out-9",
			Type:   "tool_output",
			Name:   "exec_command",
			CallID: "call-9",
			Text:   "hi\n",
			Data:   map[string]any{"output": "hi\n", "success": true},
		},
	}}
	messages := interactiveSessionMessagesFromRecord(record, reasoningProjectionChatWidget, false)
	if len(messages) != 1 {
		t.Fatalf("messages = %d, want 1: %#v", len(messages), messages)
	}
	if !strings.Contains(messages[0].Text, "echo hi") {
		t.Fatalf("paired command lost its arguments:\n%s", messages[0].Text)
	}
	if !strings.Contains(messages[0].RawText, "hi") {
		t.Fatalf("paired command lost its output:\n%s", messages[0].RawText)
	}
}

// A viewed image item restores the artifact path the fullscreen transcript may
// preview.
func TestSessionMessagesRestoreViewedImageLikeRust(t *testing.T) {
	record := &session.Record{Items: []session.Item{{
		ID:   "img-1",
		Type: "image_view",
		Data: map[string]any{"path": "/tmp/screenshot.png"},
	}}}
	messages := interactiveSessionMessagesFromRecord(record, reasoningProjectionChatWidget, false)
	if len(messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(messages))
	}
	if !strings.Contains(messages[0].Text, "Viewed image screenshot.png") {
		t.Fatalf("viewed image row = %q", messages[0].Text)
	}
	if messages[0].ImagePath == "" || !strings.HasSuffix(messages[0].ImagePath, "screenshot.png") {
		t.Fatalf("viewed image path = %q", messages[0].ImagePath)
	}
}
