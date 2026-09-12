package tea

import (
	"strings"
	"testing"

	"codex_go/protocol"
	codextui "codex_go/tui"
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
