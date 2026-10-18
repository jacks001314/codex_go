package bottompane

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
)

// Rust #51207: BuiltinCommandFlags::daybreak_command_description gates the
// popup row, while find_builtin_command keeps a typed /daybreak dispatchable.
func TestDaybreakCommandVisibilityFollowsDescriptionLikeRust(t *testing.T) {
	flags := BuiltinCommandFlags{
		CollaborationModesEnabled:   true,
		ConnectorsEnabled:           true,
		PluginsCommandEnabled:       true,
		TokenActivityCommandEnabled: true,
		ServiceTierCommandsEnabled:  true,
		GoalCommandEnabled:          true,
		AllowElevateSandbox:         true,
	}
	if builtinCommandVisible(BuiltinsForInput(flags), "daybreak") {
		t.Fatal("/daybreak must be hidden when daybreak_command_description is nil")
	}
	if command, ok := FindBuiltinCommand("daybreak", flags); !ok || command.Command != codextui.CommandDaybreak {
		t.Fatalf("typed /daybreak lookup = %#v ok=%v, want dispatchable", command, ok)
	}

	description := "Enable broader access for cybersecurity work"
	flags.DaybreakCommandDescription = &description
	item, ok := builtinCommandByName(BuiltinsForInput(flags), "daybreak")
	if !ok {
		t.Fatal("/daybreak missing while daybreak_command_description is set")
	}
	if item.Description != description {
		t.Fatalf("/daybreak description = %q, want %q", item.Description, description)
	}

	popup := NewCommandPopup(CommandPopupFlags{DaybreakCommandDescription: &description}, nil)
	found := false
	for _, row := range popup.Rows(120) {
		if rowContainsSlashCommand(row, "daybreak") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("command popup did not carry /daybreak with an enabled description")
	}
}

func builtinCommandVisible(items []SlashCommandItem, name string) bool {
	_, ok := builtinCommandByName(items, name)
	return ok
}

func builtinCommandByName(items []SlashCommandItem, name string) (SlashCommandItem, bool) {
	for _, item := range items {
		if item.Name == name {
			return item, true
		}
	}
	return SlashCommandItem{}, false
}

func rowContainsSlashCommand(row, name string) bool {
	return strings.Contains(stripANSIForSelectionTest(row), "/"+name)
}
