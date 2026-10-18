package tui

// Availability predicates shared by every consumer, mirroring
// codex-rs/tui/src/slash_command.rs. The command popup (tui/bottom_pane), the
// dispatcher (tui/chatwidget) and the tea model each kept their own copy of
// these lists, and the copies had drifted apart from each other *and* from
// upstream (round-90 batch T5, section T5.13 of update/r90tui_2026_10_16.md).
// Every consumer now reads these two methods.

// AvailableDuringTask reports whether the command can run while a task is in
// progress; mirrors SlashCommand::available_during_task
// (codex-rs/tui/src/slash_command.rs:249-313). Everything not listed here runs
// during a task. /archive is deliberately absent, so it stays available while a
// turn runs (Rust #50764).
//
// Upstream also blocks /config (:312); the Go settings panel is read-only for
// some rows but still owns the command surface.
func (c Command) AvailableDuringTask() bool {
	switch c {
	case CommandNew,
		CommandDelete,
		CommandFork,
		CommandWorktree,
		CommandInit,
		CommandCompact,
		CommandRecap,
		CommandExport,
		CommandKeymap,
		CommandVim,
		CommandTUI,
		CommandElevateSandbox,
		CommandExperimental,
		CommandMemories,
		CommandImport,
		CommandReview,
		CommandPlan,
		CommandCd,
		CommandClear,
		CommandLogout,
		CommandMemoryDrop,
		CommandMemoryUpdate,
		CommandTheme,
		CommandPets,
		CommandConfig:
		return false
	default:
		return true
	}
}

// AvailableInSideConversation reports whether the command stays available inside
// an active side conversation; mirrors SlashCommand::available_in_side_conversation
// (codex-rs/tui/src/slash_command.rs:203-219).
//
// Upstream lists SlashCommand::Agents, which Go routes as CommandAgents
// (tui/state.go:951) while none of the popup frames maps to it yet, because of
// the /agent vs /agents drift tracked in section T5 D-1; the predicate already
// answers for the upstream slot.
func (c Command) AvailableInSideConversation() bool {
	switch c {
	case CommandCopy,
		CommandAgents,
		CommandExport,
		CommandRaw,
		CommandDiff,
		CommandMention,
		CommandStatus,
		CommandDaemon,
		CommandWarnings,
		CommandPwd,
		CommandUsage,
		CommandIde:
		return true
	default:
		return false
	}
}
