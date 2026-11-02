package onboarding

const (
	MinWelcomeAnimationHeight = 37
	MinWelcomeAnimationWidth  = 60

	welcomeHeaderLine = "  Welcome to Codex, OpenAI's command-line coding agent"
)

func WelcomeTitle() string {
	return "Welcome to Codex"
}

func WelcomeStepState(isLoggedIn bool) StepState {
	if isLoggedIn {
		return StepHidden
	}
	return StepComplete
}

func WelcomeLines(showAnimation bool, frame []string) []string {
	lines := []string{}
	if showAnimation {
		lines = append(lines, frame...)
		lines = append(lines, "")
	}
	lines = append(lines, welcomeHeaderLine)
	return lines
}

func ShouldShowWelcomeAnimation(width uint16, height uint16, animationsEnabled bool, suppressed bool) bool {
	return animationsEnabled && !suppressed && width >= MinWelcomeAnimationWidth && height >= MinWelcomeAnimationHeight
}
