package tui

import (
	"os"
	"strings"
	"sync"

	"codex_go/shell"
)

// Rust parity: codex-rs/tui/src/terminal_program_status.rs (through #54114).
//
// OSC 7501 reports lifecycle state and blocking reasons to terminal sessions.
// Direct iTerm2 sessions also receive OSC 21337 status, including bounded
// activity text. The process-wide cache is used for exit, panic, and terminal
// handoff cleanup.

// ProgramStatusKind classifies why a running agent is blocked.
type ProgramStatusKind string

const (
	ProgramStatusKindPermission ProgramStatusKind = "permission"
	ProgramStatusKindQuestion   ProgramStatusKind = "question"
	ProgramStatusKindAuth       ProgramStatusKind = "auth"
)

// ProgramStatusState is the coarse lifecycle state reported by OSC 7501.
type ProgramStatusState int

const (
	ProgramStatusIdle ProgramStatusState = iota
	ProgramStatusWorking
	ProgramStatusBlocked
	ProgramStatusDone
	ProgramStatusError
)

// ProgramStatus pairs the lifecycle state with the reason a blocked agent is
// waiting. The zero value is idle, matching an untouched session.
type ProgramStatus struct {
	State ProgramStatusState
	Kind  ProgramStatusKind
}

// BlockedProgramStatus reports the agent blocked on the given request kind.
func BlockedProgramStatus(kind ProgramStatusKind) ProgramStatus {
	return ProgramStatus{State: ProgramStatusBlocked, Kind: kind}
}

func (s ProgramStatus) String() string {
	return s.ProtocolState()
}

// ProtocolState is the OSC 7501 state token.
func (s ProgramStatus) ProtocolState() string {
	switch s.State {
	case ProgramStatusWorking:
		return "working"
	case ProgramStatusBlocked:
		return "blocked"
	case ProgramStatusDone:
		return "done"
	case ProgramStatusError:
		return "error"
	default:
		return "idle"
	}
}

// ITermLabel maps the status onto the legacy OSC 21337 label: completed and
// failed turns read as Idle there because that surface only shows liveness.
func (s ProgramStatus) ITermLabel() string {
	switch s.State {
	case ProgramStatusWorking:
		return "Working"
	case ProgramStatusBlocked:
		return "Waiting"
	default:
		return "Idle"
	}
}

// ITermIndicator is the OSC 21337 indicator color.
func (s ProgramStatus) ITermIndicator() string {
	switch s.State {
	case ProgramStatusWorking:
		return "#ff9500"
	case ProgramStatusBlocked:
		return "#5f87ff"
	default:
		return "#00d75f"
	}
}

type cachedProgramStatus struct {
	status ProgramStatus
	detail string
	// emitted distinguishes a cleared cache from a reported status.
	emitted bool
	// invalidated forces the next report to re-emit after another process owned
	// the terminal.
	invalidated bool
}

var sessionProgramStatusState = struct {
	mu     sync.Mutex
	cached cachedProgramStatus
}{}

// SetTerminalProgramStatus returns the sequence that reports status, or "" when
// the terminal does not support OSC 7501 or the same status/detail pair was
// already emitted. Callers write the returned sequence to the terminal.
func SetTerminalProgramStatus(status ProgramStatus, detail string) string {
	support := currentTerminalProgramStatusSupport()
	if !support.osc7501 {
		return ""
	}
	itermDetail := ""
	if support.osc21337 {
		itermDetail = sanitizeITermSessionDetail(detail)
	}
	sessionProgramStatusState.mu.Lock()
	defer sessionProgramStatusState.mu.Unlock()
	cached := &sessionProgramStatusState.cached
	if cached.emitted && !cached.invalidated && cached.status == status && cached.detail == itermDetail {
		return ""
	}
	cached.status = status
	cached.detail = itermDetail
	cached.emitted = true
	cached.invalidated = false
	return SetTerminalProgramStatusSequence(status, itermDetail, support.osc21337)
}

// ClearTerminalProgramStatus returns the sequence that clears the reported
// status, or "" when OSC 7501 is unsupported or nothing was reported.
func ClearTerminalProgramStatus() string {
	support := currentTerminalProgramStatusSupport()
	if !support.osc7501 {
		return ""
	}
	sessionProgramStatusState.mu.Lock()
	defer sessionProgramStatusState.mu.Unlock()
	cached := &sessionProgramStatusState.cached
	if !cached.emitted && !cached.invalidated {
		return ""
	}
	cached.emitted = false
	cached.invalidated = false
	cached.status = ProgramStatus{}
	cached.detail = ""
	return ClearTerminalProgramStatusSequence(support.osc21337)
}

// InvalidateTerminalProgramStatus forgets the cached state after another
// process owned the terminal, so the next active draw re-emits Codex's status
// even when the lifecycle state did not change during the handoff.
func InvalidateTerminalProgramStatus() {
	sessionProgramStatusState.mu.Lock()
	defer sessionProgramStatusState.mu.Unlock()
	cached := &sessionProgramStatusState.cached
	if cached.emitted || cached.invalidated {
		cached.emitted = false
		cached.invalidated = true
	}
}

// ResetTerminalProgramStatusCacheForTest restores the process cache between
// tests.
func ResetTerminalProgramStatusCacheForTest() {
	sessionProgramStatusState.mu.Lock()
	defer sessionProgramStatusState.mu.Unlock()
	sessionProgramStatusState.cached = cachedProgramStatus{}
}

// SetTerminalProgramStatusSequence renders the OSC 21337 (iTerm2) and OSC 7501
// sequences for status. detail must already be sanitized and escaped.
func SetTerminalProgramStatusSequence(status ProgramStatus, detail string, emitITermStatus bool) string {
	var out strings.Builder
	if emitITermStatus {
		out.WriteString("\x1b]21337;status=")
		out.WriteString(status.ITermLabel())
		out.WriteString(";indicator=")
		out.WriteString(status.ITermIndicator())
		out.WriteString(";status-color=;detail=")
		out.WriteString(detail)
		out.WriteString("\x07")
	}
	out.WriteString("\x1b]7501;state=")
	out.WriteString(status.ProtocolState())
	out.WriteString(":app=codex")
	if status.State == ProgramStatusBlocked {
		kind := status.Kind
		if kind == "" {
			kind = ProgramStatusKindQuestion
		}
		out.WriteString(":kind=")
		out.WriteString(string(kind))
	}
	out.WriteString("\x1b\\")
	return out.String()
}

// ClearTerminalProgramStatusSequence renders the clear sequence.
func ClearTerminalProgramStatusSequence(emitITermStatus bool) string {
	if emitITermStatus {
		return "\x1b]21337;status=;indicator=;status-color=;detail=\x07\x1b]7501;state=clear\x1b\\"
	}
	return "\x1b]7501;state=clear\x1b\\"
}

func sanitizeITermSessionDetail(detail string) string {
	detail = SanitizeTerminalTitleForEncoding(detail, TitleEncodingUnicode)
	var out strings.Builder
	out.Grow(len(detail))
	for _, r := range detail {
		if r == ';' || r == '\\' {
			out.WriteRune('\\')
		}
		out.WriteRune(r)
	}
	return out.String()
}

type terminalProgramStatusSupport struct {
	osc7501  bool
	osc21337 bool
}

var (
	terminalProgramStatusSupportOnce sync.Once
	terminalProgramStatusSupportVal  terminalProgramStatusSupport
	// terminalProgramStatusSupportForTest pins support in tests.
	terminalProgramStatusSupportForTest *terminalProgramStatusSupport
	// stdoutIsTerminal is injectable for tests.
	stdoutIsTerminal = func() bool {
		info, err := os.Stdout.Stat()
		return err == nil && info.Mode()&os.ModeCharDevice != 0
	}
)

func currentTerminalProgramStatusSupport() terminalProgramStatusSupport {
	if terminalProgramStatusSupportForTest != nil {
		return *terminalProgramStatusSupportForTest
	}
	terminalProgramStatusSupportOnce.Do(func() {
		terminalProgramStatusSupportVal = terminalProgramStatusSupportFor(
			shell.CurrentTerminalInfo(),
			stdoutIsTerminal(),
			os.Getenv("STY") != "",
		)
	})
	return terminalProgramStatusSupportVal
}

func terminalProgramStatusSupportFor(terminal *shell.TerminalInfo, stdoutIsTerm bool, screenActive bool) terminalProgramStatusSupport {
	osc21337 := stdoutIsTerm &&
		terminal != nil &&
		terminal.Name == shell.TerminalIterm2 &&
		terminal.Multiplexer == nil &&
		!screenActive
	return terminalProgramStatusSupport{osc7501: stdoutIsTerm, osc21337: osc21337}
}

// SetTerminalProgramStatusSupportForTest pins terminal protocol support in
// tests and returns a restore function.
func SetTerminalProgramStatusSupportForTest(osc7501, osc21337 bool) func() {
	previous := terminalProgramStatusSupportForTest
	terminalProgramStatusSupportForTest = &terminalProgramStatusSupport{osc7501: osc7501, osc21337: osc21337}
	ResetTerminalProgramStatusCacheForTest()
	return func() {
		terminalProgramStatusSupportForTest = previous
		ResetTerminalProgramStatusCacheForTest()
	}
}
