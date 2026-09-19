package tea

import (
	"strings"
	"testing"

	"codex_go/protocol"
	codextui "codex_go/tui"
	"codex_go/utils"
)

func cuaItem(id string, title string, status string) *protocol.ThreadItem {
	arguments := any(map[string]any{"title": title})
	return &protocol.ThreadItem{
		ID:        id,
		Type:      "mcp_tool_call",
		Server:    "cua_repl",
		Tool:      "exec",
		Status:    status,
		Arguments: &arguments,
	}
}

func completedCuaItem(id string, title string, content ...any) *protocol.ThreadItem {
	item := cuaItem(id, title, "completed")
	item.Result = &protocol.MCPToolResult{Content: content}
	return item
}

func computerActivityMessages(model *Model) []string {
	texts := []string{}
	for _, message := range model.State.Messages {
		if strings.Contains(message.Text, "computer") {
			texts = append(texts, message.Text)
		}
	}
	return texts
}

// TestComputerActivityGroupsAdjacentCallsInOneMessage covers Rust #43576:
// consecutive cua_repl calls update a single history message with the grouped
// computer-activity cell.
func TestComputerActivityGroupsAdjacentCallsInOneMessage(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 120, Height: 40})
	model.applyItemStarted(cuaItem("cua-1", "Click", "in_progress"), 0)
	model.applyItemCompleted(completedCuaItem("cua-1", "Click", map[string]any{"type": "text", "text": "ok"}))
	model.applyItemStarted(cuaItem("cua-2", "Type", "in_progress"), 0)
	model.applyItemCompleted(completedCuaItem("cua-2", "Type", map[string]any{"type": "text", "text": "ok"}))

	messages := computerActivityMessages(model)
	if len(messages) != 1 {
		t.Fatalf("computer activity messages = %d, want 1:\n%#v", len(messages), messages)
	}
	if !strings.Contains(messages[0], "Used computer") || !strings.Contains(messages[0], "2 actions") {
		t.Fatalf("grouped cell = %q", messages[0])
	}
}

// TestComputerActivityGroupEndsOnOtherItems covers the grouping boundary: any
// non-CUA item ends the group, and a later CUA call starts a new one.
func TestComputerActivityGroupEndsOnOtherItems(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 120, Height: 40})
	model.applyItemCompleted(completedCuaItem("cua-1", "Click", map[string]any{"type": "text", "text": "ok"}))
	// A non-CUA MCP call flushes the group and renders its own cell.
	otherArguments := any(map[string]any{"query": "x"})
	model.applyItemCompleted(&protocol.ThreadItem{
		ID: "mcp-1", Type: "mcp_tool_call", Server: "other", Tool: "search",
		Status: "completed", Arguments: &otherArguments,
		Result: &protocol.MCPToolResult{Content: []any{map[string]any{"type": "text", "text": "result"}}},
	})
	model.applyItemCompleted(completedCuaItem("cua-2", "Type", map[string]any{"type": "text", "text": "ok"}))

	groups := computerActivityMessages(model)
	if len(groups) != 2 {
		t.Fatalf("computer activity messages = %d, want 2 (new group after the boundary):\n%#v", len(groups), groups)
	}
	joined := strings.Join(groups, "\n")
	if !strings.Contains(joined, "1 action") {
		t.Fatalf("grouped cells = %q, want single-action groups", joined)
	}
	mcpMessages := 0
	for _, message := range model.State.Messages {
		if strings.Contains(message.Text, "Called") && strings.Contains(message.Text, "other.search") {
			mcpMessages++
		}
	}
	if mcpMessages != 1 {
		t.Fatalf("non-CUA MCP cell count = %d, want 1", mcpMessages)
	}
}

// TestComputerActivityGroupEndsOnTurnBoundary covers the turn-end flush and the
// interrupted-call failure path.
func TestComputerActivityGroupEndsOnTurnBoundary(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 120, Height: 40})
	model.setStatus("running")
	model.applyItemStarted(cuaItem("cua-1", "Click", "in_progress"), 0)
	if model.computerActivityGroup == nil || !model.computerActivityGroup.IsActive() {
		t.Fatal("a running CUA call must open an active group")
	}

	model.markActiveToolCallsFailed("Interrupted current turn.")
	if model.computerActivityGroup != nil {
		t.Fatal("the interrupted group must be flushed")
	}
	messages := computerActivityMessages(model)
	if len(messages) != 1 || !strings.Contains(messages[0], "Interrupted current turn.") {
		t.Fatalf("interrupted group = %#v", messages)
	}

	// A later CUA call starts a fresh group instead of reusing the flushed one.
	model.applyItemCompleted(completedCuaItem("cua-2", "Type", map[string]any{"type": "text", "text": "ok"}))
	if groups := computerActivityMessages(model); len(groups) != 2 {
		t.Fatalf("computer activity messages = %d, want 2", len(groups))
	}
}

// reasoningItem builds the completed reasoning summary item the app server
// emits for a reasoning summary (Rust #43921/#46565).
func completedReasoningItem(id string, summary string) *protocol.ThreadItem {
	return &protocol.ThreadItem{ID: id, Type: "reasoning", Summary: []string{summary}}
}

func computerActivityMessage(model *Model) *codextui.Message {
	for index := range model.State.Messages {
		if strings.Contains(model.State.Messages[index].Text, "Used computer") {
			return &model.State.Messages[index]
		}
	}
	return nil
}

// Mirrors Rust #46565's computer-activity snapshot: CUA calls and intervening
// reasoning share one cell, the reasoning keeps its chronological position in the
// expanded transcript, and compact/raw output stay reasoning-free.
func TestComputerActivityKeepsReasoningInOrderLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{Width: 120, Height: 40})

	model.applyItemStarted(cuaItem("cua-1", "Inspect page 1", "in_progress"), 0)
	model.applyItemCompleted(completedReasoningItem("reasoning-1", "Inspecting action 1"))
	model.applyItemCompleted(completedCuaItem("cua-1", "Inspect page 1", map[string]any{"type": "text", "text": "Full output for 1"}))
	model.applyItemCompleted(completedReasoningItem("reasoning-2", "Checking action 1"))
	model.applyItemStarted(cuaItem("cua-2", "Inspect page 2", "in_progress"), 0)
	model.applyItemCompleted(completedReasoningItem("reasoning-3", "Inspecting action 2"))
	model.applyItemCompleted(completedCuaItem("cua-2", "Inspect page 2", map[string]any{"type": "text", "text": "Full output for 2"}))
	model.applyItemCompleted(completedReasoningItem("reasoning-4", "Checking action 2"))

	groups := computerActivityMessages(model)
	if len(groups) != 1 {
		t.Fatalf("computer activity messages = %d, want one shared cell:\n%#v", len(groups), groups)
	}
	message := computerActivityMessage(model)
	if message == nil {
		t.Fatalf("grouped cell missing: %#v", groups)
	}
	if !strings.Contains(message.Text, "2 actions") {
		t.Fatalf("compact summary = %q, want both calls grouped", message.Text)
	}
	for _, reasoning := range []string{"Inspecting action 1", "Checking action 1", "Inspecting action 2", "Checking action 2"} {
		if strings.Contains(message.Text, reasoning) {
			t.Fatalf("compact preview leaked reasoning %q:\n%s", reasoning, message.Text)
		}
		if strings.Contains(message.RawText, reasoning) {
			t.Fatalf("raw output leaked reasoning %q:\n%s", reasoning, message.RawText)
		}
	}
	if !strings.Contains(message.RawText, "Inspect page 1") || !strings.Contains(message.RawText, "Inspect page 2") {
		t.Fatalf("raw output must keep both calls:\n%s", message.RawText)
	}
	transcript := utils.StripANSI(message.TranscriptText)
	last := -1
	for _, want := range []string{"Inspect page 1", "Inspecting action 1", "Checking action 1", "Inspect page 2", "Inspecting action 2", "Checking action 2"} {
		index := strings.Index(transcript, want)
		if index < 0 {
			t.Fatalf("expanded transcript missing %q:\n%s", want, transcript)
		}
		if index < last {
			t.Fatalf("expanded transcript out of order at %q:\n%s", want, transcript)
		}
		last = index
	}
	if !strings.HasPrefix(strings.TrimSpace(transcript), "\u2022 Called cua_repl.exec") {
		t.Fatalf("expanded transcript must start with the first call:\n%s", transcript)
	}
	for _, message := range state.Messages {
		if message.TranscriptOnly {
			t.Fatalf("reasoning must stay inside the activity group, not a standalone entry: %q", message.Text)
		}
	}
}
