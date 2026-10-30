package tea

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
	"codex_go/utils"
)

// Mirrors Rust #58127: the startup help block is a tooltip too. A fresh
// session's first event renders it only when `tui.show_tooltips` is enabled,
// while a resumed session keeps the rotating tooltip path.
func TestModelStartupHelpHonorsTooltipPreferenceLikeRust(t *testing.T) {
	for _, test := range []struct {
		name         string
		threadID     string
		showTooltips bool
		wantHelp     bool
		wantTip      bool
	}{
		{name: "fresh tooltips on", showTooltips: true, wantHelp: true},
		{name: "fresh tooltips off", showTooltips: false},
		{name: "resumed tooltips on", threadID: "thread-resumed", showTooltips: true, wantTip: true},
		{name: "resumed tooltips off", threadID: "thread-resumed", showTooltips: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := codextui.NewState(nil)
			state.ThreadID = test.threadID
			show := test.showTooltips
			fullscreen := false
			model := NewModel(state, Options{
				Width: 100, Height: 30, ShowSessionHeader: true, ShowTooltips: &show,
				FullscreenTranscript: &fullscreen,
			})
			model.rawOutput = true
			view := utils.StripANSI(model.View())
			if got := strings.Contains(view, "To get started, describe a task"); got != test.wantHelp {
				t.Fatalf("startup help present = %v, want %v:\n%s", got, test.wantHelp, view)
			}
			if got := strings.Contains(view, "Tip: "); got != test.wantTip {
				t.Fatalf("startup tip present = %v, want %v:\n%s", got, test.wantTip, view)
			}
		})
	}
}
