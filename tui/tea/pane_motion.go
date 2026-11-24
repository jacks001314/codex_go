package tea

import (
	"math"
	"time"

	"github.com/charmbracelet/x/ansi"

	codextui "codex_go/tui"
)

// Rust parity: codex-rs/tui/src/app/pane_motion.rs (#59739). The sidebar slides
// from its last painted fraction to the new target, reversing when the target
// changes mid-slide. Without a frame requester the motion snaps, exactly like
// PaneMotion::advance(open, frames: None).

const (
	sidebarSlideDuration  = 180 * time.Millisecond
	sidebarAnimationFrame = 16 * time.Millisecond
)

type paneMotionState struct {
	fraction float64
	target   float64
	from     float64
	started  time.Time
	sliding  bool
}

// sideWorkspaceOpen reports whether the workspace asks for a side pane, which
// is the motion's target state.
func (m *Model) sideWorkspaceOpen() bool {
	// A zoomed document keeps the split and fills it (Rust #61889), so only a
	// conversation zoom closes the workspace.
	if m == nil || !m.sideWorkspaceEnabled() || m.transcriptUnavailableForPanes() ||
		(m.sideZoomed && !m.sidebarDocumentVisible()) {
		return false
	}
	return m.activeSide != nil || m.sidebarChooserVisible() || m.sidebarDocumentVisible()
}

// advancePaneMotion mirrors PaneMotion::advance: the eased fraction drives the
// sidebar width, and a live frame requester keeps requesting frames until the
// slide settles.
func (m *Model) advancePaneMotion(open bool, animate bool) float64 {
	return m.advancePaneMotionState(&m.paneMotion, open, animate)
}

// advancePaneMotionState runs PaneMotion::advance over one pane's state, so the
// sidebar and the docked Command Center slide independently.
func (m *Model) advancePaneMotionState(state *paneMotionState, open bool, animate bool) float64 {
	if m == nil || state == nil {
		return 0
	}
	target := 0.0
	if open {
		target = 1
	}
	now := m.currentTime()
	if state.target != target {
		state.target = target
		state.from = state.fraction
		state.started = now
		state.sliding = true
	}
	if state.sliding && animate {
		progress := now.Sub(state.started).Seconds() / sidebarSlideDuration.Seconds()
		if progress < 1 && state.from != target {
			eased := progress * progress * (3 - 2*progress)
			state.fraction = state.from + (target-state.from)*eased
			m.frames.requestAnim(sidebarAnimationFrame)
			return state.fraction
		}
	}
	state.sliding = false
	state.fraction = target
	return target
}

// sidePanePaintWidths shrinks the right column to the slide fraction. An open
// pane keeps one column so the split exists on the first opening frame
// (Rust sidebar_areas `.max(u16::from(open))`).
func (m *Model) sidePanePaintWidths(width int, settled int, fraction float64) (int, int) {
	right := int(math.Round(float64(settled) * fraction))
	if right < 1 {
		right = 1
	}
	right = min(right, settled)
	left := max(width-ansi.StringWidth(sideWorkspaceDivider)-right, 1)
	return left, right
}

// snapshotSidebarColumn retains the settled right column so a closing slide can
// keep shrinking what was last painted after the pane state is gone.
func (m *Model) snapshotSidebarColumn(right []string, settled int) {
	rows := make([]string, len(right))
	for index := range right {
		rows[index] = padSidePaneLine(codextui.TruncateToWidth(right[index], settled), settled)
	}
	m.sidebarClosingLines = rows
	m.sidebarClosingWidth = settled
}

// fitSidebarColumn windows the retained right column to the painted width.
func fitSidebarColumn(right []string, paintWidth int, height int) []string {
	rows := make([]string, height)
	for index := range rows {
		line := ""
		if index < len(right) {
			line = right[index]
		}
		rows[index] = padSidePaneLine(codextui.TruncateToWidth(line, paintWidth), paintWidth)
	}
	return rows
}
