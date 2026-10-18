package tea

import (
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"
)

// updateAndRunClipboard executes the command returned by a key event and feeds
// any resulting clipboard message back into the model.
func updateAndRunClipboard(t *testing.T, model *Model, msg bubbletea.Msg) {
	t.Helper()
	_, cmd := model.Update(msg)
	if cmd == nil {
		return
	}
	if result := cmd(); result != nil {
		model.Update(result)
	}
}
