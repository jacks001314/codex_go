package tea

import (
	"testing"

	codextui "codex_go/tui"
)

// TestDisconnectClearsStaleMCPStartupLikeRust mirrors Rust #63599's
// pause_for_disconnect: startup notifications from a disconnected transport can
// no longer finish, so stale MCP startup activity stops keeping the TUI busy.
// A real running turn keeps its own busy state.
func TestDisconnectClearsStaleMCPStartupLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	m := NewModel(state, Options{})
	m.mcpStartupActive = true
	m.mcpStartupHeader = "Starting MCP servers"
	if !m.isTaskRunning() {
		t.Fatal("MCP startup did not report the task as busy")
	}

	updated, _ := m.Update(AppServerDisconnectMsg{})
	m = updated.(*Model)
	if m.mcpStartupActive || m.mcpStartupHeader != "" {
		t.Fatalf("MCP startup survived the disconnect: active=%v header=%q", m.mcpStartupActive, m.mcpStartupHeader)
	}
	if m.isTaskRunning() {
		t.Fatal("stale MCP startup still keeps local commands busy")
	}

	m.State.Status = "running"
	updated, _ = m.Update(AppServerDisconnectMsg{})
	m = updated.(*Model)
	if !m.isTaskRunning() {
		t.Fatal("a running turn lost its busy state on disconnect")
	}
}
