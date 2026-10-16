package tea

import (
	"testing"

	codextui "codex_go/tui"
	bottompane "codex_go/tui/bottom_pane"
	chatwidget "codex_go/tui/chatwidget"
)

// The popup, the dispatcher and the tea model each used to keep their own copy of
// the availability tables, and the copies had drifted apart from each other and
// from upstream. Every consumer must now read codextui.Command
// (tui/slash_command_availability.go, mirroring slash_command.rs:203-219 and
// :249-313).
func TestSlashCommandAvailabilityConsumersAgreeLikeRust(t *testing.T) {
	for _, frame := range codextui.SlashCommandFrames() {
		item := bottompane.SlashCommandItem{Command: frame.Command}
		if got, want := item.AvailableDuringTask(), chatwidget.CommandAvailableDuringTask(frame.Command); got != want {
			t.Fatalf("/%s during-task: popup=%v chatwidget=%v", frame.Name, got, want)
		}
		if got, want := item.AvailableDuringTask(), frame.Command.AvailableDuringTask(); got != want {
			t.Fatalf("/%s during-task: popup=%v shared=%v", frame.Name, got, want)
		}
		if got, want := item.AvailableInSideConversation(), chatwidget.CommandAvailableInSideConversation(frame.Command); got != want {
			t.Fatalf("/%s side conversation: popup=%v chatwidget=%v", frame.Name, got, want)
		}
		if got, want := item.AvailableInSideConversation(), frame.Command.AvailableInSideConversation(); got != want {
			t.Fatalf("/%s side conversation: popup=%v shared=%v", frame.Name, got, want)
		}
	}
}
