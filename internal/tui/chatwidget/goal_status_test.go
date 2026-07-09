package chatwidget

import (
	"reflect"
	"testing"
	"time"

	"codex_go/internal/appserver"
)

func TestActiveGoalUsagePrefersTokenBudget(t *testing.T) {
	budget := int64(50_000)
	got := ActiveGoalUsage(&budget, 12_500, 90)
	if got == nil || *got != "12.5K / 50K" {
		t.Fatalf("active usage = %#v", got)
	}
}

func TestActiveGoalUsageReportsTimeWithoutBudget(t *testing.T) {
	got := ActiveGoalUsage(nil, 12_500, 120)
	if got == nil || *got != "2m" {
		t.Fatalf("active usage = %#v", got)
	}
}

func TestStoppedGoalBudgetUsageReportsBudgetedTokens(t *testing.T) {
	budget := int64(50_000)
	got := StoppedGoalBudgetUsage(&budget, 63_876)
	if got == nil || *got != "63.9K / 50K tokens" {
		t.Fatalf("stopped budget usage = %#v", got)
	}
	if got := StoppedGoalBudgetUsage(nil, 12_500); got != nil {
		t.Fatalf("unbudgeted stopped usage = %#v", got)
	}
}

func TestCompletedGoalUsage(t *testing.T) {
	budget := int64(50_000)
	if got := CompletedGoalUsage(&budget, 40_000, 120); got != "40K tokens" {
		t.Fatalf("budgeted completed usage = %q", got)
	}
	if got := CompletedGoalUsage(nil, 40_000, 36_720); got != "10h 12m" {
		t.Fatalf("unbudgeted completed usage = %q", got)
	}
}

func TestActiveGoalStatusIncludesCurrentTurnElapsedTime(t *testing.T) {
	observedAt := time.Unix(1000, 0)
	state := activeGoalState(observedAt, 60)
	turnStarted := observedAt.Add(-120 * time.Second)
	got, ok := state.Indicator(observedAt.Add(60*time.Second), &turnStarted)
	wantUsage := "2m"
	want := GoalStatusIndicator{Kind: GoalStatusIndicatorActive, Usage: &wantUsage}
	if !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("indicator = %#v ok=%v want %#v", got, ok, want)
	}
}

func TestActiveGoalStatusDoesNotCountIdleTimeBeforeTurnStart(t *testing.T) {
	observedAt := time.Unix(1000, 0)
	turnStarted := observedAt.Add(120 * time.Second)
	state := activeGoalState(observedAt, 60)
	got, ok := state.Indicator(turnStarted.Add(60*time.Second), &turnStarted)
	wantUsage := "2m"
	want := GoalStatusIndicator{Kind: GoalStatusIndicatorActive, Usage: &wantUsage}
	if !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("indicator = %#v ok=%v want %#v", got, ok, want)
	}
}

func TestGoalSummaryLinesAndHints(t *testing.T) {
	budget := int64(50_000)
	goal := appserver.Goal{
		ThreadID:        "thread",
		Objective:       "do the thing",
		Status:          appserver.GoalBudgetLimited,
		TokenBudget:     &budget,
		TokensUsed:      63_876,
		TimeUsedSeconds: 36_720,
	}
	got := GoalSummaryLines(goal)
	want := []string{
		"Goal",
		"Status: limited by budget",
		"Objective: do the thing",
		"Time used: 10h 12m",
		"Tokens used: 63.9K",
		"Token budget: 50K",
		"",
		"Commands: /goal edit, /goal clear",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("summary = %#v", got)
	}
}

func TestEditedGoalStatus(t *testing.T) {
	cases := map[appserver.GoalStatus]appserver.GoalStatus{
		appserver.GoalActive:        appserver.GoalActive,
		appserver.GoalPaused:        appserver.GoalPaused,
		appserver.GoalBlocked:       appserver.GoalBlocked,
		appserver.GoalUsageLimited:  appserver.GoalUsageLimited,
		appserver.GoalBudgetLimited: appserver.GoalActive,
		appserver.GoalComplete:      appserver.GoalActive,
	}
	for input, want := range cases {
		if got := EditedGoalStatus(input); got != want {
			t.Fatalf("EditedGoalStatus(%s) = %s, want %s", input, got, want)
		}
	}
}

func TestResumePausedGoalView(t *testing.T) {
	view := ResumePausedGoalView("ship parity")
	if view.Title != "Resume paused goal?" || view.Subtitle != "Goal: ship parity" || len(view.Items) != 2 || view.InitialSelectedIndex != 0 || !view.AllowCancel {
		t.Fatalf("resume view = %#v", view)
	}
	if view.Items[0].Name != "Resume goal" || view.Items[1].Name != "Leave paused" {
		t.Fatalf("resume items = %#v", view.Items)
	}
	if view.Items[0].Action != GoalMenuActionResume || !view.Items[0].DismissOnSelect || !view.Items[1].DismissOnSelect {
		t.Fatalf("resume item behavior = %#v", view.Items)
	}
}

func activeGoalState(observedAt time.Time, timeUsedSeconds int64) GoalStatusState {
	return NewGoalStatusState(appserver.Goal{
		ThreadID:        "thread",
		Objective:       "do the thing",
		Status:          appserver.GoalActive,
		TokensUsed:      0,
		TimeUsedSeconds: timeUsedSeconds,
		CreatedAt:       1,
		UpdatedAt:       1,
	}, observedAt)
}
