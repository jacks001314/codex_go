package tea

import (
	chatwidget "codex_go/tui/chatwidget"
	bubbletea "github.com/charmbracelet/bubbletea"
)

// openMemoriesSettings mirrors Rust's /memories settings surface. The actual
// persisted values are supplied by the app-server/config layer when wired in;
// this keeps the TUI command actionable even in local-only sessions.
func (m *Model) openMemoriesSettings() {
	if m == nil {
		return
	}
	use := true
	generate := true
	if m.featureSettings != nil {
		if value, ok := m.featureSettings["memories"]; ok {
			use = value
		}
		if value, ok := m.featureSettings["memory_generation"]; ok {
			generate = value
		}
	}
	view := chatwidget.SelectionView{
		Title:    "Memories",
		Subtitle: "Configure memory use and generation.",
		Items: []chatwidget.SelectionItem{
			{Name: "Use memories: " + onOff(use), ID: "use_memories"},
			{Name: "Generate memories: " + onOff(generate), ID: "generate_memories"},
			{Name: "Reset memories", ID: "reset_memories"},
		},
	}
	m.openSelectionViewModal(ModalKindMemories, view)
	m.notice = "Memories"
}

func (m *Model) applyMemoriesModalOption(option string) bubbletea.Cmd {
	if m.featureSettings == nil {
		m.featureSettings = map[string]bool{}
	}
	switch option {
	case "use_memories":
		next := !m.featureSettings["memories"]
		m.featureSettings["memories"] = next
		if m.onWriteSettings != nil {
			return m.writeSettings("memories", []SettingsEdit{{KeyPath: "memories.use_memories", Value: next}})
		}
		m.notice = "Use memories: " + onOff(next)
	case "generate_memories":
		next := !m.featureSettings["memory_generation"]
		m.featureSettings["memory_generation"] = next
		if m.onWriteSettings != nil {
			return m.writeSettings("memories", []SettingsEdit{{KeyPath: "memories.generate_memories", Value: next}})
		}
		m.notice = "Generate memories: " + onOff(next)
	case "reset_memories":
		m.notice = "Memories reset requested."
	}
	return nil
}

func onOff(value bool) string {
	if value {
		return "on"
	}
	return "off"
}
