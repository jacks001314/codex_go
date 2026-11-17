package tea

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	codextui "codex_go/tui"
)

// Rust wraps Markdown prose with `break_words` enabled, so a token that cannot
// fit breaks across rows inside whichever pane paints it. Go kept it as one
// over-wide row and the pane clipped it, which is the truncated JSON, hash, and
// provider text users saw in `/side`.
const sidePaneLongProseToken = `payload=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef`

func sidePaneLongTokenModel(width int, height int) *Model {
	model := NewModel(codextui.NewState(nil), Options{Width: width, Height: height})
	model.featureSettings = map[string]bool{sideWorkspaceFeatureKey: true}
	model.State.SetThreadID("root")
	model.State.AddMessage(codextui.RoleAssistant, "snapshot "+sidePaneLongProseToken)
	model.activeSide = &activeSideConversation{
		ParentThreadID: "root",
		SideThreadID:   "side",
		ParentMessages: []codextui.Message{{Role: codextui.RoleUser, Text: "parent line"}},
		ShowingSide:    true,
	}
	return model
}

func TestSidePaneWrapsLongProseTokenLikeRust(t *testing.T) {
	model := sidePaneLongTokenModel(170, 30)
	left, right, _, ok := model.sidePaneLayout(model.width)
	if !ok || right <= 0 {
		t.Fatalf("split layout unavailable: left=%d right=%d ok=%t", left, right, ok)
	}
	if right >= ansi.StringWidth(sidePaneLongProseToken) {
		t.Fatalf("pane width %d cannot exercise the wrap case", right)
	}
	var pane strings.Builder
	for index, row := range strings.Split(model.viewString(), "\n") {
		if width := ansi.StringWidth(row); width != model.width {
			t.Fatalf("row %d width = %d, want %d", index, width, model.width)
		}
		pane.WriteString(ansi.Strip(ansi.Cut(row, left+sideWorkspaceDividerWidth, model.width)))
		pane.WriteString("\n")
	}
	if !strings.Contains(strings.Join(strings.Fields(pane.String()), ""), sidePaneLongProseToken) {
		t.Fatalf("side pane clipped the token instead of wrapping it:\n%s", pane.String())
	}
}

func TestNarrowSideChatWrapsLongProseTokenLikeRust(t *testing.T) {
	model := sidePaneLongTokenModel(62, 30)
	rows := strings.Split(model.viewString(), "\n")
	if len(rows) != 30 {
		t.Fatalf("frame height %d, want 30", len(rows))
	}
	flat := ansi.Strip(strings.Join(rows, "\n"))
	if !strings.Contains(strings.Join(strings.Fields(flat), ""), sidePaneLongProseToken) {
		t.Fatalf("narrow side chat clipped the token instead of wrapping it:\n%s", flat)
	}
}
