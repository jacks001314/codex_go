package bottompane

import (
	"reflect"
	"testing"

	codextui "codex_go/tui"
)

func TestRustSlashCommandOrderBaseline20260728(t *testing.T) {
	// Upstream SlashCommand enum order (codex-rs/tui/src/slash_command.rs:12-89),
	// re-anchored to head 4eb4a65407 (round-90). The names without a Go
	// carrier yet are inert and recorded in rustSlashCommandsWithoutFrame;
	// D-1 closed: the Agents slot now carries the upstream "agents" spelling.
	want := []string{
		"model", "presets", "daybreak", "ide", "permissions", "keymap", "vim",
		"setup-default-sandbox", "experimental", "approve", "memories", "skills",
		"import", "hooks", "review", "rename", "new", "archive", "delete", "resume",
		"fork", "worktree", "app", "init", "compact", "config", "recap", "plan",
		"voice", "goal", "agents", "side", "btw", "copy", "export", "raw", "tui",
		"diff", "mention", "status", "daemon", "warnings", "cd", "pwd", "usage",
		"debug-config", "title", "statusline", "theme", "pets", "mcp", "apps",
		"plugins", "logout", "quit", "exit", "feedback", "rollout", "ps", "stop",
		"clear", "test-approval", "subagents", "debug-m-drop", "debug-m-update",
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
		codextui.CommandReview:     true,
		codextui.CommandRename:     true,
		codextui.CommandNew:        true,
		codextui.CommandClear:      true,
		codextui.CommandPlan:       true,
		codextui.CommandGoal:       true,
		codextui.CommandIde:        true,
		codextui.CommandKeymap:     true,
		codextui.CommandMcp:        true,
		codextui.CommandRaw:        true,
		codextui.CommandUsage:      true,
		codextui.CommandPets:       true,
		codextui.CommandSide:       true,
		codextui.CommandResume:     true,
		codextui.CommandTitle:      true,
		codextui.CommandStatusline: true,

		// Rust #54378 adds Model and #54383 adds Permissions to
		// slash_command.rs supports_inline_args.
		codextui.CommandModel:       true,
		codextui.CommandPermissions: true,

		// Batch T2-8b made the command popup and the chatwidget dispatcher read
		// one shared list (codextui.Command.SupportsInlineArgs) so both sides
		// agree; batch T5 added the upstream entries that were missing from
		// rustSlashCommandOrder, so /voice, /pwd, /export, /cd, /recap,
		// /worktree and /daemon now reach the popup as well.
		codextui.CommandVoice:  true,
		codextui.CommandPwd:    true,
		codextui.CommandExport: true,
		codextui.CommandFork:   true,
		codextui.CommandCd:     true,
	}
	flags := BuiltinCommandFlags{
		CollaborationModesEnabled:   true,
		ConnectorsEnabled:           true,
		PluginsCommandEnabled:       true,
		TokenActivityCommandEnabled: true,
		GoalCommandEnabled:          true,
		AllowElevateSandbox:         true,
	}
	for _, item := range BuiltinsForInput(flags) {
		if got := item.SupportsInlineArgs(); got != want[item.Command] {
			t.Fatalf("/%s SupportsInlineArgs() = %v, want %v", item.Name, got, want[item.Command])
		}
	}
}

// TestRustSlashPopupInventoryFollowsRustOrder pins the popup inventory to the
// upstream enum order: every name in rustSlashCommandOrder either has a Go frame
// or is a documented pending carrier, and the inventory is exactly that table
// filtered to the frames that are available.
func TestRustSlashPopupInventoryFollowsRustOrder(t *testing.T) {
	flags := BuiltinCommandFlags{
		CollaborationModesEnabled:   true,
		ConnectorsEnabled:           true,
		PluginsCommandEnabled:       true,
		TokenActivityCommandEnabled: true,
		ServiceTierCommandsEnabled:  true,
		GoalCommandEnabled:          true,
		AllowElevateSandbox:         true,
	}

	frames := map[string]codextui.SlashCommandFrame{}
	for _, frame := range codextui.SlashCommandFrames() {
		frames[frame.Name] = frame
	}

	// Commands the Rust popup does not have: they stay dispatchable but never
	// enter the inventory.
	compatOnly := map[string]bool{
		"help": true, "approval": true, "sandbox": true, "unarchive": true,
		"attach": true, "image": true, "url-image": true,
		"clear-attachments": true, "editor": true,
	}

	for _, name := range rustSlashCommandOrder {
		_, hasFrame := frames[name]
		pending := rustSlashCommandsWithoutFrame[name]
		if !hasFrame && !pending {
			t.Fatalf("popup order entry %q has no frame and is not a documented pending carrier", name)
		}
		if hasFrame && pending {
			t.Fatalf("popup order entry %q now has a frame; drop it from rustSlashCommandsWithoutFrame", name)
		}
	}

	expected := make([]string, 0, len(rustSlashCommandOrder))
	for _, name := range rustSlashCommandOrder {
		frame, ok := frames[name]
		if !ok || !builtinFrameAvailable(frame, flags) {
			continue
		}
		expected = append(expected, frame.Name)
	}
	got := make([]string, 0, len(expected))
	for _, item := range BuiltinsForInput(flags) {
		got = append(got, item.Name)
	}
	if !reflect.DeepEqual(got, expected) {
		t.Fatalf("popup inventory drifted from the upstream enum order:\n got: %#v\nwant: %#v", got, expected)
	}

	// The upstream commands batch T5 added to the order table must now be in the
	// inventory; before the fix they were silently absent.
	visible := map[string]bool{}
	for _, name := range got {
		visible[name] = true
	}
	for _, name := range []string{"voice", "export", "cd", "pwd", "daemon", "recap", "worktree"} {
		if !visible[name] {
			t.Fatalf("upstream command %q missing from popup inventory", name)
		}
	}

	// The commands T5 made visible must carry the upstream description text
	// (codex-rs/tui/src/slash_command.rs:93-162). /voice was a Go-local
	// paraphrase until upstream 9c9451131f (#44622) rewrote it.
	descriptions := map[string]string{
		"voice":    "start or stop voice; use /voice settings to choose a voice",
		"export":   "export the conversation as markdown",
		"cd":       "change the current working directory",
		"pwd":      "show the current working directory",
		"daemon":   "Manage the local background server",
		"recap":    "summarize the current conversation now",
		"worktree": "start or continue a conversation in a new worktree",
		// Popup text that T5 re-aligned to upstream HEAD: /app (50b8213385 #33901),
		// /archive (d390f0a09c #44337), /delete (08e49689b8 #44711),
		// /copy (52e73e3a54 #43055), /mcp (4773a132c3 #49290),
		// /subagents (0c14c73471 #39088).
		"app":       "continue this session in the Desktop app",
		"archive":   "archive this session",
		"delete":    "permanently delete this session",
		"copy":      "copy the last response or part of it",
		"mcp":       "list MCP tools; use /mcp verbose or /mcp login <name>",
		"subagents": "switch between this session's subagents",
		// D-1: the Agents slot uses the upstream name and description
		// (slash_command.rs:47/:144), not the legacy "agent" frame.
		"agents": "open the agent command center",
	}
	for _, item := range BuiltinsForInput(flags) {
		want, tracked := descriptions[item.Name]
		if !tracked {
			continue
		}
		if item.Description != want {
			t.Fatalf("/%s popup description = %q, want %q", item.Name, item.Description, want)
		}
	}

	// A frame that is available but reaches neither the order table nor the
	// compatibility list would be dropped from the popup silently; catch that.
	for name, frame := range frames {
		if compatOnly[name] || !builtinFrameAvailable(frame, flags) {
			continue
		}
		if !visible[name] {
			t.Fatalf("frame %q is available but absent from the popup inventory (add it to rustSlashCommandOrder)", name)
		}
	}
}
