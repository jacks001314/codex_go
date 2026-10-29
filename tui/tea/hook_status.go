package tea

import (
	"strings"
	"time"
	"unicode"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
)

const hookStatusRevealDelay = 300 * time.Millisecond

type hookStatusTickMsg struct{}

func (m *Model) markHookRunReveal(id string, now time.Time) {
	if m == nil {
		return
	}
	if m.hookRunDeadlines == nil {
		m.hookRunDeadlines = map[string]time.Time{}
	}
	m.hookRunDeadlines[id] = now.Add(hookStatusRevealDelay)
}

func (m *Model) syncHookRunDeadlines() {
	if m == nil {
		return
	}
	active := map[string]bool{}
	for _, run := range m.hookLifecycle.ActiveRuns {
		active[run.ID] = true
	}
	for id := range m.hookRunDeadlines {
		if !active[id] {
			delete(m.hookRunDeadlines, id)
		}
	}
}

// runningHookSummary mirrors HookCell::running_status_summary: only revealed
// runs contribute, identical messages collapse to one, and mixed messages use a
// shared "Running hooks" label.
func (m *Model) runningHookSummary(now time.Time) string {
	if m == nil {
		return ""
	}
	messages := []string{}
	for _, run := range m.hookLifecycle.ActiveRuns {
		deadline, ok := m.hookRunDeadlines[run.ID]
		if !ok || now.Before(deadline) {
			continue
		}
		messages = append(messages, strings.TrimSpace(run.Issue))
	}
	switch len(messages) {
	case 0:
		return ""
	case 1:
		if messages[0] == "" {
			return "Running hook"
		}
		return capitalizeHookSummary(messages[0])
	}
	first := messages[0]
	if first != "" {
		allSame := true
		for _, message := range messages[1:] {
			if message != first {
				allSame = false
				break
			}
		}
		if allSame {
			return capitalizeHookSummary(first)
		}
	}
	return "Running hooks"
}

func (m *Model) renderHookStatus(now time.Time) string {
	summary := m.runningHookSummary(now)
	if summary == "" {
		return ""
	}
	return "\u2022 " + summary
}

// renderHookSummaryText folds hook activity into the working row after the
// reveal delay, using animation-gated shimmer (Rust HookStatus).
func (m *Model) renderHookSummaryText(summary string, now time.Time) string {
	if m == nil || summary == "" {
		return ""
	}
	motion := codextui.MotionModeFromAnimationsEnabled(m.animationsEnabled)
	var elapsed time.Duration
	if !m.taskStartedAt.IsZero() {
		elapsed = now.Sub(m.taskStartedAt)
	}
	spans := codextui.SummaryShimmer(summary, elapsed, motion)
	reset := "\x1b[0m"
	dim := "\x1b[2m"
	if m.Styles.ExecCell.Reset != "" {
		reset = m.Styles.ExecCell.Reset
	}
	if m.Styles.Chat.DimText != "" {
		dim = m.Styles.Chat.DimText
	}
	var out strings.Builder
	for _, span := range spans {
		switch span.Style {
		case codextui.SummaryShimmerDim:
			out.WriteString(dim)
			out.WriteString(span.Text)
			out.WriteString(reset)
		case codextui.SummaryShimmerColor:
			out.WriteString(summaryShimmerForeground(span.Foreground))
			out.WriteString(span.Text)
			out.WriteString(reset)
		default:
			out.WriteString(span.Text)
		}
	}
	return out.String()
}

func (m *Model) ensureHookStatusTick(now time.Time) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	var next time.Time
	for _, run := range m.hookLifecycle.ActiveRuns {
		deadline, ok := m.hookRunDeadlines[run.ID]
		if !ok || !now.Before(deadline) {
			continue
		}
		if next.IsZero() || deadline.Before(next) {
			next = deadline
		}
	}
	if next.IsZero() {
		return nil
	}
	return bubbletea.Tick(next.Sub(now), func(time.Time) bubbletea.Msg {
		return hookStatusTickMsg{}
	})
}

func capitalizeHookSummary(message string) string {
	runes := []rune(strings.TrimSpace(message))
	if len(runes) == 0 {
		return ""
	}
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}
