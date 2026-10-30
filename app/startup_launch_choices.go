package app

// Rust parity: codex-rs/tui/src/app_server_session/startup_launch.rs (#50013).
// The first thread of a TUI process carries only the model, provider and
// reasoning-effort choices the launch made explicitly. Everything else stays
// unset so the app server resolves its own new-thread defaults; an explicit
// profile launch keeps the resolved-configuration path until server profile
// selection is available for connected launches.

import (
	"strings"

	"codex_go/appserver"
	"codex_go/cli"
)

// startupLaunchChoices mirrors Rust StartupLaunchChoices.
type startupLaunchChoices struct {
	model       string
	hasModel    bool
	provider    string
	hasProvider bool
	effort      any
	hasEffort   bool
	// selectedProfile keeps the resolved-configuration path (no field is
	// cleared or omitted beyond an explicit provider).
	selectedProfile bool
}

// startupLaunchChoicesFromLaunch mirrors StartupLaunchChoices::from_launch: the
// typed launch flags win, then the generic `-c` assignments with the last
// occurrence of a key winning.
func startupLaunchChoicesFromLaunch(root *cli.RootOptions) startupLaunchChoices {
	choices := startupLaunchChoices{}
	if root != nil {
		choices.selectedProfile = strings.TrimSpace(root.Shared.Profile) != ""
		if provider := strings.TrimSpace(root.Shared.OSSProvider); provider != "" {
			choices.provider = provider
			choices.hasProvider = true
		}
		if model := strings.TrimSpace(root.Shared.Model); model != "" {
			choices.model = model
			choices.hasModel = true
		}
	}
	if choices.selectedProfile {
		return choices
	}
	for _, override := range launchConfigOverrides(root) {
		switch override.Path {
		case "model":
			if text, ok := override.Value.(string); ok && strings.TrimSpace(text) != "" {
				choices.model = strings.TrimSpace(text)
				choices.hasModel = true
			}
		case "model_provider":
			if text, ok := override.Value.(string); ok && strings.TrimSpace(text) != "" {
				choices.provider = strings.TrimSpace(text)
				choices.hasProvider = true
			}
		case "model_reasoning_effort":
			choices.effort = override.Value
			choices.hasEffort = true
		}
	}
	return choices
}

// configure applies the launch choices to a thread/start request. An unselected
// model or reasoning effort is removed rather than forwarded, so a resolved
// client default cannot turn into an explicit server override.
func (c startupLaunchChoices) configure(params *appserver.ThreadStartParams) {
	if params == nil {
		return
	}
	if c.selectedProfile {
		if c.hasProvider {
			params.ModelProvider = c.provider
		}
		return
	}
	params.Model = c.model
	params.ModelProvider = c.provider
	if params.Config != nil {
		delete(params.Config, "model_reasoning_effort")
		if len(params.Config) == 0 {
			params.Config = nil
		}
	}
	if c.hasEffort {
		if params.Config == nil {
			params.Config = map[string]any{}
		}
		params.Config["model_reasoning_effort"] = c.effort
	}
}
