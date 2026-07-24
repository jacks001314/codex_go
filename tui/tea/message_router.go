package tea

import (
	bubbletea "github.com/charmbracelet/bubbletea"
)

// MessageRouter provides categorized message routing for the Update loop.
// It decomposes the single large Update() type-switch into focused handler
// methods, each responsible for one category of messages.
//
// This is a starting point for the architecture; the full Update() method
// currently still lives in model.go. As we migrate, more cases will move
// into the router's handler methods.

// routeKeyMsg handles keyboard input routing with priority:
//
//  1. Transcript overlay keys (if overlay active)
//  2. Ctrl+C dispatch (modal, running task, side, quit)
//  3. Modal keys (if modal active)
//  4. Windows sandbox guard
//  5. Skill popup keys
//  6. Slash popup keys
//  7. Global keymap (open_transcript, copy, toggle_raw_output)
//  8. Transcript navigation
//  9. Input history
//  10. Composer submission / queuing
func routeKeyMsg(m *Model, msg bubbletea.KeyMsg) bubbletea.Cmd {
	// Transcript overlay
	if m.overlay != nil {
		return m.updateTranscriptOverlayKey(msg)
	}

	// Ctrl+C dispatch
	if msg.Type == bubbletea.KeyCtrlC {
		if m.modal != nil && m.modal.sessionPicker != nil {
			m.respondModal(true)
			return nil
		}
		if m.isTaskRunning() {
			return m.interruptRunningTask()
		}
		if m.inSideConversation() {
			return m.returnFromSideConversation()
		}
		return bubbletea.Quit
	}

	// Ctrl+D quit
	if msg.Type == bubbletea.KeyCtrlD {
		return bubbletea.Quit
	}

	// Modal handling
	if m.modal != nil {
		return m.updateModal(msg)
	}

	// Windows sandbox guard
	if m.windowsSandboxSetupActive {
		return nil
	}

	// Skill popup
	if _, handled := m.updateSkillPopupKey(msg); handled {
		return nil
	}

	// Slash popup
	if _, handled := m.updateSlashPopupKey(msg); handled {
		return nil
	}

	// Global keymap
	keySpec := keySpecFromKeyMsg(msg)
	if m.keyMatches("global", "toggle_side_conversation", keySpec) ||
		(keySpec == "ctrl-7" && m.keyMatches("global", "toggle_side_conversation", "ctrl-/")) {
		return m.toggleSideConversation()
	}
	if m.keyMatches("root", "open_transcript", msg.String()) {
		m.openTranscriptOverlay()
		return nil
	}
	if m.keyMatches("root", "copy", msg.String()) {
		m.copyLastAgentResponse()
		return nil
	}
	if m.keyMatches("root", "toggle_raw_output", msg.String()) {
		m.toggleRawOutputMode()
		return nil
	}

	// Transcript navigation
	if m.applyTranscriptNavigationKey(msg) {
		return nil
	}

	// Input history
	if m.applyInputHistoryKey(msg) {
		return nil
	}

	// Composer submission (handled in Update via fallthrough to composer)
	return nil
}

// routeWindowMsg handles window resize and focus/blur events.
func routeWindowMsg(m *Model, msg bubbletea.Msg) {
	switch msg := msg.(type) {
	case bubbletea.WindowSizeMsg:
		m.resize(msg.Width, msg.Height)
	case bubbletea.FocusMsg:
		m.terminalFocused = true
	case bubbletea.BlurMsg:
		m.terminalFocused = false
	}
}
