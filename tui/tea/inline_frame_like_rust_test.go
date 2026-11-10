package tea

import (
	"strings"
	"testing"

	"codex_go/protocol"
	codextui "codex_go/tui"
)

func inlineFrameModel(t *testing.T, width int, height int, alternateScreen string) *Model {
	t.Helper()
	state := codextui.NewState(nil)
	state.AddMessage(codextui.RoleUser, "ni hao a")
	state.AddMessage(codextui.RoleAssistant, "你好！有什么可以帮你的吗？比如看看代码、改改功能，或者跑下测试，随时说。")
	return NewModel(state, Options{Width: width, Height: height, AlternateScreenMode: alternateScreen})
}

func frameRows(view string) int {
	return strings.Count(view, "\n") + 1
}

// TestInlineFrameKeepsLastTerminalRowFreeLikeRust mirrors the inline
// (terminal-owned) transcript mode: the view must stay one row shorter than the
// terminal. Every row is padded to the full width, so a frame that reaches the
// last row leaves the cursor wrapped past the margin; Bubble Tea then scrolls
// the screen while painting, its relative cursor model drifts, and finalized
// output is printed a second time (the duplicate assistant reply).
func TestInlineFrameKeepsLastTerminalRowFreeLikeRust(t *testing.T) {
	composerLines := make([]string, 40)
	for index := range composerLines {
		composerLines[index] = "draft-row"
	}
	cases := []struct {
		name   string
		mutate func(*Model)
	}{
		{name: "idle", mutate: func(*Model) {}},
		{
			name: "long draft",
			mutate: func(model *Model) {
				model.composer.SetValue(strings.Join(composerLines, "\n"))
			},
		},
		{
			name: "running turn",
			mutate: func(model *Model) {
				model.Update(ThreadEventMsg{Event: protocol.TurnStarted()})
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			model := inlineFrameModel(t, 120, 24, "never")
			testCase.mutate(model)
			rows := frameRows(model.viewString())
			if rows > model.height-1 {
				t.Fatalf("inline frame rows = %d, want at most %d in a %d-row terminal", rows, model.height-1, model.height)
			}
		})
	}
}

// TestOwnedFrameStillFillsTerminalLikeRust keeps the alternate-screen layout
// unchanged: an owned screen repaints from an absolute origin, so its frame is
// allowed to use every terminal row.
func TestOwnedFrameStillFillsTerminalLikeRust(t *testing.T) {
	model := inlineFrameModel(t, 120, 24, "always")
	if rows := frameRows(model.viewString()); rows != model.height {
		t.Fatalf("owned frame rows = %d, want the full %d-row terminal", rows, model.height)
	}
}
