package tea

import "strings"

// Rust parity: codex-rs/tui/src/slash_command_settings.rs (#63036).
//
// Named settings are shared by slash-command submission and composer
// completion, so a suggested flag or value always parses.
type slashCommandSettings struct {
	values   []string
	switches []string
	booleans []string
}

func (s slashCommandSettings) empty() bool {
	return len(s.values) == 0 && len(s.switches) == 0 && len(s.booleans) == 0
}

// slashSettingsForCommand mirrors `slash_command_settings::for_command`.
func slashSettingsForCommand(command string) slashCommandSettings {
	switch strings.ToLower(strings.TrimSpace(command)) {
	case "model":
		return slashCommandSettings{values: []string{"--reasoning", "--speed"}}
	case "permissions":
		return slashCommandSettings{values: []string{"--approval-policy", "--reviewer"}}
	case "title":
		return slashCommandSettings{switches: []string{"--clear"}}
	case "statusline":
		return slashCommandSettings{switches: []string{"--clear"}, booleans: []string{"colors"}}
	case "mcp":
		return slashCommandSettings{values: []string{"--environment"}}
	default:
		return slashCommandSettings{}
	}
}

// slashCommandValueChoices mirrors `slash_command_settings::value_choices`.
func slashCommandValueChoices(command, name string) []string {
	switch strings.ToLower(strings.TrimSpace(command)) {
	case "permissions":
		switch name {
		case "--approval-policy":
			return []string{"untrusted", "on-request", "never"}
		case "--reviewer":
			return []string{"user", "auto_review"}
		}
	case "model":
		if name == "--speed" {
			return []string{"standard", "flex"}
		}
	}
	return nil
}
