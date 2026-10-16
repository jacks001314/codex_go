package tui

import "testing"

// The false arm of Rust SlashCommand::available_during_task
// (codex-rs/tui/src/slash_command.rs:249-313), expressed in Go commands.
// Upstream also blocks /tui (:260) and /config (:312); neither has a Go carrier
// (section T5.11), so they cannot appear here.
var rustCommandsBlockedDuringTask = []Command{
	CommandNew, CommandDelete, CommandFork, CommandWorktree, CommandInit,
	CommandCompact, CommandRecap, CommandExport, CommandKeymap, CommandVim,
	CommandElevateSandbox, CommandExperimental, CommandMemories, CommandImport,
	CommandReview, CommandPlan, CommandCd, CommandClear, CommandLogout,
	CommandMemoryDrop, CommandMemoryUpdate, CommandTheme, CommandPets,
}

// Rust SlashCommand::available_in_side_conversation
// (codex-rs/tui/src/slash_command.rs:203-219): Copy, Agents, Export, Raw, Diff,
// Mention, Status, Daemon, Warnings, Pwd, Usage, Ide.
var rustCommandsAllowedInSideConversation = []Command{
	CommandCopy, CommandAgents, CommandExport, CommandRaw, CommandDiff,
	CommandMention, CommandStatus, CommandDaemon, CommandWarnings, CommandPwd,
	CommandUsage, CommandIde,
}

func TestCommandAvailabilityMatchesRustSlashCommandLists(t *testing.T) {
	blocked := map[Command]bool{}
	for _, command := range rustCommandsBlockedDuringTask {
		blocked[command] = true
	}
	allowed := map[Command]bool{}
	for _, command := range rustCommandsAllowedInSideConversation {
		allowed[command] = true
	}

	seen := map[Command]bool{}
	for _, frame := range SlashCommandFrames() {
		if seen[frame.Command] {
			continue
		}
		seen[frame.Command] = true
		if got, want := frame.Command.AvailableDuringTask(), !blocked[frame.Command]; got != want {
			t.Fatalf("/%s AvailableDuringTask() = %v, want %v (slash_command.rs:249-313)", frame.Name, got, want)
		}
		if got, want := frame.Command.AvailableInSideConversation(), allowed[frame.Command]; got != want {
			t.Fatalf("/%s AvailableInSideConversation() = %v, want %v (slash_command.rs:203-219)", frame.Name, got, want)
		}
	}

	// The two commands without a popup frame still answer for their upstream slot.
	if !CommandAgents.AvailableInSideConversation() {
		t.Fatal("CommandAgents must stay available in a side conversation (slash_command.rs:207)")
	}
	if CommandAgent.AvailableInSideConversation() {
		t.Fatal("CommandAgent (upstream MultiAgents/subagents) must not be side-conversation available")
	}
}
