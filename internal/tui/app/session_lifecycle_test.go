package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"codex_go/internal/appserver"
	codextui "codex_go/internal/tui"
)

func TestSessionLifecycleThreadReadErrorClassifiersMatchRust(t *testing.T) {
	terminal := fmt.Errorf("thread/read failed: %w", errors.New("thread not loaded: thread-1"))
	if !IsTerminalThreadReadError(terminal) {
		t.Fatal("terminal thread/read error was not detected")
	}
	if !ClosedStateForThreadReadError(terminal, nil) {
		t.Fatal("terminal thread/read error should force closed state")
	}

	existingClosed := true
	generic := errors.New("transport disconnected")
	if !ClosedStateForThreadReadError(generic, &existingClosed) {
		t.Fatal("existing closed state should be preserved")
	}
	existingClosed = false
	if ClosedStateForThreadReadError(generic, &existingClosed) {
		t.Fatal("generic error with existing open state should not be closed")
	}
}

func TestCanFallbackFromIncludeTurnsErrorMatchRust(t *testing.T) {
	for _, err := range []error{
		errors.New("includeTurns is unavailable before first user message"),
		fmt.Errorf("thread/read failed: %w", errors.New("ephemeral threads do not support includeTurns")),
	} {
		if !CanFallbackFromIncludeTurnsError(err) {
			t.Fatalf("CanFallbackFromIncludeTurnsError(%v) = false, want true", err)
		}
	}
	if CanFallbackFromIncludeTurnsError(errors.New("thread not loaded")) {
		t.Fatal("unrelated error should not allow includeTurns fallback")
	}
}

func TestSessionLifecycleStateStartAndClose(t *testing.T) {
	var state SessionLifecycleState
	state.MarkStarted("thread-1")
	if state.ThreadID != "thread-1" || !state.Started || state.Closed {
		t.Fatalf("state after start = %#v", state)
	}
	state.MarkClosed()
	if !state.Closed {
		t.Fatalf("state after close = %#v", state)
	}
}

func TestRefreshAgentPickerThreadLivenessMatchRust(t *testing.T) {
	nav := NewAgentNavigationState()
	nav.Upsert("thread-1", "Nick", "role", false)

	result := RefreshAgentPickerThreadLiveness(nav, AgentPickerLivenessRead{
		ThreadID: "thread-1",
		Thread: &appserver.Thread{
			ID:     "thread-1",
			Status: appserver.ActiveStatus(appserver.ThreadActiveFlagWaitingOnApproval),
		},
	})
	if !result.Available || !result.IsRunning || result.AgentNickname != "Nick" || result.AgentRole != "role" {
		t.Fatalf("active liveness result = %#v", result)
	}
	entry, _ := nav.Get("thread-1")
	if !entry.IsRunning || entry.IsClosed {
		t.Fatalf("active entry = %#v", entry)
	}

	result = RefreshAgentPickerThreadLiveness(nav, AgentPickerLivenessRead{
		ThreadID:  "thread-1",
		ReadError: errors.New("thread/read transport error: broken pipe"),
	})
	if !result.Available || result.IsRunning || result.IsClosed {
		t.Fatalf("transient liveness result = %#v", result)
	}
	entry, _ = nav.Get("thread-1")
	if entry.IsRunning || entry.IsClosed || entry.AgentNickname != "Nick" {
		t.Fatalf("transient entry = %#v", entry)
	}

	result = RefreshAgentPickerThreadLiveness(nav, AgentPickerLivenessRead{
		ThreadID:  "thread-1",
		ReadError: errors.New("thread/read failed: thread not loaded: thread-1"),
	})
	if result.Available || !result.Removed {
		t.Fatalf("terminal liveness result = %#v", result)
	}
	if _, ok := nav.Get("thread-1"); ok {
		t.Fatal("terminal missing replay channel should remove picker entry")
	}
}

func TestRefreshAgentPickerThreadLivenessTerminalWithReplayKeepsClosedEntry(t *testing.T) {
	nav := NewAgentNavigationState()
	result := RefreshAgentPickerThreadLiveness(nav, AgentPickerLivenessRead{
		ThreadID:         "thread-2",
		ReadError:        errors.New("thread/read failed: thread not loaded: thread-2"),
		HasReplayChannel: true,
	})
	if !result.Available || !result.IsClosed || result.Removed {
		t.Fatalf("replay terminal result = %#v", result)
	}
	entry, ok := nav.Get("thread-2")
	if !ok || !entry.IsClosed || entry.IsRunning {
		t.Fatalf("replay terminal entry = %#v ok=%v", entry, ok)
	}
}

func TestSelectAgentThreadDecisionMatchRust(t *testing.T) {
	if decision := SelectAgentThreadDecision(AgentThreadSelectionInput{ThreadID: "thread-1", ActiveThreadID: "thread-1"}); !decision.Noop {
		t.Fatalf("same thread decision = %#v", decision)
	}

	missing := SelectAgentThreadDecision(AgentThreadSelectionInput{ThreadID: "thread-1"})
	if !missing.NoLongerAvailable || missing.ErrorMessage != "Agent thread thread-1 is no longer available." {
		t.Fatalf("missing decision = %#v", missing)
	}

	closedWithoutChannel := SelectAgentThreadDecision(AgentThreadSelectionInput{
		ThreadID:          "thread-closed",
		LivenessAvailable: true,
		EntryExists:       true,
		EntryIsClosed:     true,
	})
	if !closedWithoutChannel.NoLongerAvailable {
		t.Fatalf("closed without channel decision = %#v", closedWithoutChannel)
	}

	attachedReplay := SelectAgentThreadDecision(AgentThreadSelectionInput{
		ThreadID:          "thread-replay",
		LivenessAvailable: true,
		AttachAttempted:   true,
		LiveAttached:      false,
	})
	if !attachedReplay.Proceed || !attachedReplay.IsReplayOnly || !attachedReplay.AttachedReplayOnly || !attachedReplay.ShouldAttachLive {
		t.Fatalf("attached replay decision = %#v", attachedReplay)
	}
	if got := ReplayOnlyAgentThreadMessage("thread-replay", true); got != "Agent thread thread-replay could not be resumed live. Replaying saved transcript." {
		t.Fatalf("attached replay message = %q", got)
	}

	attachErr := SelectAgentThreadDecision(AgentThreadSelectionInput{
		ThreadID:          "thread-err",
		LivenessAvailable: true,
		AttachAttempted:   true,
		AttachError:       errors.New("transport disconnected"),
	})
	if attachErr.Proceed || attachErr.ErrorMessage != "Failed to attach to agent thread thread-err: transport disconnected" {
		t.Fatalf("attach error decision = %#v", attachErr)
	}
}

func TestStartupThreadStartedDecisionMatchRust(t *testing.T) {
	stale := StartupThreadStartedDecisionForResult(false, "thread-stale", nil)
	if !stale.IgnoreUnexpectedCompleted || stale.UnsubscribeStaleThreadID != "thread-stale" || stale.DiscardStaleThreadID != "thread-stale" {
		t.Fatalf("stale startup decision = %#v", stale)
	}

	ok := StartupThreadStartedDecisionForResult(true, "thread-new", nil)
	if !ok.PendingCleared || ok.QueueSubmissions == nil || *ok.QueueSubmissions || !ok.EnqueuePrimaryThread || !ok.MaybeSendQueuedInput || ok.ErrorMessage != "" {
		t.Fatalf("pending startup ok = %#v", ok)
	}

	failed := StartupThreadStartedDecisionForResult(true, "", errors.New("boom"))
	if !failed.PendingCleared || failed.EnqueuePrimaryThread || failed.MaybeSendQueuedInput || failed.ErrorMessage != "Failed to start a fresh session through the app server: boom" {
		t.Fatalf("pending startup failed = %#v", failed)
	}
}

func TestSessionSummaryForThreadMatchRust(t *testing.T) {
	if summary := SessionSummaryForThread(codextui.TokenUsage{}, "", "", ""); summary != nil {
		t.Fatalf("empty summary = %#v", summary)
	}

	rolloutPath := filepath.Join(t.TempDir(), "rollout.jsonl")
	if summary := SessionSummaryForThread(codextui.TokenUsage{}, "123e4567-e89b-12d3-a456-426614174000", "", rolloutPath); summary != nil {
		t.Fatalf("missing rollout summary = %#v", summary)
	}
	if err := os.WriteFile(rolloutPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write rollout: %v", err)
	}

	summary := SessionSummaryForThread(codextui.TokenUsage{
		InputTokens:  10,
		OutputTokens: 2,
		TotalTokens:  12,
	}, "123e4567-e89b-12d3-a456-426614174000", "", rolloutPath)
	if summary == nil || summary.UsageLine != "Token usage: total=12 input=10 output=2" || summary.ResumeHint != "codex resume 123e4567-e89b-12d3-a456-426614174000" {
		t.Fatalf("summary = %#v", summary)
	}

	named := SessionSummaryForThread(codextui.TokenUsage{}, "123e4567-e89b-12d3-a456-426614174000", "my-session", rolloutPath)
	if named == nil || named.ResumeHint != "codex resume, then select my-session (123e4567-e89b-12d3-a456-426614174000)" {
		t.Fatalf("named summary = %#v", named)
	}
}

func TestApplyLoadedSubagentBackfillMatchesRust(t *testing.T) {
	nav := NewAgentNavigationState()
	ApplyLoadedSubagentBackfill(nav, []LoadedSubagentThread{
		{ThreadID: "thread-a", AgentNickname: "A", AgentRole: "builder", AgentPath: "agents/a.md"},
	})
	entry, ok := nav.Get("thread-a")
	if !ok || entry.AgentNickname != "A" || entry.AgentRole != "builder" || entry.AgentPath != "agents/a.md" || entry.IsClosed {
		t.Fatalf("backfilled entry = %#v ok=%v", entry, ok)
	}
}
