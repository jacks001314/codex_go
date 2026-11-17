package tea

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	codextui "codex_go/tui"
)

const sidePaneLongProviderError = "unexpected status 400 Bad Request: litellm.BadRequestError: Hosted LiteLLM: model group 'deepseek-v4-flash' failed with the error above."

// Rust keeps history cells and asks each one for display_lines(width) at draw
// time, so a turn error always wraps inside the pane that paints it. Go
// prepared the cell's rows once at append time (at the full terminal width) and
// the split view then clipped them at the divider, cutting provider errors
// mid-word the way /side used to show them.
func TestSidePaneWrapsPreparedHistoryCellLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 120, Height: 24})
	model.featureSettings = map[string]bool{sideWorkspaceFeatureKey: true}
	model.State.SetThreadID("root")
	model.State.AddMessage(codextui.RoleUser, "hello from the main chat")
	// The error arrives while the frame is still 120 columns wide, before the
	// focused column re-lays the transcript out at its own width.
	model.addErrorHistoryMessage(sidePaneLongProviderError)

	model.activeSide = &activeSideConversation{
		ParentThreadID: "root",
		SideThreadID:   "side",
		ParentMessages: []codextui.Message{{Role: codextui.RoleUser, Text: "parent line"}},
		ShowingSide:    true,
	}
	model.refreshTranscript()

	left, right, _, ok := model.sidePaneLayout(model.width)
	if !ok || right <= 0 {
		t.Fatalf("split layout unavailable: left=%d right=%d ok=%t", left, right, ok)
	}
	if right >= ansi.StringWidth(sidePaneLongProviderError) {
		t.Fatalf("pane width %d cannot exercise the wrap case", right)
	}

	rows := strings.Split(model.viewString(), "\n")
	var pane strings.Builder
	for index, row := range rows {
		if width := ansi.StringWidth(row); width != model.width {
			t.Fatalf("row %d width = %d, want %d", index, width, model.width)
		}
		pane.WriteString(ansi.Strip(ansi.Cut(row, left+sideWorkspaceDividerWidth, model.width)))
		pane.WriteString("\n")
	}
	// Every row of the message survives: nothing was clipped at the divider.
	flattened := strings.Join(strings.Fields(pane.String()), " ")
	if !strings.Contains(flattened, sidePaneLongProviderError) {
		t.Fatalf("side pane clipped the error instead of wrapping it:\n%s", pane.String())
	}
	// Rust's new_error_event paints a single red bullet row; Go must not add an
	// "Error: " prefix the Rust cell never writes.
	if strings.Contains(flattened, "\u25a0 Error: ") {
		t.Fatalf("error cell kept the doubled prefix:\n%s", pane.String())
	}
}
