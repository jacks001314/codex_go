package tea

import (
	"strings"
	"testing"
	"time"

	"codex_go/protocol"
	codextui "codex_go/tui"
	"codex_go/tui/chatwidget"
	"codex_go/utils"
)

// Rust parity: codex-rs/tui/src/status_indicator_widget.rs (frame cadence and
// line composition) plus the chatwidget status owners in
// status_controls.rs / compaction.rs / tool_requests.rs / safety_recourse.rs /
// streaming.rs.

func statusRowTestModel(now *time.Time, width int) (*Model, *codextui.State) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{Width: width, Height: 30})
	model.now = func() time.Time { return *now }
	return model, state
}

// TestStatusRowFrameCadenceLikeRust mirrors StatusIndicatorWidget::render:
// 32 ms frames while the progress/shimmer effects animate, one-second frames
// otherwise, and one second for reduced motion or a hidden row.
func TestStatusRowFrameCadenceLikeRust(t *testing.T) {
	now := time.Unix(1_000, 0)
	model, state := statusRowTestModel(&now, 100)

	if got := model.statusRowFrameInterval(); got != statusRowStaticFrame {
		t.Fatalf("idle frame interval = %v, want %v", got, statusRowStaticFrame)
	}

	state.SetStatus("running")
	model.syncTaskRunningTimer()
	if got := model.statusRowFrameInterval(); got != statusRowAnimatedFrame {
		t.Fatalf("animated frame interval = %v, want %v", got, statusRowAnimatedFrame)
	}

	model.hideStatusIndicator()
	if got := model.statusRowFrameInterval(); got != statusRowStaticFrame {
		t.Fatalf("hidden row interval = %v, want %v", got, statusRowStaticFrame)
	}

	animationsOff := false
	reduced := NewModel(codextui.NewState(nil), Options{Width: 100, Height: 30, AnimationsEnabled: &animationsOff})
	reduced.now = func() time.Time { return now }
	reduced.State.SetStatus("running")
	reduced.syncTaskRunningTimer()
	if got := reduced.statusRowFrameInterval(); got != statusRowStaticFrame {
		t.Fatalf("reduced-motion interval = %v, want %v", got, statusRowStaticFrame)
	}

	effects := DefaultEffectsSettings()
	effects.Progress = false
	effects.Shimmer = false
	animationsOn := true
	quiet := NewModel(codextui.NewState(nil), Options{Width: 100, Height: 30, AnimationsEnabled: &animationsOn, Effects: &effects})
	quiet.now = func() time.Time { return now }
	quiet.State.SetStatus("running")
	quiet.syncTaskRunningTimer()
	if got := quiet.statusRowFrameInterval(); got != statusRowStaticFrame {
		t.Fatalf("effects-off interval = %v, want %v", got, statusRowStaticFrame)
	}
}

// TestStatusRowOwnerPrecedenceLikeRust exercises status_controls::set_status:
// compaction overrides every caller, and each dedicated owner beats the
// streaming reasoning summary.
func TestStatusRowOwnerPrecedenceLikeRust(t *testing.T) {
	now := time.Unix(2_000, 0)
	model, state := statusRowTestModel(&now, 120)
	state.SetStatus("running")
	model.syncTaskRunningTimer()
	model.workingStatusHeader = "Reasoning tail"

	if got := model.statusIndicatorState().Header; got != "Reasoning tail" {
		t.Fatalf("reasoning header = %q", got)
	}

	model.mcpStartupActive = true
	model.mcpStartupHeader = "Starting MCP servers"
	if got := model.statusIndicatorState().Header; got != "Starting MCP servers" {
		t.Fatalf("mcp startup header = %q", got)
	}

	model.workingStatusHeader = WaitingForBackgroundTerminalHeader
	model.commandLifecycle.UnifiedExecWait = &chatwidget.UnifiedExecWaitStreak{ProcessID: "proc-1", CommandDisplay: "python server.py"}
	waitState := model.statusIndicatorState()
	if waitState.Header != WaitingForBackgroundTerminalHeader || waitState.Details != "python server.py" {
		t.Fatalf("unified-exec wait state = %#v", waitState)
	}

	model.pendingSafetyRecourse = &chatwidget.PendingSafetyRecourse{Result: &chatwidget.SafetyRecourse{}}
	if got := model.statusIndicatorState().Header; got != chatwidget.SafetyRecourseStatusHeader {
		t.Fatalf("safety recourse header = %q", got)
	}

	model.toolRequestRuntime.PendingGuardianReviewStatus.StartOrUpdate("review-1", "Reviewing command")
	guardian := model.statusIndicatorState()
	if guardian.Header != "Reviewing approval request" || guardian.Details != "Reviewing command" {
		t.Fatalf("guardian review state = %#v", guardian)
	}

	model.compactionActive = true
	compaction := model.statusIndicatorState()
	if compaction.Header != CompactionStatusHeader || compaction.Details != CompactionStatusDetails {
		t.Fatalf("compaction state = %#v", compaction)
	}

	// Compaction wins even over an outstanding guardian review.
	view := utils.StripANSI(model.View())
	if !strings.Contains(view, CompactionStatusHeader) {
		t.Fatalf("compaction header missing from row:\n%s", view)
	}
	if strings.Contains(view, "Reviewing approval request") {
		t.Fatalf("guardian review leaked past compaction:\n%s", view)
	}
	if !strings.Contains(view, CompactionStatusDetails) {
		t.Fatalf("compaction details missing from row:\n%s", view)
	}
}

// TestStatusRowHookInlineAndOverflowLikeRust covers StatusIndicatorWidget::lines:
// the hook summary joins the header line when it fits and otherwise moves to a
// `  └ ` line.
func TestStatusRowHookInlineAndOverflowLikeRust(t *testing.T) {
	now := time.Unix(3_000, 0)

	wide, wideState := statusRowTestModel(&now, 120)
	wideState.SetStatus("running")
	wide.syncTaskRunningTimer()
	wide.Update(HookRunMsg{ID: "run", EventName: "preToolUse", StatusMessage: "checking policy", Running: true})
	now = now.Add(hookStatusRevealDelay)
	wideView := utils.StripANSI(wide.View())
	wideLower := strings.ToLower(wideView)
	var inlineLine string
	for _, line := range strings.Split(wideLower, "\n") {
		if strings.Contains(line, "working") {
			inlineLine = line
		}
	}
	if inlineLine == "" || !strings.Contains(inlineLine, "checking policy") {
		t.Fatalf("hook summary did not fold into the status line:\n%s", wideView)
	}
	if strings.Count(wideLower, "checking policy") != 1 {
		t.Fatalf("hook summary rendered more than once:\n%s", wideView)
	}

	narrow, narrowState := statusRowTestModel(&now, 24)
	narrowState.SetStatus("running")
	narrow.syncTaskRunningTimer()
	narrow.Update(HookRunMsg{ID: "run", EventName: "preToolUse", StatusMessage: "checking policy", Running: true})
	now = now.Add(hookStatusRevealDelay)
	narrowView := utils.StripANSI(narrow.View())
	narrowLower := strings.ToLower(narrowView)
	var overflowLine string
	for _, line := range strings.Split(narrowLower, "\n") {
		if strings.Contains(line, "\u2514") {
			overflowLine = line
		}
	}
	if !strings.Contains(overflowLine, "checking") {
		t.Fatalf("hook summary did not reflow below the status row:\n%s", narrowView)
	}
	if strings.Count(narrowLower, "checking") != 1 {
		t.Fatalf("hook summary rendered more than once:\n%s", narrowView)
	}
}

// TestCompactionDoesNotDuplicateTranscriptRowLikeRust keeps the live compaction
// copy in the status row only.
func TestCompactionDoesNotDuplicateTranscriptRowLikeRust(t *testing.T) {
	now := time.Unix(4_000, 0)
	model, state := statusRowTestModel(&now, 120)
	state.SetStatus("running")
	model.syncTaskRunningTimer()

	model.applyItemStarted(&protocol.ThreadItem{ID: "compact-live", Type: "contextCompaction"}, 0)
	if !model.compactionActive {
		t.Fatalf("compaction did not start")
	}
	view := utils.StripANSI(model.View())
	if !strings.Contains(view, CompactionStatusHeader) {
		t.Fatalf("compaction header missing:\n%s", view)
	}
	if strings.Contains(view, "Compacting context...") {
		t.Fatalf("transcript duplicated the live compaction copy:\n%s", view)
	}
	if strings.Count(view, CompactionStatusDetails) != 1 {
		t.Fatalf("compaction details rendered %d times:\n%s", strings.Count(view, CompactionStatusDetails), view)
	}
}

// TestStatusRowTickReschedulesLikeRust keeps the frame chain alive.
func TestStatusRowTickReschedulesLikeRust(t *testing.T) {
	now := time.Unix(5_000, 0)
	model, state := statusRowTestModel(&now, 100)
	state.SetStatus("running")
	model.syncTaskRunningTimer()
	if _, cmd := model.Update(statusRowTickMsg{}); cmd == nil {
		t.Fatalf("status row tick did not schedule the next frame")
	}
}
