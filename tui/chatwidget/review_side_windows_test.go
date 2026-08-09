package chatwidget

import (
	"strings"
	"testing"
)

func TestReviewPresetViewMatchesRustOrder(t *testing.T) {
	view := NewReviewPresetView()
	if view.Title != "Select a review preset" || len(view.Items) != 4 {
		t.Fatalf("view = %+v", view)
	}
	want := []string{
		"Review against a base branch",
		"Review uncommitted changes",
		"Review a commit",
		"Custom review instructions",
	}
	for i, name := range want {
		if view.Items[i].Name != name {
			t.Fatalf("item %d = %+v, want %q", i, view.Items[i], name)
		}
	}
	if view.Items[0].Description != "(PR Style)" || !view.Items[0].DismissParentOnChildAccept {
		t.Fatalf("base branch item = %+v", view.Items[0])
	}
	if !view.Items[1].DismissOnSelect || view.Items[1].Action != ReviewActionUncommitted {
		t.Fatalf("uncommitted item = %+v", view.Items[1])
	}
}

func TestReviewBranchAndCommitPickers(t *testing.T) {
	branchView := NewReviewBranchPickerView("", []string{"main", "", "release"})
	if branchView.Title != "Select a base branch" || !branchView.Searchable || branchView.SearchPlaceholder != "Type to search branches" {
		t.Fatalf("branch view = %+v", branchView)
	}
	if len(branchView.Items) != 3 || branchView.Items[0].Name != "(detached HEAD) -> main" || branchView.Items[1].Name != "(detached HEAD) -> " || branchView.Items[0].SearchValue != "main" {
		t.Fatalf("branch items = %+v", branchView.Items)
	}
	branchTarget, ok := ReviewTargetForBranch(" main ")
	if !ok || branchTarget.Kind != ReviewTargetBaseBranch || branchTarget.Branch != " main " {
		t.Fatalf("branch target = %+v/%v", branchTarget, ok)
	}

	commitView := NewReviewCommitPickerView([]ReviewCommitEntry{{Subject: "Fix auth", SHA: "abc123"}, {Subject: "", SHA: "skip"}})
	if commitView.Title != "Select a commit to review" || !commitView.Searchable || commitView.SearchPlaceholder != "Type to search commits" {
		t.Fatalf("commit view = %+v", commitView)
	}
	if len(commitView.Items) != 2 || commitView.Items[0].Name != "Fix auth" || commitView.Items[0].SearchValue != "Fix auth abc123" || commitView.Items[1].Name != "" || commitView.Items[1].SearchValue != " skip" {
		t.Fatalf("commit items = %+v", commitView.Items)
	}
	commitTarget, ok := ReviewTargetForCommit(ReviewCommitEntry{Subject: " Fix auth ", SHA: " abc123 "})
	if !ok || commitTarget.Kind != ReviewTargetCommit || commitTarget.SHA != " abc123 " || commitTarget.Title != " Fix auth " {
		t.Fatalf("commit target = %+v/%v", commitTarget, ok)
	}
}

func TestReviewCustomPromptTarget(t *testing.T) {
	view := NewReviewCustomPromptView()
	if view.Title != "Custom review instructions" || view.Placeholder != "Type instructions and press Enter" || view.InitialText != "" {
		t.Fatalf("custom prompt view = %+v", view)
	}
	if _, ok := ReviewTargetForCustomPrompt("  "); ok {
		t.Fatal("empty custom prompt should not build a target")
	}
	target, ok := ReviewTargetForCustomPrompt(" check auth ")
	if !ok || target.Kind != ReviewTargetCustom || target.Instructions != "check auth" {
		t.Fatalf("custom target = %+v/%v", target, ok)
	}
	if target := ReviewTargetForUncommittedChanges(); target.Kind != ReviewTargetUncommitted {
		t.Fatalf("uncommitted target = %+v", target)
	}
}

func TestSideConversationStateAndPlainSubmitPolicy(t *testing.T) {
	state := NewSideConversationState(" Ask gcode ", " Ask in side thread ")
	if state.Placeholder() != " Ask gcode " {
		t.Fatalf("placeholder = %q", state.Placeholder())
	}
	state.SetActive(true)
	state.SetContextLabel(" plan ")
	if !state.Active || state.Placeholder() != " Ask in side thread " || state.ContextLabel != " plan " {
		t.Fatalf("state = %+v", state)
	}
	if got := SubmitPlainUserTurnShellEscapePolicy(); got != ShellEscapeDisallow {
		t.Fatalf("policy = %q", got)
	}
}

func TestChatSessionHeaderSetModel(t *testing.T) {
	header := NewChatSessionHeader(" gpt-5 ")
	if header.Model != "gpt-5" {
		t.Fatalf("header = %+v", header)
	}
	if header.SetModel("gpt-5") {
		t.Fatal("same model should not report a change")
	}
	if !header.SetModel("gpt-5.1") || header.Model != "gpt-5.1" {
		t.Fatalf("header after set = %+v", header)
	}
}

func TestWindowsSandboxEnablePromptView(t *testing.T) {
	view := NewWindowsSandboxEnablePromptView(true, true)
	if !view.ReopenOnCancel || len(view.Items) != 3 {
		t.Fatalf("enable view = %+v", view)
	}
	if view.Items[0].Name != "Set up default sandbox (requires Administrator permissions)" || view.Items[1].Name != "Use non-admin sandbox (higher risk if prompt injected)" || view.Items[2].Name != "Quit" {
		t.Fatalf("enable items = %+v", view.Items)
	}
	if !strings.Contains(strings.Join(view.HeaderLines, "\n"), "developers.openai.com/codex/windows") {
		t.Fatalf("header = %+v", view.HeaderLines)
	}

	required := NewWindowsSandboxEnablePromptView(false, true)
	if len(required.Items) != 2 || strings.Contains(required.Items[0].Name, "non-admin") || !strings.Contains(strings.Join(required.HeaderLines, "\n"), "Your organization requires") {
		t.Fatalf("required view = %+v", required)
	}
}

func TestWindowsSandboxFallbackPromptView(t *testing.T) {
	view := NewWindowsSandboxFallbackPromptView(true, false)
	if view.ReopenOnCancel || len(view.Items) != 3 {
		t.Fatalf("fallback view = %+v", view)
	}
	if view.Items[0].Name != "Try setting up admin sandbox again" || view.Items[1].Name != "Use Codex with non-admin sandbox" || view.Items[2].Action != WindowsSandboxActionQuit {
		t.Fatalf("fallback items = %+v", view.Items)
	}
	if !strings.Contains(strings.Join(view.HeaderLines, "\n"), "Couldn't set up your sandbox") {
		t.Fatalf("fallback header = %+v", view.HeaderLines)
	}
}

func TestWorldWritableWarningConfirmationView(t *testing.T) {
	view := NewWorldWritableWarningConfirmationView("Agent mode", []string{`C:\repo`, `D:\tmp`}, 2, false)
	header := strings.Join(view.HeaderLines, "\n")
	for _, want := range []string{"writable by Everyone", `  - C:\repo`, "and 2 more"} {
		if !strings.Contains(header, want) {
			t.Fatalf("header missing %q:\n%s", want, header)
		}
	}
	if len(view.Items) != 2 || view.Items[0].Description != "Apply Agent mode for this session" || view.Items[1].Name != "Continue and don't warn again" {
		t.Fatalf("items = %+v", view.Items)
	}

	failed := NewWorldWritableWarningConfirmationView("Read-Only mode", nil, 0, true)
	if !strings.Contains(strings.Join(failed.HeaderLines, "\n"), "couldn't complete the world-writable scan") {
		t.Fatalf("failed header = %+v", failed.HeaderLines)
	}
}

func TestWindowsSandboxSetupStatus(t *testing.T) {
	inProgress := WindowsSandboxSetupInProgressStatus()
	if inProgress.ComposerInputEnabled || inProgress.Status != "Setting up sandbox..." || inProgress.Details == "" || inProgress.InterruptHintVisible {
		t.Fatalf("in progress = %+v", inProgress)
	}
	cleared := WindowsSandboxSetupClearedStatus()
	if !cleared.ComposerInputEnabled || !cleared.InterruptHintVisible {
		t.Fatalf("cleared = %+v", cleared)
	}
}

func TestWindowsSandboxPromptDecisionMatchesRust(t *testing.T) {
	if !ElevatedWindowsSandboxSetupRequired(WindowsSandboxLevelElevated, true, false) {
		t.Fatal("elevated sandbox with requirements source and incomplete setup should require setup")
	}
	if ElevatedWindowsSandboxSetupRequired(WindowsSandboxLevelElevated, false, false) ||
		ElevatedWindowsSandboxSetupRequired(WindowsSandboxLevelElevated, true, true) ||
		ElevatedWindowsSandboxSetupRequired(WindowsSandboxLevelUnelevated, true, false) {
		t.Fatal("unexpected elevated setup requirement")
	}

	disabled := MaybePromptWindowsSandboxEnable(true, WindowsSandboxLevelDisabled, false, true)
	if !disabled.SetupRequired || !disabled.OpenEnablePrompt {
		t.Fatalf("disabled decision = %#v", disabled)
	}
	elevated := MaybePromptWindowsSandboxEnable(true, WindowsSandboxLevelElevated, true, true)
	if !elevated.SetupRequired || !elevated.OpenEnablePrompt {
		t.Fatalf("elevated decision = %#v", elevated)
	}
	hidden := MaybePromptWindowsSandboxEnable(false, WindowsSandboxLevelDisabled, false, true)
	if !hidden.SetupRequired || hidden.OpenEnablePrompt {
		t.Fatalf("hidden decision = %#v", hidden)
	}
	noPreset := MaybePromptWindowsSandboxEnable(true, WindowsSandboxLevelDisabled, false, false)
	if !noPreset.SetupRequired || noPreset.OpenEnablePrompt {
		t.Fatalf("no preset decision = %#v", noPreset)
	}
}
