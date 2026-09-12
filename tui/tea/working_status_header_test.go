package tea

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"codex_go/protocol"
	codextui "codex_go/tui"
)

var workingHeaderANSIPattern = regexp.MustCompile("\x1b\\[[0-9;]*m")

// TestWorkingStatusHeaderMsgDrivesIndicator covers Rust #43921: the latest
// streaming reasoning summary line becomes the working indicator's header.
func TestWorkingStatusHeaderMsgDrivesIndicator(t *testing.T) {
	state := codextui.NewState(nil)
	state.ThreadID = "thread-a"
	state.Status = "running"
	model := NewModel(state, Options{Width: 80, Height: 12})
	model.taskStartedAt = time.Now().Add(-time.Second)

	model.Update(WorkingStatusHeaderMsg{ThreadID: "thread-a", Text: "Mapping the app structure"})
	if got := workingHeaderANSIPattern.ReplaceAllString(model.renderWorkingIndicator(), ""); !strings.Contains(got, "Mapping the app structure") {
		t.Fatalf("working indicator = %q, want reasoning header", got)
	}

	// Updates for another thread are ignored.
	model.Update(WorkingStatusHeaderMsg{ThreadID: "thread-b", Text: "other thread"})
	if got := workingHeaderANSIPattern.ReplaceAllString(model.renderWorkingIndicator(), ""); strings.Contains(got, "other thread") {
		t.Fatalf("working indicator picked up another thread's header: %q", got)
	}

	// An empty header restores the default.
	model.Update(WorkingStatusHeaderMsg{ThreadID: "thread-a"})
	if got := workingHeaderANSIPattern.ReplaceAllString(model.renderWorkingIndicator(), ""); !strings.Contains(got, "Working") {
		t.Fatalf("working indicator = %q, want default header", got)
	}
}

// TestThreadReasoningDeltaDrivesIndicator covers the local exec path: internal
// reasoning summary deltas update the working indicator and clear at the turn
// boundary (Rust #43921).
func TestThreadReasoningDeltaDrivesIndicator(t *testing.T) {
	state := codextui.NewState(nil)
	state.ThreadID = "thread-a"
	model := NewModel(state, Options{Width: 80, Height: 12})
	model.taskStartedAt = time.Now().Add(-time.Second)

	model.Update(ThreadEventMsg{Event: protocol.TurnStarted()})
	model.Update(ThreadEventMsg{Event: protocol.ReasoningSummaryDelta("reasoning-1", "## Step one\n")})
	if got := workingHeaderANSIPattern.ReplaceAllString(model.renderWorkingIndicator(), ""); !strings.Contains(got, "Step one") {
		t.Fatalf("working indicator = %q, want reasoning header", got)
	}

	// The latest usable line wins as more summary streams.
	model.Update(ThreadEventMsg{Event: protocol.ReasoningSummaryDelta("reasoning-1", "more detail")})
	if got := workingHeaderANSIPattern.ReplaceAllString(model.renderWorkingIndicator(), ""); !strings.Contains(got, "more detail") {
		t.Fatalf("working indicator = %q, want the latest summary line", got)
	}

	// A turn boundary clears the live reasoning header.
	model.Update(ThreadEventMsg{Event: protocol.TurnCompleted(protocol.Usage{})})
	if model.workingStatusHeader != "" {
		t.Fatalf("working status header = %q, want cleared", model.workingStatusHeader)
	}
}

// TestResumeAndSwitchSeedReasoningHeading covers Rust #43921's resume/switch
// restore: the active reasoning heading is seeded from the resumed or
// switched-to thread snapshot.
func TestResumeAndSwitchSeedReasoningHeading(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{Width: 80, Height: 12})

	model.applyResumeResponse("thread-a", SessionResumeResponse{
		Status:              "running",
		WorkingStatusHeader: "Step one",
	})
	if model.workingStatusHeader != "Step one" {
		t.Fatalf("resume heading = %q, want Step one", model.workingStatusHeader)
	}

	model.applyAgentSwitchResult(AgentSwitchResultMsg{
		ThreadID: "thread-b",
		Response: AgentThreadSwitchResponse{
			Entry:               codextui.AgentThreadEntry{ThreadID: "thread-b"},
			Status:              "running",
			WorkingStatusHeader: "Step two",
		},
	})
	if model.workingStatusHeader != "Step two" {
		t.Fatalf("switch heading = %q, want Step two", model.workingStatusHeader)
	}
}
