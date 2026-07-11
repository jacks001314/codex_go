package app

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"codex_go/internal/appserver"
)

func TestThreadGoalErrorMessageExplainsTemporarySessionMatchRust(t *testing.T) {
	err := fmt.Errorf("thread/goal/get failed in TUI: %w", errors.New("ephemeral thread does not support goals: thread-1"))
	if got := ThreadGoalErrorMessage("read", err); got != EphemeralThreadGoalErrorMessage {
		t.Fatalf("ThreadGoalErrorMessage(ephemeral) = %q, want %q", got, EphemeralThreadGoalErrorMessage)
	}

	joined := errors.Join(
		errors.New("server disappeared"),
		errors.New("thread goals require a persisted thread; this thread is ephemeral"),
	)
	if got := ThreadGoalErrorMessage("set", joined); got != EphemeralThreadGoalErrorMessage {
		t.Fatalf("ThreadGoalErrorMessage(joined ephemeral) = %q, want %q", got, EphemeralThreadGoalErrorMessage)
	}
}

func TestThreadGoalErrorMessagePreservesGenericFailureContextMatchRust(t *testing.T) {
	err := fmt.Errorf("thread/goal/get failed in TUI: %w", errors.New("server disappeared"))
	want := "Failed to read thread goal: thread/goal/get failed in TUI: server disappeared"
	if got := ThreadGoalErrorMessage("read", err); got != want {
		t.Fatalf("ThreadGoalErrorMessage(generic) = %q, want %q", got, want)
	}
}

func TestThreadGoalUsageMessageMatchesRust(t *testing.T) {
	if ThreadGoalUsageMessage != "Usage: /goal [<objective>|clear|edit|pause|resume]" {
		t.Fatalf("ThreadGoalUsageMessage = %q", ThreadGoalUsageMessage)
	}
}

func TestShouldConfirmBeforeReplacingGoalMatchRust(t *testing.T) {
	if ShouldConfirmBeforeReplacingGoal(nil) {
		t.Fatal("nil goal should not require confirmation")
	}
	if ShouldConfirmBeforeReplacingGoal(&appserver.Goal{Status: appserver.GoalComplete}) {
		t.Fatal("complete goal should not require replacement confirmation")
	}
	for _, status := range []appserver.GoalStatus{
		appserver.GoalActive,
		appserver.GoalPaused,
		appserver.GoalBlocked,
		appserver.GoalUsageLimited,
		appserver.GoalBudgetLimited,
	} {
		if !ShouldConfirmBeforeReplacingGoal(&appserver.Goal{Status: status}) {
			t.Fatalf("%s goal should require replacement confirmation", status)
		}
	}
	if ShouldConfirmBeforeReplacingGoal(&appserver.Goal{Status: "unknown"}) {
		t.Fatal("unknown goal status should not require replacement confirmation")
	}
}

func TestGoalStatusShouldPromptResumeAfterResumeMatchRust(t *testing.T) {
	for _, status := range []appserver.GoalStatus{
		appserver.GoalPaused,
		appserver.GoalBlocked,
		appserver.GoalUsageLimited,
	} {
		if !GoalStatusShouldPromptResumeAfterResume(status) {
			t.Fatalf("%s should prompt resume after resume", status)
		}
	}
	for _, status := range []appserver.GoalStatus{
		appserver.GoalActive,
		appserver.GoalBudgetLimited,
		appserver.GoalComplete,
		"unknown",
	} {
		if GoalStatusShouldPromptResumeAfterResume(status) {
			t.Fatalf("%s should not prompt resume after resume", status)
		}
	}
}

func TestOpenThreadGoalMenuAndEditorDecisionMatchRust(t *testing.T) {
	goal := &appserver.Goal{ThreadID: "thread-1", Objective: "ship it", Status: appserver.GoalActive}
	ignored := OpenThreadGoalMenuDecision(false, goal, nil)
	if !ignored.Ignore {
		t.Fatalf("ignored menu = %#v", ignored)
	}
	errDecision := OpenThreadGoalMenuDecision(true, nil, errors.New("server disappeared"))
	if errDecision.ErrorMessage != "Failed to read thread goal: server disappeared" {
		t.Fatalf("menu err = %#v", errDecision)
	}
	empty := OpenThreadGoalMenuDecision(true, nil, nil)
	if !empty.ShowUsage || empty.UsageHint != "No goal is currently set." {
		t.Fatalf("empty menu = %#v", empty)
	}
	summary := OpenThreadGoalMenuDecision(true, goal, nil)
	if !summary.ShowSummary || summary.Goal == nil || summary.Goal.Objective != "ship it" {
		t.Fatalf("summary menu = %#v", summary)
	}

	noThread := OpenThreadGoalEditorDecision(true, false, nil, nil)
	if noThread.ErrorMessage != "No goal is currently set." || !noThread.ShowUsage || noThread.UsageHint != "Create a goal before editing it." {
		t.Fatalf("no-thread editor = %#v", noThread)
	}
	edit := OpenThreadGoalEditorDecision(true, true, goal, nil)
	if !edit.ShowEditPrompt || edit.Goal == nil || edit.Goal.Objective != "ship it" {
		t.Fatalf("edit decision = %#v", edit)
	}
}

func TestResumePausedGoalPromptDecisionMatchRust(t *testing.T) {
	paused := &appserver.Goal{Objective: "continue", Status: appserver.GoalPaused}
	decision := ResumePausedGoalPromptDecision(true, paused, nil)
	if !decision.ShowResumePausedPrompt || decision.Goal == nil || decision.Goal.Objective != "continue" {
		t.Fatalf("paused prompt decision = %#v", decision)
	}
	active := ResumePausedGoalPromptDecision(true, &appserver.Goal{Status: appserver.GoalActive}, nil)
	if active.ShowResumePausedPrompt {
		t.Fatalf("active prompt decision = %#v", active)
	}
	if ignored := ResumePausedGoalPromptDecision(false, paused, nil); !ignored.Ignore {
		t.Fatalf("ignored paused prompt = %#v", ignored)
	}
}

func TestSetThreadGoalDraftPreflightDecisionMatchRust(t *testing.T) {
	activeGoal := &appserver.Goal{Status: appserver.GoalActive}
	confirm := SetThreadGoalDraftPreflightDecision(true, activeGoal, nil, ThreadGoalSetConfirmIfExists, "", nil)
	if !confirm.ShowReplaceConfirmation || confirm.Mode != ThreadGoalSetConfirmIfExists {
		t.Fatalf("confirm decision = %#v", confirm)
	}
	replaceCompleted := SetThreadGoalDraftPreflightDecision(true, &appserver.Goal{Status: appserver.GoalComplete}, nil, ThreadGoalSetConfirmIfExists, "", nil)
	if replaceCompleted.ShowReplaceConfirmation || !replaceCompleted.ReplacingGoal || !replaceCompleted.ClearExistingBeforeSet || replaceCompleted.Status != appserver.GoalActive {
		t.Fatalf("replace completed decision = %#v", replaceCompleted)
	}
	create := SetThreadGoalDraftPreflightDecision(true, nil, nil, ThreadGoalSetConfirmIfExists, "", nil)
	if create.ShowReplaceConfirmation || create.ReplacingGoal || create.Status != appserver.GoalActive {
		t.Fatalf("create decision = %#v", create)
	}
	budget := int64(1200)
	update := SetThreadGoalDraftPreflightDecision(true, activeGoal, nil, ThreadGoalSetUpdateExisting, appserver.GoalPaused, &budget)
	if update.Status != appserver.GoalPaused || update.TokenBudget == nil || *update.TokenBudget != 1200 || update.ClearExistingBeforeSet {
		t.Fatalf("update decision = %#v", update)
	}
	readErr := SetThreadGoalDraftPreflightDecision(true, nil, errors.New("read failed"), ThreadGoalSetConfirmIfExists, "", nil)
	if readErr.ErrorMessage != "Failed to read thread goal: read failed" {
		t.Fatalf("read error decision = %#v", readErr)
	}
}

func TestThreadGoalActionResultDecisionsMatchRust(t *testing.T) {
	budget := int64(2000)
	goal := appserver.Goal{Objective: "ship parity", Status: appserver.GoalBudgetLimited, TokenBudget: &budget, TokensUsed: 2100, TimeUsedSeconds: 9}
	success := ThreadGoalSetSuccessDecision(true, goal)
	if success.InfoMessage != "Goal limited by budget" || success.Hint != "Objective: ship parity Time: 9s. Tokens: 2.1K/2K." || !success.MaybeSendQueuedInput {
		t.Fatalf("success decision = %#v", success)
	}
	setErr := ThreadGoalSetErrorDecision(true, true, errors.New("boom"))
	if !setErr.CleanupMaterializedGoalFiles || setErr.ErrorMessage != "Failed to replace thread goal: boom" {
		t.Fatalf("set error decision = %#v", setErr)
	}
	status := ThreadGoalStatusUpdateDecision(true, &appserver.Goal{Status: appserver.GoalPaused}, nil)
	if status.InfoMessage != "Goal paused" {
		t.Fatalf("status decision = %#v", status)
	}
	statusErr := ThreadGoalStatusUpdateDecision(true, nil, errors.New("bad"))
	if statusErr.ErrorMessage != "Failed to update thread goal: bad" {
		t.Fatalf("status error decision = %#v", statusErr)
	}
	cleared := ThreadGoalClearDecision(true, true, nil)
	if cleared.InfoMessage != "Goal cleared" || cleared.Hint != "" {
		t.Fatalf("cleared decision = %#v", cleared)
	}
	missing := ThreadGoalClearDecision(true, false, nil)
	if missing.InfoMessage != "No goal to clear" || missing.Hint != "This thread does not currently have a goal." {
		t.Fatalf("missing clear decision = %#v", missing)
	}
	clearErr := ThreadGoalClearDecision(true, false, errors.New("bad"))
	if clearErr.ErrorMessage != "Failed to clear thread goal: bad" {
		t.Fatalf("clear error decision = %#v", clearErr)
	}
}

func TestReplaceThreadGoalConfirmationMatchRust(t *testing.T) {
	view := ReplaceThreadGoalConfirmation("thread-1", "new objective")
	if view.Title != "Replace goal?" || view.Subtitle != "New objective: new objective" || view.ReplaceName != "Replace current goal" || view.ReplaceHint != "Set the new objective and start it now" || view.CancelName != "Cancel" || view.CancelHint != "Keep the current goal" || view.ReplacementMode != ThreadGoalSetReplaceExisting {
		t.Fatalf("view = %#v", view)
	}
	spaced := ReplaceThreadGoalConfirmation("thread-1", " new objective ")
	if spaced.Subtitle != "New objective:  new objective " {
		t.Fatalf("spaced subtitle = %q", spaced.Subtitle)
	}
	if got := TruncateGoalObjective("a\u0301b", 1); got != "a\u0301" {
		t.Fatalf("grapheme truncate = %q", got)
	}
	long := ReplaceThreadGoalConfirmation("thread-1", strings.Repeat("a", 205))
	if len([]rune(long.Subtitle)) != len("New objective: ")+200 || long.Subtitle[len(long.Subtitle)-3:] != "..." {
		t.Fatalf("long subtitle = %q", long.Subtitle)
	}
}

func TestGoalUsageSummaryForThreadGoalMatchesRust(t *testing.T) {
	budget := int64(50_000)
	goal := appserver.Goal{
		Objective:       "Complete the task described in ../gameboy-long-running-prompt5.txt",
		Status:          appserver.GoalBudgetLimited,
		TokenBudget:     &budget,
		TokensUsed:      63_876,
		TimeUsedSeconds: 120,
	}
	want := "Objective: Complete the task described in ../gameboy-long-running-prompt5.txt Time: 2m. Tokens: 63.9K/50K."
	if got := GoalUsageSummaryForThreadGoal(goal); got != want {
		t.Fatalf("GoalUsageSummaryForThreadGoal() = %q, want %q", got, want)
	}

	unbudgeted := appserver.Goal{Objective: "only objective", TokensUsed: 99}
	if got := GoalUsageSummaryForThreadGoal(unbudgeted); got != "Objective: only objective" {
		t.Fatalf("unbudgeted summary = %q", got)
	}
}
