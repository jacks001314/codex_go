package tea

import (
	"strings"
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	codextui "codex_go/tui"
	"codex_go/tui/chatwidget"
)

// Rust parity: codex-rs/tui/src/status_indicator_widget.rs plus the status
// owners in codex-rs/tui/src/chatwidget (compaction.rs, tool_requests.rs,
// safety_recourse.rs, mcp_startup.rs, streaming.rs).

const (
	// CompactionStatusHeader/Details mirror chatwidget::compaction's
	// COMPACTION_HEADER/COMPACTION_DETAILS. Compaction always replaces the
	// status row body while it runs.
	CompactionStatusHeader  = "Compacting context"
	CompactionStatusDetails = "Making room to continue."

	// WaitingForBackgroundTerminalHeader mirrors the unified-exec wait streak
	// header (Rust #43921).
	WaitingForBackgroundTerminalHeader = "Waiting for background terminal"

	// statusRowAnimatedFrame mirrors Rust StatusIndicatorWidget::render: frames
	// arrive every 32 ms while the progress/shimmer effects animate.
	statusRowAnimatedFrame = 32 * time.Millisecond
	// statusRowStaticFrame is Rust's one-second clock-only frame.
	statusRowStaticFrame = time.Second
)

// statusRowTickMsg advances the live status row. Rust schedules this frame from
// StatusIndicatorWidget::render at 32 ms while progress/shimmer run and once a
// second otherwise.
type statusRowTickMsg struct{}

// statusRowTickCmd schedules the next status-row frame. The chain stays alive
// for the life of the program; the interval carries the cadence.
func (m *Model) statusRowTickCmd() bubbletea.Cmd {
	return bubbletea.Tick(m.statusRowFrameInterval(), func(time.Time) bubbletea.Msg {
		return statusRowTickMsg{}
	})
}

// statusRowFrameInterval reports Rust's frame cadence for the live status row.
func (m *Model) statusRowFrameInterval() time.Duration {
	if m == nil {
		return statusRowStaticFrame
	}
	if m.statusRowVisible() && m.animationsEnabled && (m.effects.Progress || m.effects.Shimmer) {
		return statusRowAnimatedFrame
	}
	return statusRowStaticFrame
}

// statusRowVisible mirrors BottomPane::status_indicator_visible: the row only
// exists while a turn runs and the transcript has not replaced it.
func (m *Model) statusRowVisible() bool {
	return m != nil && m.isTaskRunning() && !m.statusIndicatorHidden
}

// statusIndicatorState resolves the row's header and details with Rust's owner
// precedence. Rust funnels every status update through
// chatwidget::status_controls::set_status, which replaces the caller's text with
// the compaction copy while compaction runs (compaction.rs), and the remaining
// owners refuse to be overwritten by streaming reasoning deltas
// (streaming.rs::on_agent_reasoning_delta): pending guardian reviews
// (tool_requests.rs), the safety-recourse wait (safety_recourse.rs), the
// unified-exec wait streak, MCP startup, and finally the reasoning summary.
func (m *Model) statusIndicatorState() chatwidget.StatusIndicatorState {
	if m == nil {
		return chatwidget.WorkingStatusIndicatorState()
	}
	if m.compactionActive {
		return chatwidget.StatusIndicatorState{
			Header:          CompactionStatusHeader,
			Details:         CompactionStatusDetails,
			DetailsMaxLines: codextui.StatusDetailsDefaultMaxLines,
		}
	}
	if status, ok := m.toolRequestRuntime.PendingGuardianReviewStatus.StatusIndicatorState(); ok {
		return status
	}
	if m.pendingSafetyRecourse != nil && m.pendingSafetyRecourse.Result != nil {
		return chatwidget.StatusIndicatorState{Header: chatwidget.SafetyRecourseStatusHeader}
	}
	if strings.EqualFold(strings.TrimSpace(m.workingStatusHeader), WaitingForBackgroundTerminalHeader) {
		state := chatwidget.StatusIndicatorState{Header: WaitingForBackgroundTerminalHeader}
		if wait := m.commandLifecycle.UnifiedExecWait; wait != nil {
			if command := strings.TrimSpace(wait.CommandDisplay); command != "" {
				state.Details = command
				state.DetailsMaxLines = 1
			}
		}
		return state
	}
	if m.mcpStartupActive && strings.TrimSpace(m.mcpStartupHeader) != "" {
		return chatwidget.StatusIndicatorState{Header: strings.TrimSpace(m.mcpStartupHeader)}
	}
	if header := strings.TrimSpace(m.workingStatusHeader); header != "" {
		return chatwidget.StatusIndicatorState{Header: header}
	}
	return chatwidget.WorkingStatusIndicatorState()
}

// appendHookStatusToRow folds hook activity into the status row the way Rust
// does in StatusIndicatorWidget::lines: the summary joins the header line after
// the elapsed/interrupt segment when it fits, and otherwise moves to its own
// `  └ ` line truncated to the row width. The dedicated hook row is only needed
// when the status row is hidden.
func (m *Model) appendHookStatusToRow(lines []string, width int, now time.Time) []string {
	summary := m.runningHookSummary(now)
	if summary == "" || len(lines) == 0 {
		return lines
	}
	styled := m.renderHookSummaryText(summary, now)
	separator := m.footerStyle.Render(" · ")
	if ansi.StringWidth(lines[0])+ansi.StringWidth(separator)+ansi.StringWidth(styled) <= width {
		lines[0] += separator + styled
		return lines
	}
	overflow := m.footerStyle.Render("  └ ") + styled
	return append(lines, codextui.TruncateWithEllipsis(overflow, width))
}
