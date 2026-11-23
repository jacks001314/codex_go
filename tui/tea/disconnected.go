package tea

// Rust parity: codex-rs/tui/src/bottom_pane/chat_composer/reconnect.rs and
// app/event_dispatch.rs (#63258).
//
// A failed app-server transport leaves the conversation on screen. Local view
// commands keep working, and the slash popup explains which commands need the
// server instead of hiding them.

import (
	"strings"

	codextui "codex_go/tui"
)

// setAppServerDisconnected enters or leaves the offline TUI state after the
// app-server transport failed or a fresh connection replaced it.
func (m *Model) setAppServerDisconnected(disconnected bool) {
	if m == nil {
		return
	}
	wasDisconnected := m.appServerDisconnected
	m.appServerDisconnected = disconnected
	if wasDisconnected == disconnected {
		return
	}
	// The command popup caches its catalog, so a connection change rebuilds it
	// while keeping the draft.
	m.slashPopup = slashCommandPopup{}
	if disconnected {
		m.refreshSlashPopup()
	}
}

// firstLineOfText returns everything before the first newline.
func firstLineOfText(text string) string {
	if index := strings.IndexByte(text, '\n'); index >= 0 {
		return text[:index]
	}
	return text
}

// disconnectedSlashCommand reports whether the draft is a command the TUI can
// run without its app server. The command must occupy one line and be complete,
// mirroring Rust's bare_command/inline_command matching.
func disconnectedSlashCommand(input string) bool {
	raw := input
	input = strings.TrimSpace(input)
	if input == "" || raw != input || strings.Contains(input, "\n") {
		return false
	}
	invocation, ok := codextui.ParseCommand(input)
	if !ok || !slashInvocationDispatchable(invocation) {
		return false
	}
	return invocation.Command.AvailableWhenDisconnected()
}

// offlineFeedbackMessage reports the thread identity and the reconnect guidance
// /feedback shows while the server is unreachable.
func (m *Model) offlineFeedbackMessage() (string, string) {
	thread := "Thread ID unavailable."
	if m != nil {
		if id := strings.TrimSpace(m.State.ThreadID); id != "" {
			thread = "Thread ID " + id
		}
	}
	return thread, "Reconnect to send feedback or save a ZIP."
}

// noteOfflineConnectionRequirement explains a command the offline TUI cannot run
// and keeps the draft intact.
func (m *Model) noteOfflineConnectionRequirement(command string) {
	if m == nil {
		return
	}
	name := strings.TrimSpace(command)
	if name == "" {
		name = "That command"
	} else if !strings.HasPrefix(name, "/") {
		name = "/" + name
	}
	m.notice = name + " requires a server connection."
	m.refreshTranscript()
}
