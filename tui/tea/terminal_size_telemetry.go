package tea

import (
	"strings"
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/protocol"
	codextui "codex_go/tui"
)

// Rust parity: codex-rs/tui/src/app/startup.rs and app/thread_routing.rs
// (#53859). A new turn in the active thread observes the terminal size, and a
// resize is observed once the terminal has stayed at the new geometry for 15
// seconds. Both observations reuse the launch context captured by
// `codex.tui.start` and only record when the buckets changed.

// terminalSizeSettleDefault mirrors Rust's 15-second settle window.
const terminalSizeSettleDefault = 15 * time.Second

type terminalSizePendingSettle struct {
	width    int
	height   int
	deadline time.Time
}

// terminalSizeSettleMsg fires when a pending resize has stayed stable for the
// settle window.
type terminalSizeSettleMsg struct {
	generation uint64
}

func cloneStringMapTea(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func terminalSizeTagPair(width int, height int) (string, string) {
	tags := codextui.TerminalSizeTags(width, height)
	return tags[codextui.TerminalColumnsTag], tags[codextui.TerminalRowsTag]
}

func (m *Model) terminalSizeSettleWindow() time.Duration {
	if m != nil && m.terminalSizeSettleDelay > 0 {
		return m.terminalSizeSettleDelay
	}
	return terminalSizeSettleDefault
}

// terminalSizeTelemetryEnabled mirrors Rust's
// `enabled(config) && !terminal_size_tags.is_empty()` gate.
func (m *Model) terminalSizeTelemetryEnabled() bool {
	if m == nil || len(m.terminalSizeContext) == 0 {
		return false
	}
	return codextui.TerminalSizeTelemetryEnabled(m.terminalSizeExporter, m.terminalSizeAnalyticsEnabled)
}

func (m *Model) terminalSizeAnalyticsConsent() bool {
	return m == nil || m.terminalSizeAnalyticsEnabled == nil || *m.terminalSizeAnalyticsEnabled
}

func (m *Model) terminalSizeGeometry() (int, int) {
	read := codextui.TerminalSizeRead
	if m != nil && m.terminalSizeReader != nil {
		read = m.terminalSizeReader
	}
	width, height := read()
	if width < 0 {
		width = 0
	}
	if height < 0 {
		height = 0
	}
	return width, height
}

func (m *Model) recordTerminalSize(reason string, width int, height int) {
	if m == nil {
		return
	}
	if m.terminalSizeRecorder != nil {
		m.terminalSizeRecorder(m.terminalSizeContext, m.terminalSizeAnalyticsConsent(), reason, width, height)
		return
	}
	codextui.RecordTerminalSize(m.terminalSizeContext, m.terminalSizeAnalyticsConsent(), reason, width, height)
}

// observeTerminalSizeForTelemetry mirrors the per-loop resize bookkeeping: a
// disabled observation clears the pending resize and re-seeds the settled
// buckets, while an enabled size change starts (or restarts) the settle window.
func (m *Model) observeTerminalSizeForTelemetry() {
	if m == nil || !m.terminalSizeInitialized {
		return
	}
	changed := m.width != m.terminalSizeLastWidth || m.height != m.terminalSizeLastHeight
	if !m.terminalSizeTelemetryEnabled() {
		m.terminalSizePending = nil
		m.terminalSizeSettledColumns, m.terminalSizeSettledRows = terminalSizeTagPair(m.width, m.height)
	} else if changed {
		m.terminalSizeGeneration++
		m.terminalSizePending = &terminalSizePendingSettle{
			width:    m.width,
			height:   m.height,
			deadline: time.Now().Add(m.terminalSizeSettleWindow()),
		}
	}
	m.terminalSizeLastWidth = m.width
	m.terminalSizeLastHeight = m.height
}

// observeTerminalSizeTurnStarted mirrors Rust's thread routing: record a new
// turn in the active thread once, using the turn id to suppress duplicates.
func (m *Model) observeTerminalSizeTurnStarted(event protocol.ThreadEvent) {
	if m == nil || !m.terminalSizeTelemetryEnabled() {
		return
	}
	activeThreadID := ""
	if m.State != nil {
		activeThreadID = strings.TrimSpace(m.State.ThreadID)
	}
	eventThreadID := strings.TrimSpace(event.ThreadID)
	if activeThreadID != "" && eventThreadID != "" && eventThreadID != activeThreadID {
		return
	}
	if activeThreadID == "" && eventThreadID == "" {
		return
	}
	turnID := strings.TrimSpace(event.TurnID)
	if turnID != "" && turnID == m.terminalSizeLastTurnID {
		return
	}
	if turnID != "" {
		m.terminalSizeLastTurnID = turnID
	}
	width, height := m.terminalSizeGeometry()
	m.recordTerminalSize("turn_start", width, height)
}

// terminalSizeSettleCmd schedules the one pending settle check. Repeated
// updates before it fires do not schedule another timer.
func (m *Model) terminalSizeSettleCmd() bubbletea.Cmd {
	if m == nil || m.terminalSizePending == nil || m.terminalSizeScheduled == m.terminalSizeGeneration {
		return nil
	}
	m.terminalSizeScheduled = m.terminalSizeGeneration
	generation := m.terminalSizeGeneration
	delay := time.Until(m.terminalSizePending.deadline)
	if delay < 0 {
		delay = 0
	}
	return bubbletea.Tick(delay, func(time.Time) bubbletea.Msg {
		return terminalSizeSettleMsg{generation: generation}
	})
}

// applyTerminalSizeSettle confirms the terminal is still at the pending
// geometry, then records a resize observation only when the buckets changed.
func (m *Model) applyTerminalSizeSettle(generation uint64) bubbletea.Cmd {
	if m == nil || m.terminalSizePending == nil || generation != m.terminalSizeGeneration {
		return nil
	}
	if !m.terminalSizeTelemetryEnabled() {
		m.terminalSizePending = nil
		return nil
	}
	expectedWidth, expectedHeight := m.terminalSizePending.width, m.terminalSizePending.height
	width, height := m.terminalSizeGeometry()
	if width != expectedWidth || height != expectedHeight {
		m.terminalSizeGeneration++
		m.terminalSizePending = &terminalSizePendingSettle{
			width:    width,
			height:   height,
			deadline: time.Now().Add(m.terminalSizeSettleWindow()),
		}
		return m.terminalSizeSettleCmd()
	}
	columns, rows := terminalSizeTagPair(width, height)
	if columns != m.terminalSizeSettledColumns || rows != m.terminalSizeSettledRows {
		m.recordTerminalSize("resize", width, height)
	}
	m.terminalSizeSettledColumns, m.terminalSizeSettledRows = columns, rows
	m.terminalSizePending = nil
	return nil
}
