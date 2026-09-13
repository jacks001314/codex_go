package tea

import (
	"strings"
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/appserver"
	codextui "codex_go/tui"
	tuiapp "codex_go/tui/app"
	historycell "codex_go/tui/history_cell"
)

// RecapTrigger distinguishes a scheduled recap from an explicit `/recap`
// (Rust RecapTrigger).
type RecapTrigger int

const (
	// RecapTriggerAutomatic is a scheduled recap for an unfocused conversation.
	RecapTriggerAutomatic RecapTrigger = iota
	// RecapTriggerManual is an explicit `/recap`.
	RecapTriggerManual
)

// RecapThreadOptions carries the visible thread's identity into the temporary
// structured recap thread (Rust TemporaryStructuredThreadOptions).
type RecapThreadOptions struct {
	Model             string
	ModelProvider     string
	CWD               string
	PermissionProfile string
}

// RecapGenerateFunc runs the temporary structured recap turn and returns the
// model's raw JSON response (Rust temporary_structured_request.rs). A nil hook
// leaves /recap unavailable for this runtime.
type RecapGenerateFunc func(threadID string, options RecapThreadOptions, prompt string, schema map[string]any) (string, error)

// RecapGeneratedMsg reports the outcome of a recap generation request.
type RecapGeneratedMsg struct {
	ThreadID       string
	Response       string
	Err            error
	Trigger        RecapTrigger
	TurnRevision   int
	CompletedTurns int
}

// recapCheckMsg fires when the automatic recap deadline elapses.
type recapCheckMsg struct{}

// applyRecapCommand implements the manual `/recap` flow (Rust
// request_recap with RecapTrigger::Manual).
func (m *Model) applyRecapCommand() bubbletea.Cmd {
	if m == nil || m.State == nil {
		return nil
	}
	if m.recapInFlight {
		m.addErrorHistoryMessage(tuiapp.ManualRecapInProgressMessage)
		m.refreshTranscript()
		return nil
	}
	history := tuiapp.RecapHistory(m.State.Messages)
	if strings.TrimSpace(history) == "" {
		m.addErrorHistoryMessage(tuiapp.ManualRecapEmptyHistoryMessage)
		m.refreshTranscript()
		return nil
	}
	if m.onGenerateRecap == nil {
		m.addErrorHistoryMessage(tuiapp.ManualRecapFailureMessage)
		m.refreshTranscript()
		return nil
	}
	m.showRecapLoading()
	return m.startRecapRequest(RecapTriggerManual, history)
}

// startRecapRequest launches the temporary structured recap turn.
func (m *Model) startRecapRequest(trigger RecapTrigger, history string) bubbletea.Cmd {
	m.recapInFlight = true
	threadID := m.currentThreadID()
	options := RecapThreadOptions{
		Model:             strings.TrimSpace(m.State.Model),
		ModelProvider:     strings.TrimSpace(m.State.Provider),
		CWD:               strings.TrimSpace(m.State.CWD),
		PermissionProfile: strings.TrimSpace(m.pendingServerProfile),
	}
	prompt := tuiapp.RecapPrompt(history)
	schema := tuiapp.RecapOutputSchema()
	generate := m.onGenerateRecap
	turnRevision := m.recap.TurnRevision
	completedTurns := m.recap.CompletedTurns
	return func() bubbletea.Msg {
		response, err := generate(threadID, options, prompt, schema)
		return RecapGeneratedMsg{
			ThreadID:       threadID,
			Response:       response,
			Err:            err,
			Trigger:        trigger,
			TurnRevision:   turnRevision,
			CompletedTurns: completedTurns,
		}
	}
}

// applyRecapGeneratedMsg renders the generated recap or reports the manual
// failure message (Rust handle_generated_recap).
func (m *Model) applyRecapGeneratedMsg(msg RecapGeneratedMsg) bubbletea.Cmd {
	if m == nil || !m.recapInFlight {
		return nil
	}
	m.recapInFlight = false
	if msg.Trigger == RecapTriggerManual {
		m.clearRecapLoading()
	}
	if strings.TrimSpace(msg.ThreadID) != m.currentThreadID() {
		return nil
	}
	if msg.TurnRevision != m.recap.TurnRevision || msg.CompletedTurns != m.recap.CompletedTurns {
		return nil
	}
	if msg.Err != nil {
		return m.failRecap(msg.Trigger, msg.TurnRevision)
	}
	summary, nextAction, ok := tuiapp.ParseRecap(msg.Response)
	if !ok {
		return m.failRecap(msg.Trigger, msg.TurnRevision)
	}
	m.recap.MarkRecapped(msg.CompletedTurns)
	m.applyHistoryCell(historycell.NewThreadRecapHistoryCell(summary).WithNextAction(nextAction))
	return nil
}

// failRecap reports a manual failure or schedules one automatic retry per turn
// revision (Rust retry_or_report_recap_failure).
func (m *Model) failRecap(trigger RecapTrigger, turnRevision int) bubbletea.Cmd {
	if trigger == RecapTriggerManual {
		m.addErrorHistoryMessage(tuiapp.ManualRecapFailureMessage)
		m.refreshTranscript()
		return nil
	}
	if m.disableAutoRecap || !m.recap.BeginRetry(turnRevision) {
		return nil
	}
	return bubbletea.Tick(tuiapp.RecapRetryDelay, func(time.Time) bubbletea.Msg { return recapCheckMsg{} })
}

// applyRecapCheck starts an automatic recap when the deadline has elapsed (Rust
// AppEvent::CheckRecap).
func (m *Model) applyRecapCheck(now time.Time) bubbletea.Cmd {
	if m == nil || m.disableAutoRecap || m.recapInFlight || m.onGenerateRecap == nil {
		return nil
	}
	if !m.recap.ShouldGenerate(now) {
		return nil
	}
	history := tuiapp.RecapHistory(m.State.Messages)
	if strings.TrimSpace(history) == "" {
		return nil
	}
	return m.startRecapRequest(RecapTriggerAutomatic, history)
}

// noteRecapTurnFinished records a terminal turn and reschedules the automatic
// recap check (Rust note_turn_finished + schedule_recap_check).
func (m *Model) noteRecapTurnFinished(status appserver.TurnStatus) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	m.recap.NoteTurnFinished(status, m.currentTime())
	return m.scheduleRecapCheck()
}

// noteRecapFocusLost anchors the unfocused recap window (Rust
// TerminalFocusLost + schedule_recap_check).
func (m *Model) noteRecapFocusLost() bubbletea.Cmd {
	if m == nil {
		return nil
	}
	m.recap.NoteFocusLost(m.currentTime())
	return m.scheduleRecapCheck()
}

// noteRecapFocusGained clears the unfocused window so pending checks are inert
// (Rust TerminalFocusGained).
func (m *Model) noteRecapFocusGained() {
	if m == nil {
		return
	}
	m.recap.NoteFocusGained()
}

// seedRecapProgress resets and seeds the recap accounting for an attached
// thread (Rust seed_from_turns on resume/replacement).
func (m *Model) seedRecapProgress(completedTurns int) {
	if m == nil {
		return
	}
	now := m.currentTime()
	m.recap.ResetForNewThread(now)
	m.recap.SeedFromProgress(tuiapp.RecapProgress{CompletedTurns: completedTurns}, now)
}

// scheduleRecapCheck schedules the automatic recap deadline, if any (Rust
// schedule_recap_check). A stale tick is inert because it re-checks the
// deadline when it fires.
func (m *Model) scheduleRecapCheck() bubbletea.Cmd {
	if m == nil || m.disableAutoRecap {
		return nil
	}
	deadline, ok := m.recap.NextCheckDeadline()
	if !ok {
		return nil
	}
	delay := time.Until(deadline)
	if delay < 0 {
		delay = 0
	}
	return bubbletea.Tick(delay, func(time.Time) bubbletea.Msg { return recapCheckMsg{} })
}

// showRecapLoading appends the transient loading row and remembers its index so
// a later clear can remove it (Rust show_recap_loading).
func (m *Model) showRecapLoading() {
	if m == nil || m.State == nil {
		return
	}
	width := m.width
	if width < 20 {
		width = 20
	}
	cell := historycell.NewThreadRecapLoadingCell()
	m.recapLoadingIndex = len(m.State.Messages)
	m.State.AddHistoryLines(cell.DisplayLines(width), cell.RawLines())
	m.refreshTranscript()
}

// clearRecapLoading removes the loading row if it is still present (Rust
// clear_recap_loading).
func (m *Model) clearRecapLoading() {
	if m == nil || m.State == nil || m.recapLoadingIndex < 0 {
		return
	}
	index := m.recapLoadingIndex
	m.recapLoadingIndex = -1
	if index >= len(m.State.Messages) || m.State.Messages[index].Role != codextui.RoleHistory {
		return
	}
	if !strings.Contains(m.State.Messages[index].Text, "Generating conversation recap") {
		return
	}
	m.State.Messages = append(m.State.Messages[:index], m.State.Messages[index+1:]...)
	m.State.BumpMessagesRevision()
	m.refreshTranscript()
}
