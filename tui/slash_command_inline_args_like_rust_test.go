package tui

import "testing"

// Rust keeps exactly one list of inline-argument commands
// (SlashCommand::supports_inline_args, codex-rs/tui/src/slash_command.rs:172-201)
// and the command popup reads it back through SlashCommandItem
// (bottom_pane/slash_commands.rs:34-39). Batch T2-8b made Go match that shape:
// chatwidget.CommandSupportsInlineArgs and SlashCommandItem.SupportsInlineArgs
// both delegate to Command.SupportsInlineArgs, and this test pins the list
// against Rust's names so the two Go copies cannot drift again.
func TestCommandSupportsInlineArgsMatchesRustSlashCommandList(t *testing.T) {
	// Codex-rs/tui/src/slash_command.rs:173-199, in Rust's order. Rust spells
	// `/btw` as its own variant; Go folds it onto CommandSide
	// (slash_command.go:49-50).
	rustInline := []string{
		"review", "model", "permissions", "title", "statusline", "rename", "new",
		"clear", "fork", "plan", "goal", "voice", "ide", "keymap", "mcp", "export",
		"raw", "cd", "pwd", "usage", "pets", "side", "btw", "resume",
	}
	inline := make(map[string]bool, len(rustInline))
	for _, name := range rustInline {
		inline[name] = true
	}
	seen := make(map[string]bool, len(rustInline))
	for _, frame := range SlashCommandFrames() {
		if inline[frame.Name] {
			seen[frame.Name] = true
		}
		if got, want := frame.Command.SupportsInlineArgs(), inline[frame.Name]; got != want {
			t.Fatalf("/%s SupportsInlineArgs() = %v, want %v (codex-rs/tui/src/slash_command.rs:172-201)", frame.Name, got, want)
		}
	}
	for _, name := range rustInline {
		if !seen[name] {
			t.Fatalf("Rust lists /%s in supports_inline_args but the Go builtins have no such frame", name)
		}
	}
}

// The Go-only direct-argument commands stay outside Rust's list, so the shared
// list must not swallow them.
func TestCommandSupportsInlineArgsExcludesGoOnlyCommands(t *testing.T) {
	for _, command := range []Command{
		CommandApproval,
		CommandSandbox,
		CommandAttach,
		CommandImage,
		CommandURLImage,
		CommandWorktree,
		CommandCompact,
		CommandRecap,
		CommandLogout,
	} {
		if command.SupportsInlineArgs() {
			t.Fatalf("%q SupportsInlineArgs() = true, want false (not in codex-rs/tui/src/slash_command.rs:172-201)", string(command))
		}
	}
}
