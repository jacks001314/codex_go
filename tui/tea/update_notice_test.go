package tea

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
	historycell "codex_go/tui/history_cell"
)

func TestNewModelAddsInitialHistoryCells(t *testing.T) {
	state := codextui.NewState(nil)
	NewModel(state, Options{
		InitialHistoryCells: []historycell.HistoryCell{
			historycell.NewUpdateAvailable("1.0.0", "2.0.0", "npm install -g @jacks001314/codex-go@latest"),
		},
	})
	if len(state.Messages) == 0 {
		t.Fatal("initial update history cell was not added")
	}
	message := state.Messages[len(state.Messages)-1]
	if !strings.Contains(message.RawText, "Update available!") || !strings.Contains(message.RawText, "1.0.0 -> 2.0.0") {
		t.Fatalf("initial update message = %#v", message)
	}
}
