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
	nav.Upsert(rustThreadID1, "Nick", "role", false)

	result := RefreshAgentPickerThreadLiveness(nav, AgentPickerLivenessRead{
		ThreadID: rustThreadID1,
		Thread: &appserver.Thread{
			ID:     rustThreadID1,
			Status: appserver.ActiveStatus(appserver.ThreadActiveFlagWaitingOnApproval),
		},
	})
	if !result.Available || !result.IsRunning || result.AgentNickname != "Nick" || result.AgentRole != "role" {
		t.Fatalf("active liveness result = %#v", result)
	}
	entry, _ := nav.Get(rustThreadID1)
	if !entry.IsRunning || entry.IsClosed {
		t.Fatalf("active entry = %#v", entry)
	}

	result = RefreshAgentPickerThreadLiveness(nav, AgentPickerLivenessRead{
		ThreadID:  rustThreadID1,
		ReadError: errors.New("thread/read transport error: broken pipe"),
	})
	if !result.Available || result.IsRunning || result.IsClosed {
		t.Fatalf("transient liveness result = %#v", result)
	}
	entry, _ = nav.Get(rustThreadID1)
	if entry.IsRunning || entry.IsClosed || entry.AgentNickname != "Nick" {
		t.Fatalf("transient entry = %#v", entry)
	}

	result = RefreshAgentPickerThreadLiveness(nav, AgentPickerLivenessRead{
		ThreadID:  rustThreadID1,
		ReadError: errors.New("thread/read failed: thread not loaded: " + rustThreadID1),
	})
	if result.Available || !result.Removed {
		t.Fatalf("terminal liveness result = %#v", result)
	}
	if _, ok := nav.Get(rustThreadID1); ok {
		t.Fatal("terminal missing replay channel should remove picker entry")
	}
}

func TestRefreshAgentPickerThreadLivenessPreservesEmptyMetadataLikeRust(t *testing.T) {
	nav := NewAgentNavigationState()
	nav.Upsert(rustThreadID1, "Nick", "role", false)
	empty := ""

	result := RefreshAgentPickerThreadLiveness(nav, AgentPickerLivenessRead{
		ThreadID: rustThreadID1,
		Thread: &appserver.Thread{
			ID:            rustThreadID1,
			AgentNickname: &empty,
			AgentRole:     &empty,
			Status:        appserver.ActiveStatus(),
		},
	})
	if !result.Available || result.AgentNickname != "" || result.AgentRole != "" {
		t.Fatalf("empty metadata result = %#v", result)
	}
	entry, _ := nav.Get(rustThreadID1)
	if entry.AgentNickname != "" || entry.AgentRole != "" {
		t.Fatalf("empty metadata entry = %#v", entry)
	}
}

func TestRefreshAgentPickerThreadLivenessTerminalWithReplayKeepsClosedEntry(t *testing.T) {
	nav := NewAgentNavigationState()
	result := RefreshAgentPickerThreadLiveness(nav, AgentPickerLivenessRead{
		ThreadID:         rustThreadID2,
		ReadError:        errors.New("thread/read failed: thread not loaded: " + rustThreadID2),
		HasReplayChannel: true,
	})
	if !result.Available || !result.IsClosed || result.Removed {
		t.Fatalf("replay terminal result = %#v", result)
	}
	entry, ok := nav.Get(rustThreadID2)
	if !ok || !entry.IsClosed || entry.IsRunning {
		t.Fatalf("replay terminal entry = %#v ok=%v", entry, ok)
	}
}

func TestSelectAgentThreadDecisionMatchRust(t *testing.T) {
	if decision := SelectAgentThreadDecision(AgentThreadSelectionInput{ThreadID: rustThreadID1, ActiveThreadID: rustThreadID1}); !decision.Noop {
		t.Fatalf("same thread decision = %#v", decision)
	}

	missing := SelectAgentThreadDecision(AgentThreadSelectionInput{ThreadID: rustThreadID1})
	if !missing.NoLongerAvailable || missing.ErrorMessage != "Agent thread "+rustThreadID1+" is no longer available." {
		t.Fatalf("missing decision = %#v", missing)
	}

	closedWithoutChannel := SelectAgentThreadDecision(AgentThreadSelectionInput{
		ThreadID:          rustThreadID2,
		LivenessAvailable: true,
		EntryExists:       true,
		EntryIsClosed:     true,
	})
	if !closedWithoutChannel.NoLongerAvailable {
		t.Fatalf("closed without channel decision = %#v", closedWithoutChannel)
	}

	attachedReplay := SelectAgentThreadDecision(AgentThreadSelectionInput{
		ThreadID:          rustThreadID3,
		LivenessAvailable: true,
		AttachAttempted:   true,
		LiveAttached:      false,
	})
	if !attachedReplay.Proceed || !attachedReplay.IsReplayOnly || !attachedReplay.AttachedReplayOnly || !attachedReplay.ShouldAttachLive {
		t.Fatalf("attached replay decision = %#v", attachedReplay)
	}
	if got := ReplayOnlyAgentThreadMessage(rustThreadID3, true); got != "Agent thread "+rustThreadID3+" could not be resumed live. Replaying saved transcript." {
		t.Fatalf("attached replay message = %q", got)
	}

	attachErr := SelectAgentThreadDecision(AgentThreadSelectionInput{
		ThreadID:          rustThreadID3,
		LivenessAvailable: true,
		AttachAttempted:   true,
		AttachError:       errors.New(" transport disconnected "),
	})
	if attachErr.Proceed || attachErr.ErrorMessage != "Failed to attach to agent thread "+rustThreadID3+":  transport disconnected " {
		t.Fatalf("attach error decision = %#v", attachErr)
	}

	if decision := SelectAgentThreadDecision(AgentThreadSelectionInput{ThreadID: " " + rustThreadID1 + " "}); decision.Noop || decision.Proceed || decision.ErrorMessage != "" || decision.NoLongerAvailable {
		t.Fatalf("spaced thread decision = %#v, want zero", decision)
	}
}

func TestStartupThreadStartedDecisionMatchRust(t *testing.T) {
	stale := StartupThreadStartedDecisionForResult(false, rustThreadID1, nil)
	if !stale.IgnoreUnexpectedCompleted || stale.UnsubscribeStaleThreadID != rustThreadID1 || stale.DiscardStaleThreadID != rustThreadID1 {
		t.Fatalf("stale startup decision = %#v", stale)
	}

	canonical := StartupThreadStartedDecisionForResult(false, "00000000-0000-0000-0000-0000000000AA", nil)
	if canonical.UnsubscribeStaleThreadID != "00000000-0000-0000-0000-0000000000aa" || canonical.DiscardStaleThreadID != "00000000-0000-0000-0000-0000000000aa" {
		t.Fatalf("canonical stale startup decision = %#v", canonical)
	}
	spaced := StartupThreadStartedDecisionForResult(false, " "+rustThreadID1+" ", nil)
	if !spaced.IgnoreUnexpectedCompleted || spaced.UnsubscribeStaleThreadID != "" || spaced.DiscardStaleThreadID != "" {
		t.Fatalf("spaced stale startup decision = %#v", spaced)
	}

	ok := StartupThreadStartedDecisionForResult(true, rustThreadID2, nil)
	if !ok.PendingCleared || ok.QueueSubmissions == nil || *ok.QueueSubmissions || !ok.EnqueuePrimaryThread || !ok.MaybeSendQueuedInput || ok.ErrorMessage != "" {
		t.Fatalf("pending startup ok = %#v", ok)
	}

	failed := StartupThreadStartedDecisionForResult(true, "", errors.New(" boom "))
	if !failed.PendingCleared || failed.EnqueuePrimaryThread || failed.MaybeSendQueuedInput || failed.ErrorMessage != "Failed to start a fresh session through the app server:  boom " {
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

	upper := SessionSummaryForThread(codextui.TokenUsage{}, "123E4567-E89B-12D3-A456-426614174000", "", rolloutPath)
	if upper == nil || upper.ResumeHint != "codex resume 123e4567-e89b-12d3-a456-426614174000" {
		t.Fatalf("uppercase thread summary = %#v", upper)
	}

	spacedID := SessionSummaryForThread(codextui.TokenUsage{}, " 123e4567-e89b-12d3-a456-426614174000 ", "", rolloutPath)
	if spacedID != nil {
		t.Fatalf("spaced thread id summary = %#v, want nil", spacedID)
	}

	spacedName := " my-session "
	named = SessionSummaryForThread(codextui.TokenUsage{}, "123e4567-e89b-12d3-a456-426614174000", spacedName, rolloutPath)
	if named == nil || named.ResumeHint != "codex resume, then select "+spacedName+" (123e4567-e89b-12d3-a456-426614174000)" {
		t.Fatalf("spaced name summary = %#v", named)
	}

	spacedRolloutPath := filepath.Join(t.TempDir(), " rollout.jsonl ")
	if err := os.WriteFile(spacedRolloutPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write spaced rollout: %v", err)
	}
	spacedRollout := SessionSummaryForThread(codextui.TokenUsage{}, "123e4567-e89b-12d3-a456-426614174000", "", spacedRolloutPath)
	if spacedRollout == nil || spacedRollout.ResumeHint != "codex resume 123e4567-e89b-12d3-a456-426614174000" {
		t.Fatalf("spaced rollout path summary = %#v", spacedRollout)
	}
}

func TestApplyLoadedSubagentBackfillMatchesRust(t *testing.T) {
	nav := NewAgentNavigationState()
	loadedThreadID := "00000000-0000-0000-0000-0000000000a0"
	ApplyLoadedSubagentBackfill(nav, []LoadedSubagentThread{
		{ThreadID: loadedThreadID, AgentNickname: "A", AgentRole: "builder", AgentPath: "agents/a.md"},
		{ThreadID: " " + loadedThreadID + " ", AgentNickname: "Bad", AgentRole: "builder", AgentPath: "agents/bad.md"},
	})
	entry, ok := nav.Get(loadedThreadID)
	if !ok || entry.AgentNickname != "A" || entry.AgentRole != "builder" || entry.AgentPath != "agents/a.md" || entry.IsClosed {
		t.Fatalf("backfilled entry = %#v ok=%v", entry, ok)
	}
	if got := nav.TrackedThreadIDs(); len(got) != 1 || got[0] != loadedThreadID {
		t.Fatalf("tracked backfilled threads = %#v", got)
	}
}
