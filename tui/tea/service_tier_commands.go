package tea

import (
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	bottompane "codex_go/tui/bottom_pane"
	chatwidget "codex_go/tui/chatwidget"
)

// Service-tier slash commands (Rust #50472 chatwidget/slash_dispatch.rs
// InputResult::ServiceTierCommand): every advertised tier is reachable by its
// catalog name, so the bundled Ultra Fast tier answers `/ultrafast` and a custom
// catalog's `Express` tier answers `/express`.

// serviceTierCommandForInput resolves a submitted `/name` against the active
// model's catalog tier commands. Only a bare command with no arguments matches;
// otherwise the input stays a prompt.
func (m *Model) serviceTierCommandForInput(input string) (*bottompane.ServiceTierCommand, bool) {
	if m == nil {
		return nil, false
	}
	trimmed := strings.TrimSpace(input)
	if !strings.HasPrefix(trimmed, "/") || strings.ContainsAny(trimmed, " \t\n") {
		return nil, false
	}
	name := strings.TrimPrefix(trimmed, "/")
	for _, tier := range m.serviceTierCommands {
		if !strings.EqualFold(strings.TrimSpace(tier.Name), name) {
			continue
		}
		// `/fast` keeps the builtin command's persisted "fast" alias and
		// unavailable-model notice; the catalog command path only owns the
		// remaining tiers.
		if strings.EqualFold(name, "fast") {
			continue
		}
		{
			copy := tier
			return &copy, true
		}
	}
	return nil, false
}

// applyServiceTierCommand selects a tier, or clears the selection back to the
// implicit default when that tier is already active (Rust
// NextServiceTierForToggle). The choice persists through the same settings path
// as `/fast` and becomes the saved selection the effective tier is recomputed
// from on the next model change.
func (m *Model) applyServiceTierCommand(command bottompane.ServiceTierCommand) bubbletea.Cmd {
	if m == nil || m.State == nil {
		return nil
	}
	id := strings.TrimSpace(command.ID)
	if id == "" {
		return nil
	}
	next := id
	if strings.TrimSpace(m.State.ServiceTier) == id {
		next = chatwidget.ServiceTierDefaultRequestValue
	}
	m.State.ServiceTier = next
	m.savedServiceTier = next
	if m.onWriteSettings == nil {
		m.notice = "Service tier set to " + next
		m.refreshTranscript()
		return nil
	}
	configValue := next
	if next == id && id == chatwidget.ServiceTierFastRequestValue {
		configValue = "fast"
	}
	return m.writeSettings(settingsWriteKindServiceTier, []SettingsEdit{{KeyPath: "service_tier", Value: configValue}})
}
