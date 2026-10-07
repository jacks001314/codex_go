package turn

import (
	"context"
	"testing"
	"time"

	"codex_go/model"
)

// toolsChangeLoopAgent serves one completed response per call and delegates the
// tool-change comparison to the real client state.
type toolsChangeLoopAgent struct {
	tracker *model.ResponsesAgentRunner
}

func (a *toolsChangeLoopAgent) Run(_ context.Context, _ *model.AgentRequest) (*model.AgentResponse, error) {
	return &model.AgentResponse{
		ResponseID: "resp-done",
		Message:    "done",
		Items:      []model.AgentItem{{Type: "agent_message", Text: "done"}},
	}, nil
}

func (a *toolsChangeLoopAgent) InferenceToolsChanged(sessionKey string, tools []any) bool {
	return a.tracker.InferenceToolsChanged(sessionKey, tools)
}

// TestAgentLoopCountsInferenceToolChangesLikeRust mirrors Rust #50964
// (codex-rs/core/src/session/turn.rs `try_run_sampling_request`): every sampling
// request compares its full model-visible tool list with the retained one and a
// change is recorded on the turn profile. The retained list survives turns, so
// the first turn only establishes the baseline.
func TestAgentLoopCountsInferenceToolChangesLikeRust(t *testing.T) {
	agent := &toolsChangeLoopAgent{tracker: model.NewResponsesAgentRunner(nil)}
	loop := NewAgentLoop(&AgentLoopOptions{Agent: agent})
	alpha := []any{map[string]any{"type": "function", "name": "alpha"}}
	changed := []any{map[string]any{"type": "function", "name": "alpha", "description": "changed"}}

	for _, tc := range []struct {
		name  string
		tools []any
		want  uint32
	}{
		{"baseline", alpha, 0},
		{"catalog change", changed, 1},
		{"unchanged catalog", changed, 0},
	} {
		result, err := loop.Run(context.Background(), &AgentLoopRequest{ThreadID: "thread-1", Tools: tc.tools})
		if err != nil {
			t.Fatalf("%s: Run() error = %v", tc.name, err)
		}
		if result.TimingProfile == nil || result.TimingProfile.ToolsChangeCount != tc.want {
			t.Fatalf("%s: ToolsChangeCount = %#v, want %d", tc.name, result.TimingProfile, tc.want)
		}
	}
}

// TestTimingStateRecordsToolsChangeLikeRust mirrors Rust #50964
// (codex-rs/core/src/turn_timing.rs `record_tools_change`): a change counts only
// while the profile is active. The Rust test
// `turn_profile_breaks_down_sampling_blocking_and_retry_overhead` expects
// tools_change_count 0 unless a change was recorded.
func TestTimingStateRecordsToolsChangeLikeRust(t *testing.T) {
	timing := NewTimingState()
	start := time.Date(2026, 10, 7, 1, 0, 0, 0, time.UTC)
	timing.RecordToolsChange()
	if profile := timing.CompleteProfile(start); profile.ToolsChangeCount != 0 {
		t.Fatalf("RecordToolsChange before start = %d, want 0", profile.ToolsChangeCount)
	}

	timing = NewTimingState()
	timing.MarkTurnStarted(start)
	timing.RecordToolsChange()
	timing.RecordToolsChange()
	profile := timing.CompleteProfile(start.Add(time.Second))
	if profile.ToolsChangeCount != 2 {
		t.Fatalf("ToolsChangeCount = %d, want 2", profile.ToolsChangeCount)
	}
	timing.RecordToolsChange()
	if again := timing.CompleteProfile(start.Add(2 * time.Second)); again.ToolsChangeCount != 2 {
		t.Fatalf("ToolsChangeCount after completion = %d, want 2", again.ToolsChangeCount)
	}
}
