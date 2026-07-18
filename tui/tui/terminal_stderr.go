package tui

import "errors"

// Rust parity subset: codex-rs/tui/src/tui/terminal_stderr.rs.

var ErrTerminalStderrAlreadyActive = errors.New("terminal stderr suppression is already active")

type TerminalStderrLine struct {
	Text string
}

type TerminalStderrState struct {
	OwnerActive bool
	Suppressed  bool
}

type TerminalStderrGuard struct {
	Active bool
	state  *TerminalStderrState
}

func InstallTerminalStderrGuard(state *TerminalStderrState, shouldSuppress bool) (TerminalStderrGuard, error) {
	if state == nil {
		state = &TerminalStderrState{}
	}
	if !shouldSuppress {
		return TerminalStderrGuard{state: state}, nil
	}
	if state.OwnerActive {
		return TerminalStderrGuard{}, ErrTerminalStderrAlreadyActive
	}
	state.OwnerActive = true
	state.Suppressed = true
	return TerminalStderrGuard{Active: true, state: state}, nil
}

func (g *TerminalStderrGuard) Close() {
	if g == nil || !g.Active || g.state == nil {
		return
	}
	FinishTerminalStderr(g.state)
	g.Active = false
}

func PauseTerminalStderr(state *TerminalStderrState) {
	if state != nil && state.OwnerActive {
		state.Suppressed = false
	}
}

func ResumeTerminalStderr(state *TerminalStderrState) {
	if state != nil && state.OwnerActive {
		state.Suppressed = true
	}
}

func FinishTerminalStderr(state *TerminalStderrState) {
	if state != nil && state.OwnerActive {
		state.Suppressed = false
		state.OwnerActive = false
	}
}
