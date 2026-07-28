package bottompane

import (
	"reflect"
	"testing"

	codextui "codex_go/tui"
)

func TestRustSlashCommandOrderBaseline20260728(t *testing.T) {
	want := []string{
		"model", "ide", "permissions", "keymap", "vim", "setup-default-sandbox",
		"sandbox-add-read-dir", "experimental", "approve", "memories", "skills",
		"import", "hooks", "review", "rename", "new", "archive", "delete", "resume",
		"fork", "app", "init", "compact", "plan", "goal", "agent", "side", "btw",
		"copy", "raw", "diff", "mention", "status", "usage", "debug-config", "title",
		"statusline", "theme", "pets", "mcp", "apps", "plugins", "logout", "quit",
		"exit", "feedback", "rollout", "ps", "stop", "clear", "personality",
		"test-approval", "subagents", "debug-m-drop", "debug-m-update",
	}
	if !reflect.DeepEqual(rustSlashCommandOrder, want) {
		t.Fatalf("Rust slash command order drifted:\n got: %#v\nwant: %#v", rustSlashCommandOrder, want)
	}
}

func TestRustSlashPopupUsesCanonicalNamesAndKeepsCompatibilityAliasesForDispatch(t *testing.T) {
	flags := BuiltinCommandFlags{
		CollaborationModesEnabled:   true,
		ConnectorsEnabled:           true,
		PluginsCommandEnabled:       true,
		TokenActivityCommandEnabled: true,
		ServiceTierCommandsEnabled:  true,
		GoalCommandEnabled:          true,
		PersonalityCommandEnabled:   true,
		AllowElevateSandbox:         true,
	}

	visible := map[string]bool{}
	for _, item := range BuiltinsForInput(flags) {
		visible[item.Name] = true
		if item.IsAlias {
			t.Fatalf("popup inventory contains dispatch alias: %#v", item)
		}
	}
	for _, name := range []string{"subagents", "stop", "pets"} {
		if !visible[name] {
			t.Fatalf("Rust canonical command %q missing from popup inventory", name)
		}
	}
	for _, name := range []string{"multi-agents", "clean", "pet", "help", "approval", "sandbox", "unarchive", "attach", "image", "url-image", "clear-attachments", "editor"} {
		if visible[name] {
			t.Fatalf("compatibility command %q leaked into Rust popup inventory", name)
		}
	}

	aliases := map[string]codextui.Command{
		"clean": codextui.CommandStop,
		"pet":   codextui.CommandPets,
		"keys":  codextui.CommandKeymap,
	}
	for name, wantCommand := range aliases {
		item, ok := FindBuiltinCommand(name, flags)
		if !ok || item.Command != wantCommand || !item.IsAlias {
			t.Fatalf("dispatch alias %q = %#v ok=%v, want command %q", name, item, ok, wantCommand)
		}
	}
}

func TestRustSlashInlineArgContractIsExhaustive(t *testing.T) {
	want := map[codextui.Command]bool{
		codextui.CommandReview:          true,
		codextui.CommandRename:          true,
		codextui.CommandNew:             true,
		codextui.CommandClear:           true,
		codextui.CommandPlan:            true,
		codextui.CommandGoal:            true,
		codextui.CommandIde:             true,
		codextui.CommandKeymap:          true,
		codextui.CommandMcp:             true,
		codextui.CommandRaw:             true,
		codextui.CommandUsage:           true,
		codextui.CommandPets:            true,
		codextui.CommandSide:            true,
		codextui.CommandResume:          true,
		codextui.CommandSandboxReadRoot: true,
	}
	flags := BuiltinCommandFlags{
		CollaborationModesEnabled:   true,
		ConnectorsEnabled:           true,
		PluginsCommandEnabled:       true,
		TokenActivityCommandEnabled: true,
		GoalCommandEnabled:          true,
		PersonalityCommandEnabled:   true,
		AllowElevateSandbox:         true,
	}
	for _, item := range BuiltinsForInput(flags) {
		if got := item.SupportsInlineArgs(); got != want[item.Command] {
			t.Fatalf("/%s SupportsInlineArgs() = %v, want %v", item.Name, got, want[item.Command])
		}
	}
}
