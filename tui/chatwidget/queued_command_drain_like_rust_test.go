package chatwidget

import (
	"testing"

	codextui "codex_go/tui"
)

// rustQueueDrainContinue lists the Continue arms of
// ChatWidget::queued_command_drain_result
// (codex-rs/tui/src/chatwidget/slash_dispatch.rs:1298-1321), in upstream order,
// for every arm that has a Go carrier. SlashCommand::Daybreak (:1315) is
// deliberately absent: Go has no /daybreak carrier yet (round-90 立项 candidate),
// so the 23 carriers below are what can be pinned today.
var rustQueueDrainContinue = []codextui.Command{
	codextui.CommandIde,
	codextui.CommandStatus,
	codextui.CommandDaemon,
	codextui.CommandPwd,
	codextui.CommandUsage,
	codextui.CommandDebugConfig,
	codextui.CommandPs,
	codextui.CommandStop,
	codextui.CommandMemoryDrop,
	codextui.CommandMemoryUpdate,
	codextui.CommandMcp,
	codextui.CommandApps,
	codextui.CommandPlugins,
	codextui.CommandRollout,
	codextui.CommandCopy,
	codextui.CommandRaw,
	codextui.CommandVim,
	codextui.CommandDiff,
	codextui.CommandApp,
	codextui.CommandRename,
	codextui.CommandVoice,
	codextui.CommandRecap,
	codextui.CommandTestApproval,
}

// rustQueueDrainStop lists the Stop arms (slash_dispatch.rs:1333-1371) that have
// a Go carrier, in upstream order. The Go commands share a slash spelling with
// the upstream arm they stand in for, except where noted. Arms Go has no carrier
// for at all are named in the comment and are asserted to remain unresolvable by
// TestQueuedCommandDrainPinsRustStopArms, so adding a carrier forces this table
// to be revisited: Presets (:1346), Config (:1368), Tui (:1370).
var rustQueueDrainStop = []codextui.Command{
	codextui.CommandFeedback,
	codextui.CommandWarnings,
	codextui.CommandExport,
	codextui.CommandNew,
	codextui.CommandArchive,
	codextui.CommandDelete,
	codextui.CommandClear,
	codextui.CommandResume,
	codextui.CommandFork,
	codextui.CommandInit,
	codextui.CommandCompact,
	codextui.CommandReview,
	codextui.CommandModel,
	codextui.CommandPlan,
	codextui.CommandGoal,
	codextui.CommandSide, // SlashCommand::Side | SlashCommand::Btw (:1349-1350)
	codextui.CommandKeymap,
	codextui.CommandAgent, // SlashCommand::Agents | SlashCommand::MultiAgents (:1352-1353)
	codextui.CommandPermissions,
	codextui.CommandElevateSandbox,
	codextui.CommandExperimental,
	codextui.CommandAutoReview,
	codextui.CommandMemories,
	codextui.CommandExit, // SlashCommand::Quit | SlashCommand::Exit (:1359-1360)
	codextui.CommandLogout,
	codextui.CommandMention,
	codextui.CommandSkills,
	codextui.CommandImport,
	codextui.CommandHooks,
	codextui.CommandTitle,
	codextui.CommandStatusline,
	codextui.CommandTheme,
	codextui.CommandPets,
}

func TestQueuedCommandDrainPinsRustContinueArms(t *testing.T) {
	for _, command := range rustQueueDrainContinue {
		if got := QueuedCommandDrainResult(command, false, false); got != QueueDrainContinue {
			t.Fatalf("/%s queue drain = %q, want continue (slash_dispatch.rs:1298-1321)", command, got)
		}
	}
}

func TestQueuedCommandDrainPinsRustStopArms(t *testing.T) {
	for _, command := range rustQueueDrainStop {
		if got := QueuedCommandDrainResult(command, false, false); got != QueueDrainStop {
			t.Fatalf("/%s queue drain = %q, want stop (slash_dispatch.rs:1333-1371)", command, got)
		}
	}
}

func TestQueuedCommandDrainRustBusyGuardShortCircuits(t *testing.T) {
	for _, command := range []codextui.Command{codextui.CommandRaw, codextui.CommandCd, codextui.CommandWorktree} {
		if got := QueuedCommandDrainResult(command, true, false); got != QueueDrainStop {
			t.Fatalf("/%s pending-or-running queue drain = %q, want stop (slash_dispatch.rs:1294)", command, got)
		}
		if got := QueuedCommandDrainResult(command, false, true); got != QueueDrainStop {
			t.Fatalf("/%s modal queue drain = %q, want stop (slash_dispatch.rs:1294)", command, got)
		}
	}
}

func TestQueuedCommandDrainRustRuntimeArmsMatch(t *testing.T) {
	// SlashCommand::Cd (slash_dispatch.rs:1322-1325): Stop only when a thread is
	// bound and its working directory can still change; otherwise Continue.
	cases := []struct {
		name  string
		facts QueuedCommandDrainFacts
		want  QueueDrain
	}{
		{"no thread", QueuedCommandDrainFacts{CanChangeWorkingDirectory: true}, QueueDrainContinue},
		{"thread with fixed cwd", QueuedCommandDrainFacts{ThreadID: "t1"}, QueueDrainContinue},
		{"thread with changeable cwd", QueuedCommandDrainFacts{ThreadID: "t1", CanChangeWorkingDirectory: true}, QueueDrainStop},
	}
	for _, tc := range cases {
		if got := QueuedCommandDrainResultWithFacts(codextui.CommandCd, false, false, tc.facts); got != tc.want {
			t.Fatalf("/cd %s queue drain = %q, want %q (slash_dispatch.rs:1322-1325)", tc.name, got, tc.want)
		}
	}
	// SlashCommand::Worktree (slash_dispatch.rs:1326-1332): Stop only when a
	// managed worktree is available.
	if got := QueuedCommandDrainResultWithFacts(codextui.CommandWorktree, false, false, QueuedCommandDrainFacts{}); got != QueueDrainContinue {
		t.Fatalf("/worktree without a managed worktree queue drain = %q, want continue (slash_dispatch.rs:1326-1332)", got)
	}
	if got := QueuedCommandDrainResultWithFacts(codextui.CommandWorktree, false, false, QueuedCommandDrainFacts{ManagedWorktreeAvailable: true}); got != QueueDrainStop {
		t.Fatalf("/worktree with a managed worktree queue drain = %q, want stop (slash_dispatch.rs:1326-1332)", got)
	}
}
