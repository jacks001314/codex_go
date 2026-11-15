package tea

import (
	"io"

	codextui "codex_go/tui"
)

const (
	disableAlternateScrollSequence = "\x1b[?1007l"
	enableAlternateScrollSequence  = "\x1b[?1007h"
)

// alternateScrollState mirrors Rust's AlternateScreen scroll bookkeeping
// (#61697): initialEnabled is the mode recorded by the startup probe, and dirty
// means this TUI changed it and must restore the initial value on handoff or
// exit. A failed restore keeps dirty set so the next cleanup attempt retries.
type alternateScrollState struct {
	initialEnabled bool
	dirty          bool
	managed        bool
	enabled        bool
	written        bool
}

func newAlternateScrollState() alternateScrollState {
	return alternateScrollState{initialEnabled: codextui.InitialAlternateScrollEnabled()}
}

func (m *Model) writeAlternateScrollMode(enabled bool) bool {
	if m == nil || m.petRuntime == nil || m.petRuntime.out == nil {
		return false
	}
	sequence := disableAlternateScrollSequence
	if enabled {
		sequence = enableAlternateScrollSequence
	}
	m.petRuntime.mu.Lock()
	defer m.petRuntime.mu.Unlock()
	_, err := io.WriteString(m.petRuntime.out, sequence)
	return err == nil
}

// configureAlternateScroll applies a managed alternate-scroll mode. The dirty
// flag stays set after a successful write because the terminal is now in a TUI
// state that release/exit must restore.
func (m *Model) configureAlternateScroll(enabled bool) {
	if m == nil {
		return
	}
	if m.alternateScroll.managed && m.alternateScroll.enabled == enabled && m.alternateScroll.written {
		return
	}
	m.alternateScroll.managed = true
	m.alternateScroll.enabled = enabled
	m.alternateScroll.dirty = true
	m.alternateScroll.written = m.writeAlternateScrollMode(enabled)
}

// restoreAlternateScroll puts the terminal back in the mode recorded at
// startup, defaulting to disabled when the probe did not observe it. It reports
// whether the write succeeded so a failed cleanup can retry later.
func (m *Model) restoreAlternateScroll() bool {
	if m == nil || !m.alternateScroll.dirty {
		return true
	}
	if !m.writeAlternateScrollMode(m.alternateScroll.initialEnabled) {
		return false
	}
	m.alternateScroll.dirty = false
	m.alternateScroll.managed = false
	m.alternateScroll.written = false
	return true
}

// alternateScrollForSurface returns the mode the active surface needs and
// whether this TUI owns the mode. Inline launches leave the terminal's policy
// alone unless a temporary pager needs the wheel.
func (m *Model) alternateScrollForSurface() (bool, bool) {
	if m == nil {
		return false, false
	}
	if m.overlay != nil {
		return !m.overlayMouseCapture.captures(m.ownedScreen), true
	}
	if m.ownedScreen {
		return false, true
	}
	return false, false
}

// configureAlternateScrollForSurface reapplies input modes after a terminal
// handoff, or restores the terminal policy when the current surface does not
// need alternate scroll.
func (m *Model) configureAlternateScrollForSurface() {
	if m == nil {
		return
	}
	if enabled, manage := m.alternateScrollForSurface(); manage {
		m.configureAlternateScroll(enabled)
		return
	}
	m.restoreAlternateScroll()
}
