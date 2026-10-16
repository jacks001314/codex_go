package tui

// SupportsInlineArgs reports whether the command takes inline arguments, so
// `/cmd <args>` is dispatched locally instead of being sent to the model as a
// prompt (Rust SlashCommand::supports_inline_args,
// codex-rs/tui/src/slash_command.rs:172-201).
//
// This method is the single source of truth for the whole Go TUI: the
// chatwidget dispatcher (chatwidget/slash_dispatch.go:203,364) and the command
// popup (bottom_pane/slash_commands.go, Rust
// bottom_pane/slash_commands.rs:34-39) both consult it, exactly as Rust's
// SlashCommandItem delegates to SlashCommand::supports_inline_args.
//
// Rust spells `/btw` as its own SlashCommand::Btw variant; Go folds `/btw` onto
// CommandSide (slash_command.go:49-50, state.go:1079), so the two lists are equal
// up to that alias.
func (c Command) SupportsInlineArgs() bool {
	switch c {
	case CommandReview,
		CommandModel,
		CommandPermissions,
		CommandTitle,
		CommandStatusline,
		CommandRename,
		CommandNew,
		CommandClear,
		CommandFork,
		CommandPlan,
		CommandGoal,
		CommandVoice,
		CommandIde,
		CommandKeymap,
		CommandMcp,
		CommandExport,
		CommandRaw,
		CommandCd,
		CommandPwd,
		CommandUsage,
		CommandPets,
		CommandSide,
		CommandResume:
		return true
	default:
		return false
	}
}
