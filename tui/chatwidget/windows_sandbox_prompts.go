package chatwidget

import "strings"

const windowsSandboxPromptFooter = "Press enter to confirm or esc to go back"

const (
	WindowsSandboxActionSetupElevated UsageMenuAction = "windows_sandbox_setup_elevated"
	WindowsSandboxActionUseLegacy     UsageMenuAction = "windows_sandbox_use_legacy"
	WindowsSandboxActionQuit          UsageMenuAction = "windows_sandbox_quit"
)

type WindowsSandboxMode string

const (
	WindowsSandboxModeDisabled   WindowsSandboxMode = "disabled"
	WindowsSandboxModeDefault    WindowsSandboxMode = "default"
	WindowsSandboxModeElevated   WindowsSandboxMode = "elevated"
	WindowsSandboxModeUnelevated WindowsSandboxMode = "unelevated"
)

type WindowsSandboxLevel string

const (
	WindowsSandboxLevelDisabled   WindowsSandboxLevel = "disabled"
	WindowsSandboxLevelElevated   WindowsSandboxLevel = "elevated"
	WindowsSandboxLevelUnelevated WindowsSandboxLevel = "unelevated"
)

type WindowsSandboxSetupStatus struct {
	ComposerInputEnabled bool
	ComposerPlaceholder  string
	Status               string
	Details              string
	InterruptHintVisible bool
}

type WindowsSandboxPromptDecision struct {
	SetupRequired    bool
	OpenEnablePrompt bool
}

func NewWindowsSandboxEnablePromptView(allowUnelevated bool, setupChoiceRequired bool) SelectionView {
	header := []string{}
	if allowUnelevated {
		header = append(header, "Set up the Codex agent sandbox to protect your files and control network access. Learn more <https://developers.openai.com/codex/windows>")
	} else {
		header = append(header,
			"Your organization requires the default Codex agent sandbox to continue. Set it up to protect your files and control network access.",
			"Learn more <https://developers.openai.com/codex/windows>",
		)
	}
	items := []SelectionItem{{
		Name:            "Set up default sandbox (requires Administrator permissions)",
		Action:          WindowsSandboxActionSetupElevated,
		DismissOnSelect: true,
	}}
	if allowUnelevated {
		items = append(items, SelectionItem{
			Name:            "Use non-admin sandbox (higher risk if prompt injected)",
			Action:          WindowsSandboxActionUseLegacy,
			DismissOnSelect: true,
		})
	}
	items = append(items, SelectionItem{
		Name:            "Quit",
		Action:          WindowsSandboxActionQuit,
		DismissOnSelect: true,
	})
	return SelectionView{
		HeaderLines:    header,
		FooterHint:     windowsSandboxPromptFooter,
		Items:          items,
		AllowCancel:    true,
		ReopenOnCancel: setupChoiceRequired,
	}
}

func NewWindowsSandboxFallbackPromptView(allowUnelevated bool, setupChoiceRequired bool) SelectionView {
	header := []string{"Couldn't set up your sandbox with Administrator permissions", ""}
	if allowUnelevated {
		header = append(header, "You can still use Codex in a non-admin sandbox. It carries greater risk if prompt injected.")
	} else {
		header = append(header, "Your organization requires the default sandbox before Codex can continue.")
	}
	header = append(header, "Learn more <https://developers.openai.com/codex/windows>")
	items := []SelectionItem{{
		Name:            "Try setting up admin sandbox again",
		Action:          WindowsSandboxActionSetupElevated,
		DismissOnSelect: true,
	}}
	if allowUnelevated {
		items = append(items, SelectionItem{
			Name:            "Use Codex with non-admin sandbox",
			Action:          WindowsSandboxActionUseLegacy,
			DismissOnSelect: true,
		})
	}
	items = append(items, SelectionItem{
		Name:            "Quit",
		Action:          WindowsSandboxActionQuit,
		DismissOnSelect: true,
	})
	return SelectionView{
		HeaderLines:    header,
		FooterHint:     windowsSandboxPromptFooter,
		Items:          items,
		AllowCancel:    true,
		ReopenOnCancel: setupChoiceRequired,
	}
}

func WindowsSandboxSetupInProgressStatus() WindowsSandboxSetupStatus {
	return WindowsSandboxSetupStatus{
		ComposerInputEnabled: false,
		ComposerPlaceholder:  "Input disabled until setup completes.",
		Status:               "Setting up sandbox...",
		Details:              "Hang tight, this may take a few minutes",
		InterruptHintVisible: false,
	}
}

func WindowsSandboxSetupClearedStatus() WindowsSandboxSetupStatus {
	return WindowsSandboxSetupStatus{
		ComposerInputEnabled: true,
		InterruptHintVisible: true,
	}
}

func WindowsSandboxModeAllowed(requirements PermissionRequirements, mode WindowsSandboxMode) bool {
	if len(requirements.AllowedWindowsSandboxModes) == 0 {
		return true
	}
	normalized := normalizeWindowsSandboxMode(mode)
	if normalized == "" || normalized == WindowsSandboxModeDisabled {
		return false
	}
	for _, candidate := range requirements.AllowedWindowsSandboxModes {
		if normalizeWindowsSandboxMode(candidate) == normalized {
			return true
		}
	}
	return false
}

func ElevatedWindowsSandboxSetupRequired(level WindowsSandboxLevel, requirementsSourcePresent bool, setupComplete bool) bool {
	return level == WindowsSandboxLevelElevated && requirementsSourcePresent && !setupComplete
}

func MaybePromptWindowsSandboxEnable(showNow bool, level WindowsSandboxLevel, elevatedSetupRequired bool, hasAutoPreset bool) WindowsSandboxPromptDecision {
	setupRequired := level == WindowsSandboxLevelDisabled || elevatedSetupRequired
	return WindowsSandboxPromptDecision{
		SetupRequired:    setupRequired,
		OpenEnablePrompt: showNow && setupRequired && hasAutoPreset,
	}
}

func normalizeWindowsSandboxMode(mode WindowsSandboxMode) WindowsSandboxMode {
	switch strings.TrimSpace(string(mode)) {
	case string(WindowsSandboxModeDefault):
		return WindowsSandboxModeUnelevated
	case string(WindowsSandboxModeElevated):
		return WindowsSandboxModeElevated
	case string(WindowsSandboxModeUnelevated):
		return WindowsSandboxModeUnelevated
	case string(WindowsSandboxModeDisabled):
		return WindowsSandboxModeDisabled
	default:
		return WindowsSandboxMode("")
	}
}
