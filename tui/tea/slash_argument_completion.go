package tea

import (
	"strings"

	bubbletea "charm.land/bubbletea/v2"
)

// Rust parity: codex-rs/tui/src/bottom_pane/chat_composer/slash_argument.rs
// (#62343).
//
// The slash menu completes command names, so a command with a small fixed set
// of first arguments also completes that argument: `/mcp lo` offers `login`,
// and Tab rewrites the token in place. Enter and the configured submit keys
// still send the draft exactly as typed.

// slashArgumentChoices is Rust's `choices(SlashCommand)` table: commands whose
// first argument is a finite list.
func slashArgumentChoices(command string) []string {
	switch strings.ToLower(strings.TrimSpace(command)) {
	case "raw":
		return []string{"on", "off"}
	case "ide":
		return []string{"on", "off", "status"}
	case "usage":
		return []string{"daily", "weekly", "cumulative"}
	case "voice":
		return []string{"settings", "mute", "stop"}
	case "keymap":
		return []string{"debug"}
	case "goal":
		return []string{"clear", "edit", "pause", "resume"}
	case "mcp":
		return []string{"verbose", "login"}
	case "pets":
		return []string{"off"}
	}
	return nil
}

// slashArgumentTarget is the first-argument token the popup completes.
type slashArgumentTarget struct {
	Command string
	Prefix  string
	// Start and End bound the token inside the composer text in bytes.
	Start int
	End   int
}

// slashArgumentTargetFor resolves a single-line draft of the form
// `/command <prefix>` into the token Tab rewrites. A second argument, a
// multiline draft, an unknown command or a command the current thread cannot
// run yields no target (Rust slash_argument_target).
func (m *Model) slashArgumentTargetFor() (slashArgumentTarget, bool) {
	if m == nil {
		return slashArgumentTarget{}, false
	}
	text := m.composerText()
	if !strings.HasPrefix(text, "/") || strings.ContainsAny(text, "\n\r") {
		return slashArgumentTarget{}, false
	}
	rest, ok := strings.CutPrefix(text, "/")
	if !ok || rest == "" {
		return slashArgumentTarget{}, false
	}
	name, argument, found := strings.Cut(rest, " ")
	if !found || name == "" {
		return slashArgumentTarget{}, false
	}
	// Only the first argument completes; a following token closes the popup.
	if strings.ContainsAny(argument, " \t") {
		return slashArgumentTarget{}, false
	}
	if len(slashArgumentChoices(name)) == 0 {
		return slashArgumentTarget{}, false
	}
	if !m.slashArgumentCommandAvailable(name) {
		return slashArgumentTarget{}, false
	}
	start := 1 + len(name) + 1
	return slashArgumentTarget{Command: name, Prefix: argument, Start: start, End: start + len(argument)}, true
}

// slashArgumentCommandAvailable reports whether the command is in the current
// catalog and can run here (Rust limits suggestions to builtins available for
// the thread and connection state).
func (m *Model) slashArgumentCommandAvailable(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	for _, item := range m.slashPopupCatalog() {
		if strings.EqualFold(strings.TrimSpace(item.Name), name) {
			return !item.Unavailable
		}
	}
	return false
}

// refreshSlashArgumentPopup replaces the command menu with the argument
// suggestions for the current draft. Restricted input keeps the command menu
// closed instead (Rust returns no popup while disconnected or in draft
// handoff).
func (m *Model) refreshSlashArgumentPopup() bool {
	target, ok := m.slashArgumentTargetFor()
	if !ok || m.threadUnavailable || m.appServerDisconnected {
		return false
	}
	prefix := strings.ToLower(target.Prefix)
	items := make([]slashCommandPopupItem, 0, 4)
	for _, choice := range slashArgumentChoices(target.Command) {
		if prefix != "" && !strings.HasPrefix(strings.ToLower(choice), prefix) {
			continue
		}
		items = append(items, slashCommandPopupItem{Name: choice})
	}
	if len(items) == 0 {
		m.slashPopup = slashCommandPopup{}
		return true
	}
	selected := 0
	if m.slashPopup.Argument && strings.EqualFold(m.slashPopup.ArgumentCommand, target.Command) {
		if previous := m.slashPopup.Selected; previous >= 0 && previous < len(items) {
			selected = previous
		}
	}
	m.slashPopup = slashCommandPopup{
		Active:          true,
		Query:           target.Prefix,
		Items:           items,
		Selected:        selected,
		Argument:        true,
		ArgumentCommand: target.Command,
		ArgumentStart:   target.Start,
		ArgumentEnd:     target.End,
	}
	return true
}

// completeSlashArgument rewrites the live argument token with the selected
// suggestion. The token is re-validated first, so a draft edited after the
// popup was built is left alone (Rust Tab revalidates the live token).
func (m *Model) completeSlashArgument() bubbletea.Cmd {
	if m == nil || !m.slashPopup.Argument {
		return nil
	}
	item, ok := m.currentSlashPopupItem()
	if !ok {
		return nil
	}
	text := m.composerText()
	start, end := m.slashPopup.ArgumentStart, m.slashPopup.ArgumentEnd
	if start < 0 || end < start || end > len(text) || !strings.EqualFold(text[start:end], m.slashPopup.Query) {
		m.slashPopup = slashCommandPopup{}
		return nil
	}
	m.setComposerPrompt(text[:start] + item.Name + text[end:])
	m.slashPopup = slashCommandPopup{}
	return nil
}
