package tea

import (
	"math/rand"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
)

// This file ports Rust's `tui/src/app/turn_tips.rs`: a random tip shown beneath
// the working status once a foreground turn has run for the working delay, and
// (later) a spaced-out completion tip. The cadence rules are the ported part:
// completion tips start at the third turn start, are spaced by at least three
// turn starts, and at most two are shown per app session.

const (
	// turnTipWorkingDelay is Rust's `WORKING_DELAY`.
	turnTipWorkingDelay = 30 * time.Second
	// turnTipCompletionInterval is Rust's `COMPLETION_INTERVAL`.
	turnTipCompletionInterval = 3
	// turnTipCompletionLimit is Rust's `COMPLETION_LIMIT`.
	turnTipCompletionLimit = 2
)

// turnTipSurface mirrors Rust's `TipSurface`.
type turnTipSurface int

const (
	turnTipSurfaceWorking turnTipSurface = iota
	turnTipSurfaceCompletion
)

// turnTipPhase mirrors Rust's `Phase` for one foreground turn.
type turnTipPhase int

const (
	turnTipPhaseWorking turnTipPhase = iota
	turnTipPhaseFinished
)

// turnTipState mirrors Rust's `TurnTipState`.
type turnTipState struct {
	threadID       string
	turnID         string
	startedAt      time.Time
	phase          turnTipPhase
	hasFinalAnswer bool
	template       string
	shown          bool
}

// turnTips mirrors Rust's `TurnTips`.
type turnTips struct {
	starts           int
	completionsShown int
	nextCompletion   int
	previous         string
	current          *turnTipState
	rng              *rand.Rand
}

func (t *turnTips) ensureRNG() *rand.Rand {
	if t.rng == nil {
		t.rng = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	return t.rng
}

// dismiss mirrors Rust's `TurnTips::dismiss` (history replay or a transcript
// reset must not resurrect a tip).
func (t *turnTips) dismiss() {
	if t == nil {
		return
	}
	t.current = nil
}

// observeTurnStarted mirrors the `TurnStarted` arm of Rust's `TurnTips::observe`:
// a repeated start for the same thread and turn does not count again.
func (t *turnTips) observeTurnStarted(threadID string, turnID string, now time.Time) {
	if t == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	turnID = strings.TrimSpace(turnID)
	if current := t.current; current != nil && current.threadID == threadID && current.turnID == turnID {
		return
	}
	t.starts++
	t.current = &turnTipState{threadID: threadID, turnID: turnID, startedAt: now, phase: turnTipPhaseWorking}
}

// observeRetainedItem mirrors Rust's `ItemCompleted` arm: an assistant message
// with a final answer marks the turn eligible for a completion tip.
func (t *turnTips) observeRetainedItem(threadID string, turnID string, finalAnswer bool) {
	if t == nil {
		return
	}
	current := t.current
	if current == nil || current.threadID != strings.TrimSpace(threadID) || current.turnID != strings.TrimSpace(turnID) {
		return
	}
	current.hasFinalAnswer = current.hasFinalAnswer || finalAnswer
}

// observeTurnCompleted mirrors Rust's `TurnCompleted` arm: only a successful
// turn with a final answer becomes eligible, the cadence must allow it, and the
// working phase must not already have shown a tip for this turn.
func (t *turnTips) observeTurnCompleted(threadID string, turnID string, succeeded bool, finalAnswer bool) {
	if t == nil {
		return
	}
	current := t.current
	if current == nil || current.threadID != strings.TrimSpace(threadID) || current.turnID != strings.TrimSpace(turnID) ||
		current.phase != turnTipPhaseWorking {
		return
	}
	current.hasFinalAnswer = current.hasFinalAnswer || finalAnswer
	current.phase = turnTipPhaseFinished
	if !succeeded || !current.hasFinalAnswer || current.shown {
		return
	}
	if t.completionsShown >= turnTipCompletionLimit {
		return
	}
	if t.starts < turnTipCompletionInterval || t.starts < t.nextCompletion {
		return
	}
	// Rust enters a waiting-for-history phase and anchors the completion tip
	// after the queued history update; Go renders it with the transcript, so the
	// turn is only marked eligible here.
	current.phase = turnTipPhaseFinished
}

// workingTip mirrors the working arm of Rust's `App::turn_tip`: the tip is
// selected and rendered once the current turn has run for the working delay.
// The caller renders it and then acknowledges the surface.
func (t *turnTips) workingTip(width int, now time.Time, keymap *codextui.KeymapConfig) (string, bool) {
	if t == nil {
		return "", false
	}
	current := t.current
	if current == nil || current.phase != turnTipPhaseWorking {
		return "", false
	}
	if current.startedAt.IsZero() || now.Sub(current.startedAt) < turnTipWorkingDelay {
		return "", false
	}
	if current.template == "" {
		current.template = t.pickWorkingTemplate(width, keymap)
	}
	tip, ok := renderTurnTipLine(current.template, width, keymap)
	if !ok {
		return "", false
	}
	return tip, true
}

// acknowledge mirrors Rust's `TurnTips::acknowledge`: exposure is counted only
// when a tip was actually rendered, and the previous template is remembered so
// the next tip differs.
func (t *turnTips) acknowledge(surface turnTipSurface) {
	if t == nil {
		return
	}
	current := t.current
	if current == nil || current.shown {
		return
	}
	current.shown = true
	t.previous = current.template
	if surface == turnTipSurfaceCompletion {
		t.completionsShown++
		t.nextCompletion = t.starts + turnTipCompletionInterval
	}
}

// pickWorkingTemplate mirrors Rust's template selection: the catalog is
// shuffled, the previously shown template is skipped, and the first template
// that renders on one line inside the width wins.
func (t *turnTips) pickWorkingTemplate(width int, keymap *codextui.KeymapConfig) string {
	templates := codextui.TooltipTemplates()
	rng := t.ensureRNG()
	shuffled := append([]string(nil), templates...)
	rng.Shuffle(len(shuffled), func(i int, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
	for _, template := range shuffled {
		if template == t.previous {
			continue
		}
		if _, ok := renderTurnTipLine(template, width, keymap); ok {
			return template
		}
	}
	for _, template := range shuffled {
		if _, ok := renderTurnTipLine(template, width, keymap); ok {
			return template
		}
	}
	return ""
}

// renderTurnTipLine renders one template and keeps it only when it is a single
// line that fits the width (Rust's `render_tooltip_lines` single-line rule).
func renderTurnTipLine(template string, width int, keymap *codextui.KeymapConfig) (string, bool) {
	if strings.TrimSpace(template) == "" {
		return "", false
	}
	rendered, ok := codextui.RenderTooltip(template, keymap)
	if !ok {
		return "", false
	}
	rendered = strings.TrimSpace(rendered)
	if rendered == "" || strings.ContainsAny(rendered, "\r\n") {
		return "", false
	}
	if width > 0 && len([]rune(rendered)) > width {
		return "", false
	}
	return rendered, true
}

// turnTipsAllowed mirrors the gates Rust's `App::turn_tip` applies before a tip
// may be shown: the preference must be on, no modal or popup may be active, the
// composer must be empty, and the turn must still be running.
func (m *Model) turnTipsAllowed() bool {
	if m == nil || !m.showTooltips || !m.isTaskRunning() {
		return false
	}
	if m.modal != nil || m.overlay != nil {
		return false
	}
	if strings.TrimSpace(m.composer.Value()) != "" || len(m.composerElements) > 0 {
		return false
	}
	return true
}

// turnTipTickMsg re-renders the frame at the working tip's deadline.
type turnTipTickMsg struct{}

// turnTipDelayCmd mirrors Rust's `frame_requester.schedule_frame_in(remaining)`.
func turnTipDelayCmd(delay time.Duration) tea.Cmd {
	return tea.Tick(delay, func(time.Time) tea.Msg { return turnTipTickMsg{} })
}
