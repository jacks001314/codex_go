package chatwidget

import (
	"reflect"
	"strings"
	"testing"

	codextui "codex_go/tui"
	bottompane "codex_go/tui/bottom_pane"
	"codex_go/turn"
)

func TestQueuedSlashPromptDispatchesInlineArgsAndRebasesElementsMatchRust(t *testing.T) {
	placeholder := "$file"
	text := "/review   inspect $file please"
	elementStart := strings.Index(text, "$file")
	elementEnd := elementStart + len("$file")
	queued := NewQueuedUserMessage(UserMessage{
		Text: text,
		TextElements: []turn.TextElement{{
			ByteRange:   turn.ByteRange{Start: uint(elementStart), End: uint(elementEnd)},
			Placeholder: &placeholder,
		}},
		LocalImages:     []string{"one.png"},
		RemoteImageURLs: []string{"https://example.test/a.png"},
		MentionBindings: []string{"file"},
	}, QueuedInputParseSlash)

	decision := DecideQueuedSlashPrompt(queued, slashDispatchTestFlags(), nil, false, false)
	if decision.Kind != QueuedSlashDispatchCommandWithArgs || decision.Command != codextui.CommandReview || decision.Args != "inspect $file please" || decision.QueueDrain != QueueDrainStop {
		t.Fatalf("decision = %#v", decision)
	}
	wantElements := []turn.TextElement{{
		ByteRange:   turn.ByteRange{Start: uint(len("inspect ")), End: uint(len("inspect $file"))},
		Placeholder: &placeholder,
	}}
	if !reflect.DeepEqual(decision.TextElements, wantElements) {
		t.Fatalf("text elements = %#v, want %#v", decision.TextElements, wantElements)
	}
	if !reflect.DeepEqual(decision.Message.LocalImages, []string{"one.png"}) ||
		!reflect.DeepEqual(decision.Message.RemoteImageURLs, []string{"https://example.test/a.png"}) ||
		!reflect.DeepEqual(decision.Message.MentionBindings, []string{"file"}) {
		t.Fatalf("message payload = %#v", decision.Message)
	}
}

func TestQueuedSlashPromptBareCommandServiceTierUnknownAndSubmitMatchRust(t *testing.T) {
	flags := slashDispatchTestFlags()
	raw := DecideQueuedSlashPrompt(NewQueuedUserMessage(NewUserMessage("/raw"), QueuedInputParseSlash), flags, nil, false, false)
	if raw.Kind != QueuedSlashDispatchCommand || raw.Command != codextui.CommandRaw || raw.QueueDrain != QueueDrainContinue {
		t.Fatalf("raw decision = %#v", raw)
	}

	busyRaw := DecideQueuedSlashPrompt(NewQueuedUserMessage(NewUserMessage("/raw"), QueuedInputParseSlash), flags, nil, true, false)
	if busyRaw.QueueDrain != QueueDrainStop {
		t.Fatalf("busy raw queue drain = %#v", busyRaw)
	}

	service := bottompane.ServiceTierCommand{ID: ServiceTierFastRequestValue, Name: "fast", Description: "fast mode"}
	serviceDecision := DecideQueuedSlashPrompt(NewQueuedUserMessage(NewUserMessage("/fast"), QueuedInputParseSlash), flags, []bottompane.ServiceTierCommand{service}, false, false)
	if serviceDecision.Kind != QueuedSlashDispatchServiceTier || serviceDecision.ServiceTier == nil || serviceDecision.ServiceTier.ID != ServiceTierFastRequestValue || serviceDecision.QueueDrain != QueueDrainContinue {
		t.Fatalf("service decision = %#v", serviceDecision)
	}

	unknown := DecideQueuedSlashPrompt(NewQueuedUserMessage(NewUserMessage("/wat"), QueuedInputParseSlash), flags, nil, false, false)
	if unknown.Kind != QueuedSlashDispatchUnrecognized || unknown.QueueDrain != QueueDrainContinue || !strings.Contains(unknown.InfoMessage, "Unrecognized command '/wat'") {
		t.Fatalf("unknown decision = %#v", unknown)
	}

	clearWithArgs := DecideQueuedSlashPrompt(NewQueuedUserMessage(NewUserMessage("/clear now"), QueuedInputParseSlash), flags, nil, false, false)
	if clearWithArgs.Kind != QueuedSlashDispatchSubmit || clearWithArgs.Message.Text != "/clear now" || clearWithArgs.QueueDrain != QueueDrainStop {
		t.Fatalf("clear with args decision = %#v", clearWithArgs)
	}
}

func TestSlashCommandCapabilityTablesMatchRust(t *testing.T) {
	for _, command := range []codextui.Command{codextui.CommandReview, codextui.CommandRename, codextui.CommandPlan, codextui.CommandGoal, codextui.CommandIde, codextui.CommandKeymap, codextui.CommandMcp, codextui.CommandRaw, codextui.CommandUsage, codextui.CommandPets, codextui.CommandSide, codextui.CommandResume, codextui.CommandSandboxReadRoot} {
		if !CommandSupportsInlineArgs(command) {
			t.Fatalf("%s should support inline args", command)
		}
	}
	for _, command := range []codextui.Command{codextui.CommandGoal, codextui.CommandIde, codextui.CommandTitle, codextui.CommandStatusline, codextui.CommandRaw, codextui.CommandApp} {
		if !CommandAvailableDuringTask(command) {
			t.Fatalf("%s should be available during task", command)
		}
	}
	for _, command := range []codextui.Command{codextui.CommandPlan, codextui.CommandPets, codextui.CommandReview, codextui.CommandClear} {
		if CommandAvailableDuringTask(command) {
			t.Fatalf("%s should be blocked during task", command)
		}
	}
	for _, command := range []codextui.Command{codextui.CommandCopy, codextui.CommandRaw, codextui.CommandDiff, codextui.CommandMention, codextui.CommandStatus, codextui.CommandUsage, codextui.CommandIde} {
		if !CommandAvailableInSideConversation(command) {
			t.Fatalf("%s should be side-conversation available", command)
		}
	}
}

func TestSlashCommandGuardsMatchRust(t *testing.T) {
	sideBlocked := GuardSlashCommandDispatch(SlashCommandGuardContext{
		Command:                codextui.CommandReview,
		CommandName:            "review",
		ActiveSideConversation: true,
	})
	if sideBlocked.Allowed || sideBlocked.ErrorMessage != "'/review' is unavailable in side conversations. "+SideSlashCommandUnavailableHint || !sideBlocked.DrainPendingSubmission || sideBlocked.RequestRedraw {
		t.Fatalf("side blocked = %#v", sideBlocked)
	}

	reviewSide := GuardSlashCommandDispatch(SlashCommandGuardContext{
		Command:     codextui.CommandSide,
		CommandName: "side",
		ReviewMode:  true,
	})
	if reviewSide.Allowed || reviewSide.ErrorMessage != "'/side' is unavailable while code review is running." || !reviewSide.DrainPendingSubmission {
		t.Fatalf("review side = %#v", reviewSide)
	}
	reviewBtw := GuardSlashCommandDispatch(SlashCommandGuardContext{
		Command:     codextui.CommandSide,
		CommandName: "btw",
		ReviewMode:  true,
	})
	if reviewBtw.Allowed || reviewBtw.ErrorMessage != "'/btw' is unavailable while code review is running." {
		t.Fatalf("review btw = %#v", reviewBtw)
	}

	runningPlan := GuardSlashCommandDispatch(SlashCommandGuardContext{
		Command:     codextui.CommandPlan,
		CommandName: "plan",
		TaskRunning: true,
	})
	if runningPlan.Allowed || runningPlan.ErrorMessage != "'/plan' is disabled while a task is in progress." || !runningPlan.DrainPendingSubmission || !runningPlan.RequestRedraw {
		t.Fatalf("running plan = %#v", runningPlan)
	}
	runningRaw := GuardSlashCommandDispatch(SlashCommandGuardContext{
		Command:     codextui.CommandRaw,
		CommandName: "raw",
		TaskRunning: true,
	})
	if !runningRaw.Allowed {
		t.Fatalf("raw should be allowed during task: %#v", runningRaw)
	}
	pendingResume := GuardSlashCommandDispatch(SlashCommandGuardContext{
		Command:            codextui.CommandResume,
		CommandName:        "resume",
		ResumePendingStart: true,
	})
	if pendingResume.Allowed || !pendingResume.RequestRedraw {
		t.Fatalf("pending resume = %#v", pendingResume)
	}
}

func TestPreparedSlashArgsUsageMcpKeymapRawMatchRust(t *testing.T) {
	usageLogin := DispatchPreparedSlashArgs(PreparedSlashArgsContext{
		Command: codextui.CommandUsage,
		Args:    "daily",
		Source:  SlashCommandDispatchLive,
	})
	if usageLogin.Action != PreparedSlashArgsError || usageLogin.ErrorMessage != UsageChatGPTLoginRequired || !usageLogin.DrainPendingSubmission {
		t.Fatalf("usage login = %#v", usageLogin)
	}

	usage := DispatchPreparedSlashArgs(PreparedSlashArgsContext{
		Command:             codextui.CommandUsage,
		Args:                "weekly",
		HasCodexBackendAuth: true,
	})
	if usage.Action != PreparedSlashArgsShowTokenActivity || usage.TokenActivityView != TokenActivityWeekly {
		t.Fatalf("usage = %#v", usage)
	}

	badUsage := DispatchPreparedSlashArgs(PreparedSlashArgsContext{
		Command:             codextui.CommandUsage,
		Args:                "year",
		HasCodexBackendAuth: true,
	})
	if badUsage.Action != PreparedSlashArgsError || badUsage.ErrorMessage != "Usage: /usage [daily|weekly|cumulative]" {
		t.Fatalf("bad usage = %#v", badUsage)
	}

	mcp := DispatchPreparedSlashArgs(PreparedSlashArgsContext{Command: codextui.CommandMcp, Args: "verbose"})
	if mcp.Action != PreparedSlashArgsShowMcpVerbose {
		t.Fatalf("mcp = %#v", mcp)
	}
	keymap := DispatchPreparedSlashArgs(PreparedSlashArgsContext{Command: codextui.CommandKeymap, Args: "debug"})
	if keymap.Action != PreparedSlashArgsOpenKeymapDebug {
		t.Fatalf("keymap = %#v", keymap)
	}
	raw := DispatchPreparedSlashArgs(PreparedSlashArgsContext{Command: codextui.CommandRaw, Args: "off"})
	if raw.Action != PreparedSlashArgsSetRawOutput || raw.RawOutputEnabled == nil || *raw.RawOutputEnabled {
		t.Fatalf("raw = %#v", raw)
	}
	badRaw := DispatchPreparedSlashArgs(PreparedSlashArgsContext{Command: codextui.CommandRaw, Args: "maybe"})
	if badRaw.Action != PreparedSlashArgsError || badRaw.ErrorMessage != RawUsageText {
		t.Fatalf("bad raw = %#v", badRaw)
	}
}

func TestPreparedSlashArgsPlanGoalAndSideMatchRust(t *testing.T) {
	planDisabled := DispatchPreparedSlashArgs(PreparedSlashArgsContext{
		Command: codextui.CommandPlan,
		Args:    "write a plan",
	})
	if planDisabled.Action != PreparedSlashArgsInfo || planDisabled.InfoMessage != "Collaboration modes are disabled." {
		t.Fatalf("plan disabled = %#v", planDisabled)
	}

	planQueued := DispatchPreparedSlashArgs(PreparedSlashArgsContext{
		Command:                   codextui.CommandPlan,
		Args:                      "write a plan",
		CollaborationModesEnabled: true,
		PlanModeAvailable:         true,
		SessionConfigured:         false,
	})
	if planQueued.Action != PreparedSlashArgsQueuePlanMessage || planQueued.Message.Text != "write a plan" {
		t.Fatalf("plan queued = %#v", planQueued)
	}

	goalLiveBeforeSession := DispatchPreparedSlashArgs(PreparedSlashArgsContext{
		Command:      codextui.CommandGoal,
		Args:         "ship parity",
		Source:       SlashCommandDispatchLive,
		GoalsEnabled: true,
	})
	if goalLiveBeforeSession.Action != PreparedSlashArgsGoalQueueBeforeSession || goalLiveBeforeSession.Message.Text != "/goal ship parity" || !goalLiveBeforeSession.ClearLiveGoalSubmission {
		t.Fatalf("goal live before session = %#v", goalLiveBeforeSession)
	}

	goalQueuedBeforeSession := DispatchPreparedSlashArgs(PreparedSlashArgsContext{
		Command:      codextui.CommandGoal,
		Args:         "ship parity",
		Source:       SlashCommandDispatchQueued,
		GoalsEnabled: true,
	})
	if goalQueuedBeforeSession.Action != PreparedSlashArgsInfo || goalQueuedBeforeSession.InfoMessage != GoalUsageText || !strings.Contains(goalQueuedBeforeSession.Hint, "set a goal") {
		t.Fatalf("goal queued before session = %#v", goalQueuedBeforeSession)
	}

	goalPauseBeforeSession := DispatchPreparedSlashArgs(PreparedSlashArgsContext{
		Command:      codextui.CommandGoal,
		Args:         "pause",
		Source:       SlashCommandDispatchLive,
		GoalsEnabled: true,
	})
	if goalPauseBeforeSession.Action != PreparedSlashArgsInfo || goalPauseBeforeSession.InfoMessage != GoalUsageText || !goalPauseBeforeSession.ClearLiveGoalSubmission {
		t.Fatalf("goal pause before session = %#v", goalPauseBeforeSession)
	}

	goalPause := DispatchPreparedSlashArgs(PreparedSlashArgsContext{
		Command:      codextui.CommandGoal,
		Args:         "pause",
		Source:       SlashCommandDispatchLive,
		GoalsEnabled: true,
		ThreadID:     "thread-1",
	})
	if goalPause.Action != PreparedSlashArgsGoalPause || goalPause.GoalStatus != "paused" || !goalPause.ClearLiveGoalSubmission {
		t.Fatalf("goal pause = %#v", goalPause)
	}

	side := DispatchPreparedSlashArgs(PreparedSlashArgsContext{
		Command:  codextui.CommandSide,
		Args:     "quick question",
		ThreadID: "thread-1",
	})
	if side.Action != PreparedSlashArgsStartSideConversation || side.SideContextLabel != SideStartingContextLabel || side.Message.Text != "quick question" {
		t.Fatalf("side = %#v", side)
	}
	missingSide := DispatchPreparedSlashArgs(PreparedSlashArgsContext{Command: codextui.CommandSide, Args: "quick question"})
	if missingSide.Action != PreparedSlashArgsError || !strings.Contains(missingSide.ErrorMessage, "before the session starts") {
		t.Fatalf("missing side = %#v", missingSide)
	}
}

func TestPreparedSlashArgsMiscInlineBranchesMatchRust(t *testing.T) {
	rename := DispatchPreparedSlashArgs(PreparedSlashArgsContext{
		Command:       codextui.CommandRename,
		Args:          "  new    title  ",
		RenameAllowed: true,
	})
	if rename.Action != PreparedSlashArgsRenameThread || rename.Args != "new title" {
		t.Fatalf("rename = %#v", rename)
	}
	review := DispatchPreparedSlashArgs(PreparedSlashArgsContext{Command: codextui.CommandReview, Args: "focus auth"})
	if review.Action != PreparedSlashArgsReviewCustom || review.Args != "focus auth" {
		t.Fatalf("review = %#v", review)
	}
	resume := DispatchPreparedSlashArgs(PreparedSlashArgsContext{Command: codextui.CommandResume, Args: "thread name"})
	if resume.Action != PreparedSlashArgsResumeSession {
		t.Fatalf("resume = %#v", resume)
	}
	sandbox := DispatchPreparedSlashArgs(PreparedSlashArgsContext{Command: codextui.CommandSandboxReadRoot, Args: `D:\data`})
	if sandbox.Action != PreparedSlashArgsSandboxReadRoot || sandbox.Args != `D:\data` {
		t.Fatalf("sandbox = %#v", sandbox)
	}
	pet := DispatchPreparedSlashArgs(PreparedSlashArgsContext{Command: codextui.CommandPets, Args: "off"})
	if pet.Action != PreparedSlashArgsPetDisabled {
		t.Fatalf("pet disable = %#v", pet)
	}
	selectPet := DispatchPreparedSlashArgs(PreparedSlashArgsContext{Command: codextui.CommandPets, Args: "spark"})
	if selectPet.Action != PreparedSlashArgsSelectPet || selectPet.Args != "spark" {
		t.Fatalf("pet select = %#v", selectPet)
	}
}

func slashDispatchTestFlags() bottompane.BuiltinCommandFlags {
	return bottompane.BuiltinCommandFlags{
		CollaborationModesEnabled:   true,
		ConnectorsEnabled:           true,
		PluginsCommandEnabled:       true,
		TokenActivityCommandEnabled: true,
		ServiceTierCommandsEnabled:  true,
		GoalCommandEnabled:          true,
		PersonalityCommandEnabled:   true,
		AllowElevateSandbox:         true,
	}
}
