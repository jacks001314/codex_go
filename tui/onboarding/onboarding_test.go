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
		"Do you trust the contents of this directory?",
		"> Yes, continue",
		"  No, quit",
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
