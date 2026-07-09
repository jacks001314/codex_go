package chatwidget

import "testing"

func TestPlanModeNudgePolicyMatchesRust(t *testing.T) {
	scope := NewPlanModeNudgeScope("")
	base := PlanModeNudgeContext{
		CollaborationModesEnabled: true,
		PlanMaskAvailable:         true,
		ActiveMode:                CollaborationModeKindDefault,
		ComposerText:              "please make a plan first",
		ComposerInputEnabled:      true,
		DismissedScopes:           map[PlanModeNudgeScope]bool{},
		Scope:                     scope,
	}
	if !ShouldShowPlanModeNudge(base) {
		t.Fatal("expected plan nudge for standalone plan keyword")
	}
	base.ComposerText = "planning the work"
	if ShouldShowPlanModeNudge(base) {
		t.Fatal("planning should not match standalone plan keyword")
	}
	base.ComposerText = "/plan"
	if ShouldShowPlanModeNudge(base) {
		t.Fatal("slash drafts should suppress presentation even though keyword matches")
	}
	if !ContainsPlanKeyword("/plan") || !ContainsPlanKeyword("!plan") || ContainsPlanKeyword("plane") {
		t.Fatal("lexical plan keyword helper mismatch")
	}
	base.ComposerText = "plan"
	base.DismissedScopes[scope] = true
	if ShouldShowPlanModeNudge(base) {
		t.Fatal("dismissed scope should suppress nudge")
	}
}

func TestCollaborationRuntimeSetMaskAppliesPlanOverrideAndMessage(t *testing.T) {
	state := CollaborationRuntimeState{
		CollaborationModesEnabled: true,
		ThreadID:                  "thread-1",
		CurrentMode:               NewCollaborationMode(CollaborationModeKindDefault, "gpt-5", "high", "default"),
		PlanModeReasoningEffort:   "xhigh",
		DismissedPlanNudgeScopes:  map[PlanModeNudgeScope]bool{},
	}
	planMask, ok := PlanCollaborationMask(nil)
	if !ok {
		t.Fatal("missing plan mask")
	}
	planModel := "gpt-5.4-mini"
	planMask.Model = &planModel

	result := state.SetCollaborationMaskFromUserAction(planMask)
	if !result.Applied || !result.SubmitThreadSettingsUpdate || !result.RefreshModelDependentSurfaces ||
		!result.UpdateCollaborationIndicator || result.CollaborationModeIndicator != CollaborationModeIndicatorPlan {
		t.Fatalf("result = %#v", result)
	}
	if state.ActiveMask == nil || state.ActiveMask.ReasoningEffort.Value == nil || *state.ActiveMask.ReasoningEffort.Value != "xhigh" {
		t.Fatalf("plan override not applied: %#v", state.ActiveMask)
	}
	if !state.DismissedPlanNudgeScopes[NewPlanModeNudgeScope("thread-1")] || state.PlanNudgeVisible {
		t.Fatalf("plan nudge not dismissed for active thread: %#v", state)
	}
	if result.InfoMessage != "Model changed to gpt-5.4-mini xhigh for Plan mode." {
		t.Fatalf("info message = %q", result.InfoMessage)
	}
}

func TestCollaborationRuntimeCycleAndEffectiveModeMatchRust(t *testing.T) {
	state := CollaborationRuntimeState{
		CollaborationModesEnabled: true,
		ThreadID:                  "thread-1",
		CurrentMode:               NewCollaborationMode(CollaborationModeKindDefault, "gpt-5", "medium", "default"),
	}
	defaultMask, _ := DefaultCollaborationMask(nil)
	state.ActiveMask = &defaultMask

	result := state.CycleCollaborationMode(nil)
	if !result.Applied || state.ActiveMask == nil || state.ActiveMask.Mode == nil || *state.ActiveMask.Mode != CollaborationModeKindPlan {
		t.Fatalf("cycle to plan failed: result=%#v state=%#v", result, state)
	}
	effective := state.EffectiveCollaborationMode()
	if effective.Mode != CollaborationModeKindPlan || effective.Settings.ReasoningEffort == nil || *effective.Settings.ReasoningEffort != CollaborationPlanDefaultReasoningEffort {
		t.Fatalf("effective plan mode = %#v", effective)
	}
	result = state.CycleCollaborationMode(nil)
	if !result.Applied || state.ActiveMask == nil || state.ActiveMask.Mode == nil || *state.ActiveMask.Mode != CollaborationModeKindDefault {
		t.Fatalf("cycle back to default failed: result=%#v state=%#v", result, state)
	}
}

func TestThreadSettingsApplyUpdatesCWDWorkspaceRootsAndRefreshes(t *testing.T) {
	state := ThreadSettingsRuntimeState{
		CWD:            "D:/repo/old",
		WorkspaceRoots: []string{"D:/repo/old", "D:/repo/shared"},
		Collaboration: CollaborationRuntimeState{
			CollaborationModesEnabled: true,
			CurrentMode:               NewCollaborationMode(CollaborationModeKindDefault, "gpt-5", "medium", "default"),
		},
	}
	result := state.ApplyThreadSettings(ThreadSettingsRuntimeUpdate{
		CWD:               "D:/repo/new",
		ModelProviderID:   "openai",
		ServiceTier:       "default",
		ApprovalPolicy:    "on-request",
		ApprovalsReviewer: "codex",
		Personality:       PersonalityPragmatic,
		Model:             "gpt-5.4",
		ReasoningEffort:   "high",
		CollaborationMode: NewCollaborationMode(CollaborationModeKindPlan, "ignored", "low", "plan"),
	})

	if !result.CWDChanged || !result.RefreshSkillsForCurrentCWD || !result.RefreshStatusSurfaces || !result.RefreshPluginMentions || !result.RequestRedraw {
		t.Fatalf("apply result = %#v", result)
	}
	if state.CWD != "D:/repo/new" || len(state.WorkspaceRoots) != 2 || state.WorkspaceRoots[0] != "D:/repo/new" || state.WorkspaceRoots[1] != "D:/repo/shared" {
		t.Fatalf("workspace roots not rewritten: %#v", state.WorkspaceRoots)
	}
	if state.Collaboration.ActiveMask == nil || state.Collaboration.ActiveMask.Mode == nil || *state.Collaboration.ActiveMask.Mode != CollaborationModeKindPlan ||
		state.Collaboration.ActiveMask.Model == nil || *state.Collaboration.ActiveMask.Model != "gpt-5.4" {
		t.Fatalf("collaboration mask not synced: %#v", state.Collaboration.ActiveMask)
	}
}
