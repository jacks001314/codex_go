package chatwidget

import (
	"strings"
	"testing"
)

func TestModelPopupsBuildQuickAllAndReasoningViews(t *testing.T) {
	presets := []ModelPopupPreset{
		{Model: "gpt-hidden", ShowInPicker: false},
		{Model: "codex-auto-thorough", Description: "deep", ShowInPicker: true},
		{Model: "codex-auto-fast", Description: "quick", ShowInPicker: true, IsDefault: true},
		{
			Model:                  "gpt-5.1-codex",
			Description:            "full picker",
			ShowInPicker:           true,
			DefaultReasoningEffort: "medium",
			SupportedReasoningEfforts: []ReasoningEffortPopupOption{
				{Effort: "medium", Description: "balanced"},
				{Effort: "high", Description: "deep"},
			},
		},
	}
	config := ModelPopupConfig{
		CurrentModel:           "codex-auto-fast",
		CurrentReasoningEffort: "medium",
		CustomOpenAIBaseURL:    "https://proxy.example/v1",
	}
	result := NewModelPopupView(config, presets)
	if result.InfoMessage != "" {
		t.Fatalf("NewModelPopupView info = %q", result.InfoMessage)
	}
	if result.View.ViewID != ModelSelectionViewID || result.View.Title != "Select Model" {
		t.Fatalf("model view = %#v", result.View)
	}
	if len(result.View.Items) != 3 || result.View.Items[0].ID != "codex-auto-fast" || result.View.Items[2].Action != ModelMenuActionOpenAllModels {
		t.Fatalf("model items = %#v", result.View.Items)
	}
	if len(result.View.HeaderLines) != 1 || !strings.Contains(result.View.HeaderLines[0], "base URL is overridden") {
		t.Fatalf("warning lines = %#v", result.View.HeaderLines)
	}

	all := NewAllModelsPopupView(config, presets[2:])
	if all.View.ViewID != AllModelsSelectionViewID || !all.View.Searchable {
		t.Fatalf("all models view = %#v", all.View)
	}
	reasoning := NewReasoningPopupView(config, presets[3])
	if reasoning.View.ViewID != ReasoningSelectionViewID || len(reasoning.View.Items) != 2 {
		t.Fatalf("reasoning view = %#v", reasoning.View)
	}
	if reasoning.View.Items[1].SelectedDescription == "" {
		t.Fatalf("expected high reasoning warning, got %#v", reasoning.View.Items[1])
	}
	if !ShouldPromptPlanReasoningScope(ModelPopupConfig{
		CurrentModel:               "gpt-5",
		CurrentReasoningEffort:     "medium",
		CurrentPlanReasoningEffort: "low",
		PlanMode:                   true,
		CollaborationModesEnabled:  true,
	}, "gpt-5", "high") {
		t.Fatal("expected plan reasoning scope prompt")
	}
}

func TestMcpStartupRoundStateTracksHeadersAndFinish(t *testing.T) {
	state := NewMcpStartupRoundState([]string{"docs", "db"})
	first := state.Update("docs", McpStartupStatus{Kind: McpStartupStarting}, true)
	if first.Header != MCPStartupSingleHeaderPrefix+" docs" || !first.Active {
		t.Fatalf("first update = %#v", first)
	}
	second := state.Update("db", McpStartupStatus{Kind: McpStartupStarting}, true)
	if second.Header != MCPStartupMultiHeaderPrefix+" (0/2): db, docs" {
		t.Fatalf("second header = %q", second.Header)
	}
	ready := state.Update("docs", McpStartupStatus{Kind: McpStartupReady}, true)
	if ready.Header != MCPStartupMultiHeaderPrefix+" (1/2): db" {
		t.Fatalf("ready header = %q", ready.Header)
	}
	finished := state.Update("db", McpStartupStatus{Kind: McpStartupFailed, Error: "db failed"}, true)
	if !finished.Finished || len(finished.Failed) != 1 || finished.Failed[0] != "db" {
		t.Fatalf("finished = %#v", finished)
	}
	if len(finished.Warnings) < 2 || !strings.Contains(strings.Join(finished.Warnings, "\n"), "MCP startup incomplete") {
		t.Fatalf("warnings = %#v", finished.Warnings)
	}
}

func TestInputRestoreAndCancelEditState(t *testing.T) {
	queue := InputQueueState{
		QueuedUserMessages: []QueuedUserMessage{
			NewQueuedUserMessage(NewUserMessage("first"), QueuedInputPlain),
			{UserMessage: NewUserMessage("second"), Action: QueuedInputPlain, PendingPastes: [][2]string{{"p1", "body"}}},
		},
		QueuedUserMessageHistoryRecords: []UserMessageHistoryRecord{
			UserMessageTextHistoryRecord(),
			UserMessageOverrideHistoryRecord("override second"),
		},
	}
	latest, ok := queue.PopLatestQueuedComposerState()
	if !ok || latest.Text != "override second" || len(latest.PendingPastes) != 1 {
		t.Fatalf("latest composer = %#v ok=%v", latest, ok)
	}
	next, record, ok := queue.PopNextQueuedUserMessage()
	if !ok || next.UserMessage.Text != "first" || record.Kind != UserMessageHistoryText {
		t.Fatalf("next = %#v record=%#v ok=%v", next, record, ok)
	}

	cancel := CancelEditState{}
	cancel.RecordCancelEditCandidate(NewUserMessage("restore me"))
	cancel.Arm(true, InputQueueState{}, false)
	restored, ok := cancel.TakeArmedPrompt(TurnAbortInterrupted)
	if !ok || restored.Text != "restore me" {
		t.Fatalf("cancel restore = %#v ok=%v", restored, ok)
	}
}

func TestInterruptManagerMatchesResolvedPrompts(t *testing.T) {
	manager := NewInterruptManager()
	manager.PushExecApproval("call-1", "approval-1", nil)
	manager.Push(QueuedInterrupt{Kind: QueuedInterruptRequestUserInput, ID: "input-1"})
	if !manager.RemoveResolvedPrompt(ResolvedPrompt{Kind: QueuedInterruptExecApproval, ID: "approval-1"}) {
		t.Fatal("expected exec approval removal by approval id")
	}
	if manager.Len() != 1 {
		t.Fatalf("queue len = %d", manager.Len())
	}
	if !manager.RemoveResolvedPrompt(ResolvedPrompt{Kind: QueuedInterruptRequestUserInput, ID: "input-1"}) || !manager.IsEmpty() {
		t.Fatalf("user input removal failed, len=%d", manager.Len())
	}
}

func TestPlanImplementationViewAndPrompt(t *testing.T) {
	view := NewPlanImplementationView(PlanImplementationConfig{
		DefaultModeAvailable:   true,
		PlanMarkdown:           "# Plan",
		ClearContextUsageLabel: "89% used",
	})
	if view.ViewID != PlanImplementationViewID || len(view.Items) != 3 {
		t.Fatalf("view = %#v", view)
	}
	if view.Items[1].Disabled || !strings.Contains(view.Items[1].Description, "89% used") {
		t.Fatalf("clear context item = %#v", view.Items[1])
	}
	prompt, ok := PlanImplementationClearContextPrompt("# Plan")
	if !ok || !strings.Contains(prompt, PlanImplementationClearPrefix) || !strings.Contains(prompt, "# Plan") {
		t.Fatalf("prompt = %q ok=%v", prompt, ok)
	}

	blocked := NewPlanImplementationView(PlanImplementationConfig{})
	if !blocked.Items[0].Disabled || blocked.Items[0].DisabledReason != PlanImplementationDefaultBlock {
		t.Fatalf("blocked implement item = %#v", blocked.Items[0])
	}
}

func TestSafetyBufferingStateShowsRetryOnce(t *testing.T) {
	state := SafetyBufferingState{}
	state.RecordTurn("turn-1")
	result := state.Apply(SafetyBufferingUpdate{
		TurnID:          "turn-1",
		ShowBufferingUI: true,
		FasterModel:     "codex-auto-fast",
		CanRetry:        true,
	})
	if result.Prompt == nil || result.Prompt.ViewID != SafetyBufferingPromptViewID || !result.RetryAvailable {
		t.Fatalf("first safety result = %#v", result)
	}
	again := state.Apply(SafetyBufferingUpdate{
		TurnID:          "turn-1",
		ShowBufferingUI: true,
		FasterModel:     "codex-auto-fast",
		CanRetry:        true,
	})
	if again.Prompt != nil || !again.Waiting {
		t.Fatalf("second safety result = %#v", again)
	}
	cleared := state.Apply(SafetyBufferingUpdate{TurnID: "turn-1"})
	if !cleared.Cleared || state.IsWaiting() {
		t.Fatalf("cleared = %#v state=%#v", cleared, state)
	}
}

func TestPetsPickerView(t *testing.T) {
	result := NewPetsPickerView("off", PetImageSupport{Kind: PetImageSupported}, []PetOption{{ID: "default", Name: "Default"}})
	if result.InfoMessage != "" || result.View.ViewID != PetsPickerViewID {
		t.Fatalf("pets result = %#v", result)
	}
	if len(result.View.Items) != 2 || !result.View.Items[0].IsCurrent || result.View.Items[1].ID != DefaultPetID {
		t.Fatalf("pet items = %#v", result.View.Items)
	}
	unsupported := NewPetsPickerView("", PetImageSupport{Kind: PetImageTerminal}, nil)
	if !strings.Contains(unsupported.InfoMessage, "terminal image protocol") {
		t.Fatalf("unsupported = %#v", unsupported)
	}
}
