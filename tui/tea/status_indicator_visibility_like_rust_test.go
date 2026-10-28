package tea

import (
	"strings"
	"testing"
	"time"

	"codex_go/protocol"
	codextui "codex_go/tui"
	"codex_go/utils"
)

// utilsStripANSI keeps the assertions independent of the shimmer styling that
// wraps the header while progress effects are enabled.
func utilsStripANSI(text string) string { return utils.StripANSI(text) }

// newRunningStatusModel builds a turn that is running but has not streamed any
// assistant output yet, which is when Rust shows the inline status row.
func newRunningStatusModel(t *testing.T) *Model {
	t.Helper()
	model := NewModel(codextui.NewState(nil), Options{Width: 100, Height: 30})
	model.Update(ThreadEventMsg{Event: protocol.TurnStarted()})
	if !model.isTaskRunning() {
		t.Fatalf("turn did not start running: status = %q", model.State.Status)
	}
	return model
}

// TestWorkingRowHidesWhileAssistantStreamsLikeRust mirrors Rust's
// sync_active_stream_tail (codex-rs/tui/src/chatwidget/streaming.rs): while
// assistant output owns the transcript tail the inline status row is removed so
// only one in-progress affordance is on screen.
func TestWorkingRowHidesWhileAssistantStreamsLikeRust(t *testing.T) {
	model := newRunningStatusModel(t)
	if got := model.renderWorkingIndicator(); !strings.Contains(got, "Working") {
		t.Fatalf("working row at turn start = %q, want visible", got)
	}

	model.Update(ThreadEventMsg{Event: protocol.AgentMessageDelta("msg-1", "Streaming answer")})
	if got := model.renderWorkingIndicator(); got != "" {
		t.Fatalf("working row while assistant streams = %q, want hidden", got)
	}
}

// TestWorkingRowRestoresAfterCommentaryLikeRust mirrors Rust's
// pending_status_indicator_restore gate: a committed commentary block restores
// the row while the turn continues.
func TestWorkingRowRestoresAfterCommentaryLikeRust(t *testing.T) {
	model := newRunningStatusModel(t)
	model.Update(ThreadEventMsg{Event: protocol.AgentMessageDelta("msg-commentary", "Checking the command.")})
	if got := model.renderWorkingIndicator(); got != "" {
		t.Fatalf("working row while commentary streams = %q, want hidden", got)
	}

	model.Update(ThreadEventMsg{Event: protocol.ItemCompleted(
		protocol.AgentMessageItemWithPhase("msg-commentary", "Checking the command.", messagePhaseCommentary),
	)})
	if got := model.renderWorkingIndicator(); !strings.Contains(got, "Working") {
		t.Fatalf("working row after commentary = %q, want visible", got)
	}
}

// TestWorkingRowStaysHiddenAfterFinalAnswerLikeRust keeps the final answer as
// the turn's only in-progress surface; the row stays hidden until a tool starts
// or the turn ends.
func TestWorkingRowStaysHiddenAfterFinalAnswerLikeRust(t *testing.T) {
	model := newRunningStatusModel(t)
	model.Update(ThreadEventMsg{Event: protocol.AgentMessageDelta("msg-final", "All done.")})
	model.Update(ThreadEventMsg{Event: protocol.ItemCompleted(
		protocol.AgentMessageItemWithPhase("msg-final", "All done.", messagePhaseFinalAnswer),
	)})
	if got := model.renderWorkingIndicator(); got != "" {
		t.Fatalf("working row after final answer = %q, want hidden", got)
	}
}

// TestWorkingRowRestoresForInFlightCommandLikeRust mirrors
// command_lifecycle.rs ensure_status_indicator: starting a command re-shows the
// row that streaming hid.
func TestWorkingRowRestoresForInFlightCommandLikeRust(t *testing.T) {
	model := newRunningStatusModel(t)
	model.Update(ThreadEventMsg{Event: protocol.AgentMessageDelta("msg-1", "Let me run that.")})
	if got := model.renderWorkingIndicator(); got != "" {
		t.Fatalf("working row while assistant streams = %q, want hidden", got)
	}

	model.Update(ThreadEventMsg{Event: protocol.ItemStarted(
		protocol.CommandExecutionItem("call-1", "go test ./...", "", nil, "in_progress"),
	)})
	if got := model.renderWorkingIndicator(); !strings.Contains(got, "Working") {
		t.Fatalf("working row while command runs = %q, want visible", got)
	}
}

// TestWorkingRowShowsUnifiedExecFooterLikeRust mirrors
// bottom_pane/unified_exec_footer.rs: the status row reuses the footer summary
// so background terminals stay visible next to the elapsed/interrupt segment.
func TestWorkingRowShowsUnifiedExecFooterLikeRust(t *testing.T) {
	model := newRunningStatusModel(t)
	if got := model.renderWorkingIndicator(); strings.Contains(got, "background terminal") {
		t.Fatalf("working row without background terminals = %q", got)
	}

	model.commandLifecycle.TrackUnifiedExecProcessBegin("call-1", "proc-1", "powershell -NoProfile -Command \"Start-Sleep -Seconds 8\"")
	if got := model.renderWorkingIndicator(); !strings.Contains(got, "1 background terminal running \u00b7 /ps to view \u00b7 /stop to close") {
		t.Fatalf("working row with one background terminal = %q", got)
	}

	model.commandLifecycle.TrackUnifiedExecProcessBegin("call-2", "proc-2", "rg foo")
	if got := model.renderWorkingIndicator(); !strings.Contains(got, "2 background terminals running") {
		t.Fatalf("working row with two background terminals = %q", got)
	}

	model.commandLifecycle.TrackUnifiedExecProcessEnd("call-1", "proc-1")
	model.commandLifecycle.TrackUnifiedExecProcessEnd("call-2", "proc-2")
	if got := model.renderWorkingIndicator(); strings.Contains(got, "background terminal") {
		t.Fatalf("working row after terminals finished = %q", got)
	}
}

// TestUnifiedExecCommandKeepsStatusRowOnlyLikeRust mirrors
// command_lifecycle.rs: a unified-exec startup command with no parsed actions
// never becomes a transcript cell, so the status row (with the background
// terminal footer) is the only live surface.
func TestUnifiedExecCommandKeepsStatusRowOnlyLikeRust(t *testing.T) {
	model := newRunningStatusModel(t)
	item := protocol.CommandExecutionItem("call-1", `powershell -NoProfile -Command "Start-Sleep -Seconds 8"`, "", nil, "in_progress")
	item.Metadata = map[string]any{"source": "unifiedExecStartup"}
	model.Update(ThreadEventMsg{Event: protocol.ItemStarted(item)})

	view := strings.ToLower(model.View())
	if strings.Contains(view, "running powershell") {
		t.Fatalf("unified exec startup rendered a duplicate running cell:\n%s", view)
	}
	if got := model.renderWorkingIndicator(); !strings.Contains(got, "Working") {
		t.Fatalf("working row = %q, want visible", got)
	}
	if got := model.renderWorkingIndicator(); !strings.Contains(got, "1 background terminal running") {
		t.Fatalf("working row = %q, want the unified exec footer summary", got)
	}
}

// TestStatusRowActivityMarkerBlinksLikeRust mirrors status_indicator_widget's
// activity_indicator: progress effects blink the marker every 600 ms while
// reduced motion hides it.
func TestStatusRowActivityMarkerBlinksLikeRust(t *testing.T) {
	restore := codextui.SetStdoutColorLevelForTest(codextui.ColorANSI16)
	defer restore()
	start := time.Unix(100, 0)
	state := codextui.NewState(nil)
	state.SetStatus("running")
	model := NewModel(state, Options{Width: 80, Height: 18})
	model.taskStartedAt = start
	model.statusTimer.ResetAt(0, start)
	model.now = func() time.Time { return start.Add(700 * time.Millisecond) }
	if got := utilsStripANSI(model.renderWorkingIndicator()); !strings.HasPrefix(got, "\u25e6 Working") {
		t.Fatalf("activity marker = %q, want the off-phase glyph", got)
	}
	disabled := false
	reduced := NewModel(state, Options{Width: 80, Height: 18, AnimationsEnabled: &disabled})
	reduced.taskStartedAt = start
	reduced.statusTimer.ResetAt(0, start)
	reduced.now = func() time.Time { return start }
	if got := utilsStripANSI(reduced.renderWorkingIndicator()); !strings.HasPrefix(got, "Working (") {
		t.Fatalf("reduced-motion row = %q, want no activity marker", got)
	}
}
