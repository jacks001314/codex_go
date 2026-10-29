package tea

import (
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/tui/analytics"
)

// PlanHistoryResultMsg carries one validated plan history snapshot back to the
// model (Rust #45766).
type PlanHistoryResultMsg struct {
	RequestID uint64
	Report    *analytics.Report
	Err       error
}

// openPlanHistory renders /usage plan behind the analytics_plan_history
// feature gate. Unknown usage stays unknown; an unavailable endpoint renders
// the "unavailable" line instead of failing the whole command.
func (m *Model) openPlanHistory() bubbletea.Cmd {
	if m == nil {
		return nil
	}
	if !m.planHistoryEnabled {
		m.notice = "Plan usage history is not enabled."
		m.State.AddHistoryLines([]string{m.notice}, []string{m.notice})
		m.refreshTranscript()
		return nil
	}
	width := m.width
	if width < 20 {
		width = 20
	}
	if m.onReadPlanHistory == nil {
		lines := analytics.RenderPlanHistory(nil, width)
		m.State.AddHistoryLines(lines, lines)
		m.notice = "Usage"
		m.refreshTranscript()
		return nil
	}
	loading := []string{"/usage plan", "Loading plan usage history..."}
	m.State.AddHistoryLines(loading, loading)
	m.notice = "Usage"
	m.refreshTranscript()
	requestID := m.nextUsageRequest()
	m.pendingPlanHistoryRequestID = requestID
	reader := m.onReadPlanHistory
	return func() bubbletea.Msg {
		report, err := reader()
		return PlanHistoryResultMsg{RequestID: requestID, Report: report, Err: err}
	}
}

func (m *Model) applyPlanHistoryResult(msg PlanHistoryResultMsg) {
	if m == nil || m.pendingPlanHistoryRequestID != msg.RequestID {
		return
	}
	m.pendingPlanHistoryRequestID = 0
	width := m.width
	if width < 20 {
		width = 20
	}
	report := msg.Report
	if msg.Err != nil {
		report = nil
	}
	lines := append([]string{"/usage plan"}, analytics.RenderPlanHistory(report, width)...)
	m.State.AddHistoryLines(lines, lines)
	m.notice = "Usage"
	m.refreshTranscript()
}

// planHistoryArgs reports whether the /usage argument selects the plan panel.
func planHistoryArgs(args string) bool {
	return strings.EqualFold(strings.TrimSpace(args), "plan")
}
