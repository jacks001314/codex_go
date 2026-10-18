package tea

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
	"codex_go/utils"
)

func TestModelRendersAuthRecoveryHistoryLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-auth")
	model := NewModel(state, Options{Width: 80, Height: 20})

	model.Update(AuthRecoveryMsg{Message: "AWS session has expired. Reauthenticating..."})
	model.Update(AuthRecoveryMsg{Completed: true, Message: "Signed in with AWS."})

	view := utils.StripANSI(model.View())
	for _, want := range []string{
		"• AWS session has expired. Reauthenticating...",
		"✓ Signed in with AWS.",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("View() missing %q:\n%s", want, view)
		}
	}
}
