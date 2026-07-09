package chatwidget

import (
	"strings"
	"testing"
)

func TestUsageMenuViewResetAvailability(t *testing.T) {
	two := int64(2)
	view := NewUsageMenuView(UsageMenuState{
		HasChatGPTAccount:              true,
		AvailableRateLimitResetCredits: &two,
	})
	if view.ViewID != UsageMenuViewID || view.Title != "Usage" || len(view.Items) != 2 {
		t.Fatalf("usage view = %#v", view)
	}
	if view.Items[1].Disabled || view.Items[1].Action != UsageMenuActionOpenRateLimitReset {
		t.Fatalf("reset item = %#v", view.Items[1])
	}
	if !strings.Contains(view.Items[1].Description, "You have 2 usage limit resets available.") {
		t.Fatalf("reset description = %q", view.Items[1].Description)
	}
	if view.RefreshResetAvailability {
		t.Fatal("available credits should not request refresh")
	}

	zero := int64(0)
	view = NewUsageMenuView(UsageMenuState{
		HasChatGPTAccount:                true,
		AvailableRateLimitResetCredits:   &zero,
		RefreshWhenKnownResetCreditsZero: true,
		NextRefreshResetAvailabilityID:   7,
	})
	if !view.Items[1].Disabled || view.Items[1].Description != "No usage limit resets available." {
		t.Fatalf("known zero reset item = %#v", view.Items[1])
	}
	if !view.RefreshResetAvailability || view.RefreshResetAvailabilityID != 7 {
		t.Fatalf("refresh metadata = %#v", view)
	}

	view = NewUsageMenuView(UsageMenuState{HasChatGPTAccount: true})
	if view.Items[1].Disabled || view.Items[1].Description != "Check reset availability." {
		t.Fatalf("unknown reset item = %#v", view.Items[1])
	}

	view = NewUsageMenuView(UsageMenuState{})
	if !view.Items[1].Disabled || view.Items[1].Description != "No usage limit resets available." {
		t.Fatalf("signed out reset item = %#v", view.Items[1])
	}
}

func TestRateLimitResetViews(t *testing.T) {
	loading := RateLimitResetLoadingView()
	if loading.Subtitle != "Checking your available resets..." || !loading.Items[0].Disabled {
		t.Fatalf("loading view = %#v", loading)
	}

	weekly := RateLimitResetConfirmationView(2, false, "plus")
	if weekly.InitialSelectedIndex != 1 || !strings.Contains(weekly.Items[0].Description, "5-hour and weekly") {
		t.Fatalf("weekly confirmation = %#v", weekly)
	}
	monthly := RateLimitResetConfirmationView(1, true, "plus")
	if monthly.Subtitle != "You have 1 usage limit reset available." || !strings.Contains(monthly.Items[0].Description, "monthly") {
		t.Fatalf("monthly confirmation = %#v", monthly)
	}
	free := RateLimitResetConfirmationView(1, false, "free")
	if !strings.Contains(free.Items[0].Description, "monthly") {
		t.Fatalf("free confirmation = %#v", free)
	}

	consuming := RateLimitResetConsumingView()
	if consuming.AllowCancel || consuming.Items[0].Name != "Using a reset..." {
		t.Fatalf("consuming view = %#v", consuming)
	}
	success := RateLimitResetSuccessLoadingView()
	if success.AllowCancel || success.Subtitle != "Usage reset. Checking your remaining resets..." {
		t.Fatalf("success view = %#v", success)
	}
}

func TestRateLimitResetConsumeResultView(t *testing.T) {
	reset := RateLimitResetConsumeResultView(RateLimitResetOutcomeReset, false)
	if !reset.RefreshCreditsAfterReset || reset.View.Subtitle != "Usage reset. Checking your remaining resets..." {
		t.Fatalf("reset result = %#v", reset)
	}
	redeemed := RateLimitResetConsumeResultView(RateLimitResetOutcomeAlreadyRedeemed, false)
	if !redeemed.RefreshCreditsAfterReset {
		t.Fatalf("already redeemed result = %#v", redeemed)
	}
	nothing := RateLimitResetConsumeResultView(RateLimitResetOutcomeNothingToReset, false)
	if nothing.View.Subtitle != "Your usage does not need a reset right now." {
		t.Fatalf("nothing result = %#v", nothing)
	}
	noCredit := RateLimitResetConsumeResultView(RateLimitResetOutcomeNoCredit, false)
	if noCredit.AvailableCredits == nil || *noCredit.AvailableCredits != 0 || noCredit.View.Subtitle != "No usage limit resets are available." {
		t.Fatalf("no credit result = %#v", noCredit)
	}
	failed := RateLimitResetConsumeResultView("", true)
	if failed.View.Subtitle != "Couldn't reset usage. Please try again." || len(failed.View.Items) != 2 || failed.View.Items[0].Name != "Try again" {
		t.Fatalf("failed result = %#v", failed)
	}
}

func TestRateLimitResetHintAndLabel(t *testing.T) {
	if label := ResetLabel(1); label != "usage limit reset" {
		t.Fatalf("ResetLabel(1) = %q", label)
	}
	if label := ResetLabel(2); label != "usage limit resets" {
		t.Fatalf("ResetLabel(2) = %q", label)
	}
	if hint, ok := RateLimitResetHint(0); ok || hint != "" {
		t.Fatalf("hint zero = %q ok=%v", hint, ok)
	}
	hint, ok := RateLimitResetHint(2)
	if !ok || hint != "You have 2 usage limit resets available. Run /usage to use one." {
		t.Fatalf("hint two = %q ok=%v", hint, ok)
	}
}

func TestUsageRuntimeKnownZeroRefreshMatchesRust(t *testing.T) {
	zero := int64(0)
	state := &UsageRuntimeState{
		HasChatGPTAccount:              true,
		AvailableRateLimitResetCredits: &zero,
	}
	opened := state.OpenUsageMenu()
	if !opened.RefreshResetAvailability || opened.RefreshResetAvailabilityID == 0 {
		t.Fatalf("open usage result = %#v", opened)
	}
	if !opened.View.Items[1].Disabled || opened.View.Items[1].Description != "No usage limit resets available." {
		t.Fatalf("known zero reset item = %#v", opened.View.Items[1])
	}
	failedView, ok := state.FinishUsageMenuRateLimitRefresh(opened.RefreshResetAvailabilityID, RateLimitResetCreditsRefresh{Failed: true})
	if !ok {
		t.Fatal("failed refresh was not accepted")
	}
	if !failedView.Items[1].Disabled || failedView.Items[1].Description != "No usage limit resets available." {
		t.Fatalf("failed refresh should preserve cached zero = %#v", failedView.Items[1])
	}

	opened = state.OpenUsageMenu()
	updatedView, ok := state.FinishUsageMenuRateLimitRefresh(opened.RefreshResetAvailabilityID, RateLimitResetCreditsRefresh{AvailableCount: 1})
	if !ok {
		t.Fatal("successful refresh was not accepted")
	}
	if updatedView.Items[1].Disabled || updatedView.Items[1].Description != "You have 1 usage limit reset available." {
		t.Fatalf("successful refresh did not enable reset = %#v", updatedView.Items[1])
	}
}

func TestUsageRuntimeResetLifecycleMatchesRust(t *testing.T) {
	state := &UsageRuntimeState{HasChatGPTAccount: true, HasCodexBackendAuth: true}
	loading := state.ShowRateLimitResetLoadingPopup()
	if loading.RequestID == 0 || loading.View.Subtitle != "Checking your available resets..." {
		t.Fatalf("loading = %#v", loading)
	}
	loaded := state.FinishRateLimitResetCreditsRefresh(loading.RequestID, RateLimitResetCreditsRefresh{AvailableCount: 2})
	if !loaded.Matched || loaded.View.Subtitle != "You have 2 usage limit resets available." {
		t.Fatalf("loaded = %#v", loaded)
	}

	consuming := state.ShowRateLimitResetConsumingPopup()
	success := state.FinishRateLimitResetConsume(consuming.RequestID, RateLimitResetOutcomeReset, false)
	if !success.Matched || !success.RefreshCreditsAfterReset || state.PendingRateLimitResetRequestID == nil {
		t.Fatalf("success should keep pending request for post-refresh = %#v state=%#v", success, state)
	}
	if state.AvailableRateLimitResetCredits != nil {
		t.Fatalf("successful consume should clear stale credit count, got %v", *state.AvailableRateLimitResetCredits)
	}
	post := state.FinishPostConsumeResetCreditsRefresh(consuming.RequestID, RateLimitResetCreditsRefresh{AvailableCount: 1})
	if !post.Matched || post.View.Subtitle != "Usage reset. You have 1 usage limit reset left." {
		t.Fatalf("post refresh = %#v", post)
	}

	noCredit := state.ShowRateLimitResetConsumingPopup()
	done := state.FinishRateLimitResetConsume(noCredit.RequestID, RateLimitResetOutcomeNoCredit, false)
	if !done.Matched || done.RefreshCreditsAfterReset || state.PendingRateLimitResetRequestID != nil {
		t.Fatalf("no credit result = %#v state=%#v", done, state)
	}
	if state.AvailableRateLimitResetCredits == nil || *state.AvailableRateLimitResetCredits != 0 {
		t.Fatalf("no credit should cache zero, got %#v", state.AvailableRateLimitResetCredits)
	}
}

func TestUsageRuntimeMonthlyCopyAndPlanRetentionMatchRust(t *testing.T) {
	state := &UsageRuntimeState{PlanType: "free"}
	free := state.RateLimitResetConfirmationView(1)
	if !strings.Contains(free.Items[0].Description, "monthly") {
		t.Fatalf("free copy = %#v", free.Items[0])
	}

	month := int64(30 * 24 * 60)
	state = &UsageRuntimeState{}
	state.ApplyRateLimitSnapshots([]RateLimitSnapshot{{
		LimitID:  "codex",
		PlanType: "business",
		Primary:  &RateLimitWindow{WindowDurationMins: &month},
	}})
	monthly := state.RateLimitResetConfirmationView(1)
	if !strings.Contains(monthly.Items[0].Description, "monthly") {
		t.Fatalf("monthly window copy = %#v", monthly.Items[0])
	}

	state.ApplyRateLimitSnapshots([]RateLimitSnapshot{{LimitID: "codex"}})
	if state.PlanType != "business" {
		t.Fatalf("plan type should be retained, got %q", state.PlanType)
	}
}

func TestUsageRuntimeStartupHintRequiresBackendAuthMatchRust(t *testing.T) {
	state := &UsageRuntimeState{HasChatGPTAccount: true}
	requestID := state.StartRateLimitResetStartupCheck()
	result := state.FinishRateLimitResetHintRefresh(requestID, RateLimitResetCreditsRefresh{AvailableCount: 2})
	if !result.Matched || result.Hint != "" || state.AvailableRateLimitResetCredits != nil {
		t.Fatalf("unauthenticated hint result = %#v state=%#v", result, state)
	}

	state.HasCodexBackendAuth = true
	requestID = state.StartRateLimitResetStartupCheck()
	result = state.FinishRateLimitResetHintRefresh(requestID, RateLimitResetCreditsRefresh{AvailableCount: 2})
	if !result.Matched || result.Hint != "You have 2 usage limit resets available. Run /usage to use one." {
		t.Fatalf("authenticated hint result = %#v", result)
	}
	if hint, ok := state.TakePendingRateLimitResetHint(); !ok || hint != result.Hint {
		t.Fatalf("take pending hint = %q ok=%v", hint, ok)
	}
}
