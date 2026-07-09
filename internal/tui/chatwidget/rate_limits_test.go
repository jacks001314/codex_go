package chatwidget

import (
	"strings"
	"testing"
)

func TestRateLimitWarningStateThresholdsAndLabels(t *testing.T) {
	var state RateLimitWarningState
	fiveHours := int64(5 * 60)
	weekly := int64(7 * 24 * 60)

	warnings := state.TakeWarnings(RateLimitSnapshot{
		LimitID: "codex",
		Primary: &RateLimitWindow{
			UsedPercent:        74,
			WindowDurationMins: &weekly,
		},
	})
	if len(warnings) != 0 {
		t.Fatalf("warnings below threshold = %#v", warnings)
	}

	warnings = state.TakeWarnings(RateLimitSnapshot{
		LimitID: "codex",
		Primary: &RateLimitWindow{
			UsedPercent:        90,
			WindowDurationMins: &weekly,
		},
		Secondary: &RateLimitWindow{
			UsedPercent:        75,
			WindowDurationMins: &fiveHours,
		},
	})
	if len(warnings) != 2 {
		t.Fatalf("warnings len = %d, want 2: %#v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "less than 25% of your 5h limit left") {
		t.Fatalf("secondary warning = %q", warnings[0])
	}
	if !strings.Contains(warnings[1], "less than 10% of your weekly limit left") {
		t.Fatalf("primary warning = %q", warnings[1])
	}

	warnings = state.TakeWarnings(RateLimitSnapshot{
		LimitID: "codex",
		Primary: &RateLimitWindow{
			UsedPercent:        91,
			WindowDurationMins: &weekly,
		},
		Secondary: &RateLimitWindow{
			UsedPercent:        80,
			WindowDurationMins: &fiveHours,
		},
	})
	if len(warnings) != 0 {
		t.Fatalf("warnings should not repeat threshold = %#v", warnings)
	}

	warnings = state.TakeWarnings(RateLimitSnapshot{
		LimitID: "codex",
		Primary: &RateLimitWindow{
			UsedPercent:        96,
			WindowDurationMins: &weekly,
		},
	})
	if len(warnings) != 1 || !strings.Contains(warnings[0], "less than 5% of your weekly limit left") {
		t.Fatalf("95 threshold warning = %#v", warnings)
	}
}

func TestRateLimitWarningStateSkipsCapCreditsAndNonCodex(t *testing.T) {
	var state RateLimitWarningState
	monthly := int64(30 * 24 * 60)
	balance := "3.5"

	cases := []RateLimitSnapshot{
		{
			LimitID: "codex",
			Primary: &RateLimitWindow{
				UsedPercent:        100,
				WindowDurationMins: &monthly,
			},
		},
		{
			LimitID: "codex",
			Primary: &RateLimitWindow{
				UsedPercent:        95,
				WindowDurationMins: &monthly,
			},
			Credits: &RateLimitCredits{HasCredits: true, Balance: &balance},
		},
		{
			LimitID: "other",
			Primary: &RateLimitWindow{
				UsedPercent:        95,
				WindowDurationMins: &monthly,
			},
		},
	}
	for _, snapshot := range cases {
		if warnings := state.TakeWarnings(snapshot); len(warnings) != 0 {
			t.Fatalf("warnings for %#v = %#v, want none", snapshot, warnings)
		}
	}
}

func TestRateLimitSwitchPromptQueueAndView(t *testing.T) {
	balance := "2"
	if !ShouldQueueRateLimitSwitchPrompt(RateLimitSnapshot{
		LimitID: "codex",
		Primary: &RateLimitWindow{UsedPercent: 90},
	}, "gpt-5.4", false, RateLimitSwitchPromptIdle) {
		t.Fatal("ShouldQueueRateLimitSwitchPrompt = false, want true")
	}
	for name, snapshot := range map[string]RateLimitSnapshot{
		"non-codex": {LimitID: "other", Primary: &RateLimitWindow{UsedPercent: 95}},
		"credits": {
			LimitID: "codex",
			Primary: &RateLimitWindow{UsedPercent: 95},
			Credits: &RateLimitCredits{HasCredits: true, Balance: &balance},
		},
		"below": {LimitID: "codex", Primary: &RateLimitWindow{UsedPercent: 89}},
	} {
		if ShouldQueueRateLimitSwitchPrompt(snapshot, "gpt-5.4", false, RateLimitSwitchPromptIdle) {
			t.Fatalf("%s queued prompt, want false", name)
		}
	}
	if ShouldQueueRateLimitSwitchPrompt(RateLimitSnapshot{Primary: &RateLimitWindow{UsedPercent: 95}}, NudgeModelSlug, false, RateLimitSwitchPromptIdle) {
		t.Fatal("current nudge model queued prompt, want false")
	}
	if ShouldQueueRateLimitSwitchPrompt(RateLimitSnapshot{Primary: &RateLimitWindow{UsedPercent: 95}}, "gpt-5.4", true, RateLimitSwitchPromptIdle) {
		t.Fatal("hidden prompt queued, want false")
	}
	if ShouldQueueRateLimitSwitchPrompt(RateLimitSnapshot{Primary: &RateLimitWindow{UsedPercent: 95}}, "gpt-5.4", false, RateLimitSwitchPromptShown) {
		t.Fatal("shown prompt queued again, want false")
	}

	view := NewRateLimitSwitchPromptView(RateLimitSwitchPreset{Model: NudgeModelSlug})
	if view.ViewID != RateLimitSwitchPromptViewID || view.Title != "Approaching rate limits" || len(view.Items) != 3 {
		t.Fatalf("view = %#v", view)
	}
	if view.Items[0].Name != "Switch to "+NudgeModelSlug || !strings.Contains(view.Items[2].Description, "Hide future") {
		t.Fatalf("view items = %#v", view.Items)
	}
}

func TestLimitsDurationUsesRustApproximation(t *testing.T) {
	tests := map[int64]string{
		5 * 60:        "5h",
		24 * 60:       "daily",
		7 * 24 * 60:   "weekly",
		30 * 24 * 60:  "monthly",
		365 * 24 * 60: "annual",
	}
	for minutes, want := range tests {
		got, ok := LimitsDuration(minutes)
		if !ok || got != want {
			t.Fatalf("LimitsDuration(%d) = %q ok=%v, want %q", minutes, got, ok, want)
		}
	}
	if got, ok := LimitsDuration(17); ok || got != "" {
		t.Fatalf("LimitsDuration(17) = %q ok=%v, want none", got, ok)
	}
}

func TestWorkspaceOwnerNudgePromptViewMatchesRust(t *testing.T) {
	credits := NewWorkspaceOwnerNudgePromptView(AddCreditsNudgeCredits)
	if credits.Title != "You've reached your workspace credit limit" ||
		!strings.Contains(credits.Subtitle, "Notify owner?") ||
		credits.InitialSelectedIndex != 1 ||
		len(credits.Items) != 2 ||
		!credits.Items[1].IsDefault {
		t.Fatalf("credits nudge = %#v", credits)
	}
	if credits.Items[0].Action != UsageMenuActionAddCreditsNudgeSend || credits.Items[1].Action != UsageMenuActionAddCreditsNudgeCancel {
		t.Fatalf("credits actions = %#v", credits.Items)
	}

	usage := NewWorkspaceOwnerNudgePromptView(AddCreditsNudgeUsageLimit)
	if usage.Title != "Usage limit reached" || !strings.Contains(usage.Subtitle, "Request increase?") {
		t.Fatalf("usage nudge = %#v", usage)
	}
}

func TestAddCreditsNudgeEmailStateMessagesMatchRust(t *testing.T) {
	var state AddCreditsNudgeEmailState
	if !state.CanOpenWorkspaceOwnerNudgePrompt() {
		t.Fatal("empty state should allow prompt")
	}
	if !state.StartAddCreditsNudgeEmailRequest(AddCreditsNudgeCredits) || state.CanOpenWorkspaceOwnerNudgePrompt() {
		t.Fatalf("started credits request = %#v", state)
	}
	if got := state.FinishAddCreditsNudgeEmailRequest(AddCreditsNudgeEmailSent, false); got != "Workspace owner notified." {
		t.Fatalf("credits sent message = %q", got)
	}
	if !state.CanOpenWorkspaceOwnerNudgePrompt() {
		t.Fatal("finished request should allow prompt")
	}

	state.StartAddCreditsNudgeEmailRequest(AddCreditsNudgeCredits)
	if got := state.FinishAddCreditsNudgeEmailRequest(AddCreditsNudgeEmailCooldownActive, false); got != "Workspace owner was already notified recently." {
		t.Fatalf("credits cooldown message = %q", got)
	}
	state.StartAddCreditsNudgeEmailRequest(AddCreditsNudgeCredits)
	if got := state.FinishAddCreditsNudgeEmailRequest("", true); got != "Could not notify your workspace owner. Please try again." {
		t.Fatalf("credits error message = %q", got)
	}
	state.StartAddCreditsNudgeEmailRequest(AddCreditsNudgeUsageLimit)
	if got := state.FinishAddCreditsNudgeEmailRequest(AddCreditsNudgeEmailSent, false); got != "Limit increase requested." {
		t.Fatalf("usage sent message = %q", got)
	}
	state.StartAddCreditsNudgeEmailRequest(AddCreditsNudgeUsageLimit)
	if got := state.FinishAddCreditsNudgeEmailRequest(AddCreditsNudgeEmailCooldownActive, false); got != "A limit increase was already requested recently." {
		t.Fatalf("usage cooldown message = %q", got)
	}
	state.StartAddCreditsNudgeEmailRequest(AddCreditsNudgeUsageLimit)
	if got := state.FinishAddCreditsNudgeEmailRequest("", true); got != "Could not request a limit increase. Please try again." {
		t.Fatalf("usage error message = %q", got)
	}
}
