package onboarding

import (
	"reflect"
	"strings"
	"testing"
)

func TestWelcomeStateAndAnimationBreakpoint(t *testing.T) {
	if WelcomeTitle() != "Welcome to Codex" {
		t.Fatalf("welcome title = %q", WelcomeTitle())
	}
	if WelcomeStepState(true) != StepHidden || WelcomeStepState(false) != StepComplete {
		t.Fatalf("welcome states logged-in=%s logged-out=%s", WelcomeStepState(true), WelcomeStepState(false))
	}
	if !ShouldShowWelcomeAnimation(MinWelcomeAnimationWidth, MinWelcomeAnimationHeight, true, false) {
		t.Fatal("animation should show at Rust breakpoint")
	}
	if ShouldShowWelcomeAnimation(MinWelcomeAnimationWidth, MinWelcomeAnimationHeight-1, true, false) {
		t.Fatal("animation should hide below height breakpoint")
	}
	lines := WelcomeLines(true, []string{"frame-a"})
	if !reflect.DeepEqual(lines, []string{"frame-a", "", "  Welcome to Codex, OpenAI's command-line coding agent"}) {
		t.Fatalf("welcome lines = %#v", lines)
	}
}

func TestTrustDirectoryPromptStateRenderAndSelection(t *testing.T) {
	prompt := NewTrustDirectoryPrompt("/workspace/project/sub", "/workspace/project")
	prompt.ShowWindowsCreateSandboxHint = true
	lines := strings.Join(prompt.RenderLines(), "\n")
	for _, want := range []string{
		"> You are in /workspace/project/sub",
		"repository root: /workspace/project",
		"Trust this folder? Codex can read, edit, and run files here",
		"Your trust decision will be saved.",
		"> Trust and continue",
		"  Quit",
		"Press Enter to continue and create a sandbox...",
	} {
		if !strings.Contains(lines, want) {
			t.Fatalf("trust prompt missing %q:\n%s", want, lines)
		}
	}
	if prompt.StepState() != StepInProgress {
		t.Fatalf("initial step state = %s", prompt.StepState())
	}
	prompt.MoveDown()
	prompt.Confirm()
	if !prompt.ShouldQuit || prompt.Selection != TrustDirectorySelectionQuit || prompt.StepState() != StepComplete {
		t.Fatalf("quit prompt = %#v state=%s", prompt, prompt.StepState())
	}
	prompt.Trust()
	if !prompt.Trusted || prompt.ShouldQuit || prompt.Selection != TrustDirectorySelectionTrust {
		t.Fatalf("trusted prompt = %#v", prompt)
	}
}

func TestRestrictedTrustDirectoryPrompt(t *testing.T) {
	prompt := NewTrustDirectoryPrompt("/workspace/project/sub", "/workspace/project")
	prompt.Restricted = true
	prompt.ShowWindowsCreateSandboxHint = true
	lines := strings.Join(prompt.RenderLines(), "\n")
	for _, want := range []string{
		"Config, hooks, and exec policies from untrusted folders stay disabled.",
		"Opening will not change saved trust.",
		"> Open restricted",
		"  Quit",
		"Press Enter to continue; esc to quit",
	} {
		if !strings.Contains(lines, want) {
			t.Fatalf("restricted trust prompt missing %q:\n%s", want, lines)
		}
	}
	// The restricted state hides the Git-root note and never offers to save trust.
	if strings.Contains(lines, "repository root") {
		t.Fatalf("restricted prompt should hide the Git-root note:\n%s", lines)
	}
	if strings.Contains(lines, "Trust and continue") {
		t.Fatalf("restricted prompt should not offer to trust:\n%s", lines)
	}
	prompt.Confirm()
	if !prompt.Trusted || prompt.Selection != TrustDirectorySelectionTrust {
		t.Fatalf("restricted open selection = %#v", prompt)
	}
}

func TestRestrictedTrustDirectoryPromptForExistingTask(t *testing.T) {
	prompt := NewTrustDirectoryPrompt("/workspace/project", "/workspace/project")
	prompt.Restricted = true
	prompt.ExistingTask = true
	prompt.Cancel = TrustCancelAgentsOverview
	lines := strings.Join(prompt.RenderLines(), "\n")
	for _, want := range []string{
		"This existing task may retain settings and history",
		"To use restricted settings, start a new task.",
		"The folder's trust setting will not change.",
		"> Open existing task",
		"  Back to Agent Command Center",
		"Press Enter to continue; esc to go back",
	} {
		if !strings.Contains(lines, want) {
			t.Fatalf("existing-task prompt missing %q:\n%s", want, lines)
		}
	}
	if strings.Contains(lines, "Open restricted") || strings.Contains(lines, "esc to quit") {
		t.Fatalf("existing-task prompt used the new-task wording:\n%s", lines)
	}
}

func TestOnboardingScreenCurrentStepsAndDone(t *testing.T) {
	screen := NewScreen("Onboarding",
		Step{ID: "welcome", State: StepComplete},
		Step{ID: "auth", State: StepInProgress},
		Step{ID: "trust", State: StepInProgress},
	)
	current := screen.CurrentSteps()
	if len(current) != 2 || current[0].ID != "welcome" || current[1].ID != "auth" {
		t.Fatalf("current steps = %#v", current)
	}
	if screen.IsDone() {
		t.Fatal("screen should not be done while a step is in progress")
	}
	done := NewScreen("Onboarding", Step{ID: "welcome", State: StepComplete})
	if !done.IsDone() {
		t.Fatal("screen should be done when no step is in progress")
	}
}

func TestAuthOptionsAndStepState(t *testing.T) {
	options := DefaultAuthOptions(true)
	if len(options) != 3 || options[0].Choice != AuthChoiceChatGPT || options[1].Choice != AuthChoiceDeviceCode || options[2].Choice != AuthChoiceAPIKey || !options[2].Disabled {
		t.Fatalf("auth options = %#v", options)
	}
	if AuthStepState(SignInChatGPTSuccess) != StepComplete || AuthStepState(SignInAPIKeyConfigured) != StepComplete {
		t.Fatal("completed auth states should be complete")
	}
	if AuthStepState(SignInPickMode) != StepInProgress {
		t.Fatalf("pick mode state = %s", AuthStepState(SignInPickMode))
	}
}
