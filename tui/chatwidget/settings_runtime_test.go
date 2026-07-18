package chatwidget

import "testing"

func TestSettingsRuntimeFeatureSideEffectsMatchRust(t *testing.T) {
	state := SettingsRuntimeState{GoalStatusActive: true}

	fast := state.SetFeatureEnabled(SettingsFeatureFastMode, true)
	if !fast.RefreshEffectiveServiceTier || !fast.SyncServiceTierCommands {
		t.Fatalf("fast result = %#v", fast)
	}
	plugins := state.SetFeatureEnabled(SettingsFeaturePlugins, true)
	if !plugins.SyncPluginsCommand || !plugins.RefreshPluginMentions {
		t.Fatalf("plugins result = %#v", plugins)
	}
	goals := state.SetFeatureEnabled(SettingsFeatureGoals, false)
	if !goals.SyncGoalCommand || !goals.ClearGoalStatus || !goals.UpdateCollaborationIndicator || state.GoalStatusActive {
		t.Fatalf("goals result=%#v state=%#v", goals, state)
	}
	sleep := state.SetFeatureEnabled(SettingsFeaturePreventIdleSleep, true)
	if !sleep.UpdatePreventIdleSleep || !state.PreventIdleSleep {
		t.Fatalf("sleep result=%#v state=%#v", sleep, state)
	}
}

func TestSettingsRuntimeReasoningPlanMaskMatchesRust(t *testing.T) {
	state := SettingsRuntimeState{
		CollaborationModesEnabled:  true,
		ActiveModePlan:             true,
		ReasoningEffort:            "medium",
		ActiveMaskReasoningEffort:  "medium",
		PlanDefaultReasoningEffort: "low",
	}

	result := state.SetReasoningEffort("high")
	if !result.RefreshModelDependentSurfaces || state.ActiveMaskReasoningEffort != "medium" {
		t.Fatalf("global reasoning should not mutate active Plan mask: result=%#v state=%#v", result, state)
	}
	state.SetPlanModeReasoningEffort("xhigh")
	if state.ActiveMaskReasoningEffort != "xhigh" {
		t.Fatalf("plan override not applied: %#v", state)
	}
	state.SetPlanModeReasoningEffort("")
	if state.ActiveMaskReasoningEffort != "low" {
		t.Fatalf("plan reset should restore plan default: %#v", state)
	}
}

func TestSettingsRuntimeAccountUpdateClearsScopedStateMatchRust(t *testing.T) {
	state := SettingsRuntimeState{
		PendingTokenActivity:                true,
		PendingRateLimitReset:               true,
		RefreshingStatusOutputCount:         2,
		RateLimitSwitchPromptVisible:        true,
		CodexRateLimitReachedType:           "usage_limit",
		StatusLineWorkspaceHeadline:         "Team",
		StatusLineWorkspaceMessagesDisabled: true,
	}

	result := state.UpdateAccountState(true, true, true)

	if !result.ClearPendingTokenActivity || !result.ClearPendingRateLimitReset ||
		!result.ResetRateLimitWarnings || !result.DismissRateLimitSwitchPrompt ||
		!result.FinishRefreshingStatusOutputs || !result.RequestRedraw ||
		!result.ConnectorsEnabled || !result.TokenActivityCommandEnabled ||
		!result.RefreshStatusSurfaces {
		t.Fatalf("account update result = %#v", result)
	}
	if state.PendingTokenActivity || state.PendingRateLimitReset || state.RefreshingStatusOutputCount != 0 ||
		state.RateLimitSwitchPromptVisible || state.CodexRateLimitReachedType != "" ||
		state.StatusLineWorkspaceHeadline != "" || state.StatusLineWorkspaceMessagesDisabled != false ||
		!state.HasChatGPTAccount || !state.HasCodexBackendAuth {
		t.Fatalf("account update state = %#v", state)
	}
}
