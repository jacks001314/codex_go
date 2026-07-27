package chatwidget

import "testing"

func TestProtocolDecisionAndRequestClassification(t *testing.T) {
	ignored := DecideProtocolNotification("parent", ProtocolNotification{
		Kind:     NotificationMcpServerStatusUpdated,
		ThreadID: "child",
	}, ReplayNone)
	if ignored.Handle || ignored.Reason != "misrouted_mcp_status" {
		t.Fatalf("ignored = %#v", ignored)
	}
	handled := DecideProtocolNotification("parent", ProtocolNotification{Kind: NotificationError, WillRetry: true}, ReplayNone)
	if !handled.Handle || handled.RestoreRetryStatusHeader {
		t.Fatalf("handled retry error = %#v", handled)
	}
	request := ClassifyProtocolRequest(ProtocolRequest{Kind: RequestToolUserInput, ID: "input-1"}, ReplayNone)
	if request.Surface != RequestSurfaceUserInput || request.Queue == nil || request.Queue.Kind != QueuedInterruptRequestUserInput {
		t.Fatalf("request = %#v", request)
	}
	unsupported := ClassifyProtocolRequest(ProtocolRequest{Kind: RequestDynamicToolCall}, ReplayNone)
	if unsupported.Surface != RequestSurfaceUnsupported || unsupported.Error == "" {
		t.Fatalf("unsupported = %#v", unsupported)
	}
}

func TestSubmissionAndInputFlowDecisions(t *testing.T) {
	queued := DecideUserMessageSubmission(NewUserMessage("hello"), UserMessageTextHistoryRecord(), SubmissionOptions{})
	if !queued.QueueUntilConfigured || !queued.Accepted {
		t.Fatalf("queued = %#v", queued)
	}
	help := DecideUserMessageSubmission(NewUserMessage("!"), UserMessageTextHistoryRecord(), SubmissionOptions{
		SessionConfigured:     true,
		CurrentModelHasImages: true,
		ShellEscapePolicy:     ShellEscapeAllow,
	})
	if help.InfoTitle != UserShellCommandHelpTitle || help.QueueDrain != QueueDrainContinue {
		t.Fatalf("help = %#v", help)
	}
	shell := SubmitQueuedShellPrompt(NewUserMessage("! go test ./..."))
	if shell.ShellCommand != "go test ./..." || shell.QueueDrain != QueueDrainStop {
		t.Fatalf("shell = %#v", shell)
	}
	flow := InputFlowState{SessionConfigured: true, TaskRunning: true}
	if got := flow.Decide(NewUserMessage("next"), SubmissionOptions{SessionConfigured: true, CurrentModelHasImages: true}); got != InputFlowQueue {
		t.Fatalf("flow = %q", got)
	}
}

func TestIdeContextStateCommands(t *testing.T) {
	state := IdeContextState{}
	on := state.HandleCommand("on", true, true, "")
	if !on.Enabled || on.Message != "IDE context is on." || on.Hint == "" {
		t.Fatalf("on = %#v", on)
	}
	skipped, ok := state.MarkPromptFetchSkipped("no editor")
	if !ok || skipped.Message != "IDE context was skipped for this message." {
		t.Fatalf("skipped = %#v ok=%v", skipped, ok)
	}
	if _, ok := state.MarkPromptFetchSkipped("again"); ok {
		t.Fatal("second skipped warning should be suppressed")
	}
	off := state.HandleCommand("off", true, false, "")
	if off.Enabled || off.Message != "IDE context is off." {
		t.Fatalf("off = %#v", off)
	}
}

func TestKeymapServiceTierAndExecLifecycleInterfaces(t *testing.T) {
	keymap := NewKeymapPickerView(KeymapPickerConfig{Items: []KeymapActionItem{{
		Context:     "chat",
		Action:      "copy",
		Description: "Copy last response",
		Bindings:    []string{"ctrl+o"},
	}}})
	if keymap.ViewID != KeymapPickerViewID || len(keymap.Items) != 2 || !keymap.Searchable {
		t.Fatalf("keymap = %#v", keymap)
	}
	menu := NewKeymapActionMenuView(KeymapActionItem{Action: "copy", Bindings: []string{"ctrl+o"}})
	remove, ok := selectionItemByIDRustInterfaces(menu.Items, "unset")
	if menu.ViewID != KeymapActionMenuViewID || !ok || remove.Disabled {
		t.Fatalf("keymap action menu = %#v", menu)
	}
	emptyMenu := NewKeymapActionMenuView(KeymapActionItem{Action: "copy"})
	emptyRemove, ok := selectionItemByIDRustInterfaces(emptyMenu.Items, "unset")
	if !ok || !emptyRemove.Disabled {
		t.Fatalf("empty keymap action menu = %#v", emptyMenu)
	}

	tier := ServiceTierState{
		EffectiveServiceTier: ServiceTierFastRequestValue,
		HasChatGPTAccount:    true,
		FastModeEnabled:      true,
		ModelServiceTiers:    []ServiceTierCommand{{ID: ServiceTierFastRequestValue, Name: "Fast"}},
	}
	if !tier.ShouldShowFastStatus() || !tier.CanToggleFastModeFromKeybinding() {
		t.Fatalf("tier = %#v", tier)
	}
	if next := tier.NextServiceTierForToggle(ServiceTierCommand{ID: ServiceTierFastRequestValue}); next != ServiceTierDefaultRequestValue {
		t.Fatalf("next tier = %q", next)
	}

	lifecycle := CommandLifecycleState{}
	lifecycle.TrackUnifiedExecProcessBegin("call", "proc", "go test")
	lifecycle.TrackUnifiedExecOutputChunk("call", "ok\n", 1)
	if got := lifecycle.FooterCommands(); len(got) != 1 || got[0] != "go test" {
		t.Fatalf("footer = %#v", got)
	}
	if !lifecycle.TrackUnifiedExecProcessEnd("call", "proc") || len(lifecycle.UnifiedExecProcesses) != 0 {
		t.Fatalf("lifecycle = %#v", lifecycle)
	}
}

func selectionItemByIDRustInterfaces(items []SelectionItem, id string) (SelectionItem, bool) {
	for _, item := range items {
		if item.ID == id {
			return item, true
		}
	}
	return SelectionItem{}, false
}

func TestLifecycleHooksReplayRenderingAndStreaming(t *testing.T) {
	hooks := NewHooksBrowserView([]HookRun{{ID: "h1", Name: "pre", Command: "echo ok", Status: HookStatusCompleted}})
	if hooks.ViewID != HooksBrowserViewID || len(hooks.Items) != 1 || hooks.Items[0].Name != "pre" {
		t.Fatalf("hooks = %#v", hooks)
	}
	turns := TurnLifecycleState{}
	turns.Start("turn-1")
	if !turns.AgentTurnRunning || !turns.Complete("turn-1") || turns.AgentTurnRunning {
		t.Fatalf("turns = %#v", turns)
	}
	tools := ToolLifecycleState{}
	tools.Start(ToolLifecycleItem{ID: "tool-1", Name: "exec"})
	if !tools.Finish("tool-1", ToolLifecycleCompleted) || len(tools.Active) != 0 {
		t.Fatalf("tools = %#v", tools)
	}
	if !ShouldSuppressDuringReplay(ReplayResumeInitialMessages, NotificationTurnStarted) {
		t.Fatal("expected replay suppression")
	}
	render := ChatWidgetRenderState{Width: 10, TranscriptRows: []string{"a", "b", "c"}}
	if render.HistoryWrapWidth(12) != 1 || len(render.CompactTranscript(2)) != 2 {
		t.Fatalf("render = %#v", render)
	}
	stream := ChatStreamingState{}
	stream.OnAgentMessageDelta("hi")
	stream.OnPlanDelta("plan")
	if !stream.HasActivity() || stream.MessageDeltaCount != 1 || stream.PlanDeltaCount != 1 {
		t.Fatalf("stream = %#v", stream)
	}
}
