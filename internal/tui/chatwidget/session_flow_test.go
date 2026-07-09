package chatwidget

import "testing"

func TestSessionFlowConfigureNormalMatchesRustCore(t *testing.T) {
	state := SessionFlowState{
		ThreadID:              "old-thread",
		QueueSubmissions:      true,
		InitialMessagePending: true,
		ConnectorsEnabled:     true,
	}
	result := state.ConfigureSession(ThreadSessionSnapshot{
		ThreadID:                "thread-1",
		ThreadName:              "Work",
		MessageHistory:          MessageHistoryMetadata{LogID: "log-1", EntryCount: 12},
		NetworkProxy:            "proxy",
		ForkedFromID:            "parent-1",
		ForkParentTitle:         "Parent",
		RolloutPath:             "rollout.jsonl",
		CWD:                     "/repo",
		RuntimeWorkspaceRoots:   []string{"/repo", "/repo/lib"},
		Model:                   "gpt-test",
		ReasoningEffort:         "high",
		CollaborationMode:       "plan",
		ServiceTier:             "fast",
		ApprovalPolicy:          "on-request",
		PermissionProfile:       "workspace-write",
		ActivePermissionProfile: "custom",
		ApprovalsReviewer:       "guardian",
		Personality:             "concise",
		InstructionSourcePaths:  []string{"AGENTS.md"},
	}, SessionConfiguredDisplayNormal)

	if !state.SessionConfigured || state.ThreadID != "thread-1" || state.ThreadName != "Work" || state.QueueSubmissions {
		t.Fatalf("state after configure = %#v", state)
	}
	if state.RecentAutoReviewDenialsResetCount != 1 || state.HistoryLogID != "log-1" || state.HistoryEntryCount != 12 {
		t.Fatalf("metadata/reset = %#v", state)
	}
	if !result.ThreadChanged || !result.ResetCopyHistory || !result.ResetTurnLifecycle || !result.ClearSafetyBuffering || !result.ClearGoalStatus {
		t.Fatalf("reset result = %#v", result)
	}
	if !result.ShowSessionHeader || result.ClearActiveSessionHeader || !result.SubmitInitialMessage || !result.PrefetchConnectors || !result.RequestRedraw {
		t.Fatalf("display result = %#v", result)
	}
	if !result.EmitForkEvent || result.ForkEventLine != "• Thread forked from Parent (parent-1)" {
		t.Fatalf("fork result = %#v", result)
	}
	if state.InitialMessagePending {
		t.Fatal("initial message should be drained")
	}
}

func TestSessionFlowQuietAndSideClearActiveHeaderMatchRustCore(t *testing.T) {
	state := SessionFlowState{ActiveSessionHeader: true, InitialMessagePending: true}
	quiet := state.ConfigureSession(ThreadSessionSnapshot{ThreadID: "thread-quiet"}, SessionConfiguredDisplayQuiet)
	if quiet.ShowSessionHeader || !quiet.ClearActiveSessionHeader || !quiet.SubmitInitialMessage {
		t.Fatalf("quiet result = %#v", quiet)
	}
	if state.ActiveSessionHeader {
		t.Fatal("quiet configure should clear active header")
	}

	state.ActiveSessionHeader = true
	side := state.ConfigureSession(ThreadSessionSnapshot{ThreadID: "thread-side", ForkedFromID: "parent"}, SessionConfiguredDisplaySideConversation)
	if side.ShowSessionHeader || !side.ClearActiveSessionHeader || side.EmitForkEvent {
		t.Fatalf("side result = %#v", side)
	}
}

func TestSessionFlowThreadNameUpdateMatchesRustCore(t *testing.T) {
	state := SessionFlowState{ThreadID: "thread-1"}
	name := " Renamed "
	result := state.OnThreadNameUpdated("other", &name)
	if result.Applied {
		t.Fatalf("other thread update applied: %#v", result)
	}

	result = state.OnThreadNameUpdated("thread-1", &name)
	if !result.Applied || state.ThreadName != "Renamed" || result.ConfirmationMessage != "Thread renamed to Renamed." {
		t.Fatalf("name update result=%#v state=%#v", result, state)
	}
	if !result.RefreshStatusSurfaces || !result.RequestRedraw || !result.MaybeSendQueuedInput {
		t.Fatalf("name update side effects = %#v", result)
	}

	result = state.OnThreadNameUpdated("thread-1", nil)
	if !result.Applied || state.ThreadName != "" || result.ConfirmationMessage != "" {
		t.Fatalf("clear name result=%#v state=%#v", result, state)
	}
}

func TestSessionFlowCompatibilityAndForkLineHelpers(t *testing.T) {
	var state SessionFlowState
	state.Configure("thread-1")
	if !state.CanSubmitInitialMessage() {
		t.Fatal("configured session should accept initial message")
	}
	state.MarkShutdownComplete()
	if state.CanSubmitInitialMessage() {
		t.Fatal("shutdown session should not accept initial message")
	}
	if got := ForkedThreadEventLine("parent-1", ""); got != "• Thread forked from parent-1" {
		t.Fatalf("fork line = %q", got)
	}
	if got := RenameConfirmationText(" "); got != "" {
		t.Fatalf("empty rename confirmation = %q", got)
	}
}

func TestSessionFlowInitialMessageSuppressionAndSandboxGateMatchRust(t *testing.T) {
	state := SessionFlowState{
		InitialMessagePending:               true,
		ElevatedWindowsSandboxSetupRequired: true,
	}

	result := state.ConfigureSession(ThreadSessionSnapshot{ThreadID: "thread-1"}, SessionConfiguredDisplayNormal)

	if result.SubmitInitialMessage || !state.InitialMessagePending {
		t.Fatalf("sandbox setup should defer initial prompt: result=%#v state=%#v", result, state)
	}
	state.ElevatedWindowsSandboxSetupRequired = false
	if !state.SubmitInitialUserMessageIfPending() || state.InitialMessagePending {
		t.Fatalf("initial prompt should submit after setup gate clears: %#v", state)
	}

	state.InitialMessagePending = true
	state.SetInitialUserMessageSubmitSuppressed(true)
	if state.SubmitInitialUserMessageIfPending() || !state.InitialMessagePending {
		t.Fatalf("suppression should hold initial prompt: %#v", state)
	}
	state.SetInitialUserMessageSubmitSuppressed(false)
	if !state.SubmitInitialUserMessageIfPending() {
		t.Fatalf("initial prompt should submit after suppression clears")
	}
}
