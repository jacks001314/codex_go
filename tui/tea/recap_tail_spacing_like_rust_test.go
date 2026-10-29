package tea

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
	historycell "codex_go/tui/history_cell"
	"codex_go/utils"
)

func recapTailTestModel(t *testing.T, noAltScreen bool) *Model {
	t.Helper()
	state := codextui.NewState(nil)
	state.SetThreadID("thread-1")
	state.AddHistoryLines([]string{"History line"}, []string{"History line"})
	model := NewModel(state, Options{Width: 80, Height: 12, NoAltScreen: noAltScreen})
	nextAction := "Review the changes."
	model.applyHistoryCell(styledRecapCell{historycell.NewThreadRecapHistoryCell("The draft is ready.").WithNextAction(&nextAction)})
	return model
}

// Rust #47355 reserves a blank layout row below a recap at the transcript
// tail, but not below a recap followed by completed or live content.
func TestRecapTailSpacingLikeRust(t *testing.T) {
	t.Run("tail", func(t *testing.T) {
		model := recapTailTestModel(t, false)
		if !model.State.Messages[len(model.State.Messages)-1].Recap {
			t.Fatal("recap metadata was not retained on the transcript message")
		}
		content := utils.StripANSI(model.lastTranscriptContent)
		if !strings.HasSuffix(content, "\n") {
			t.Fatalf("recap tail has no reserved row:\n%s", content)
		}
		lines := strings.Split(content, "\n")
		if len(lines) < 2 || strings.TrimSpace(lines[len(lines)-1]) != "" || strings.TrimSpace(lines[len(lines)-2]) == "" {
			t.Fatalf("recap tail spacing is not one blank row: %#v", lines)
		}
	})

	t.Run("completed follow-up", func(t *testing.T) {
		model := recapTailTestModel(t, false)
		model.State.AddMessage(codextui.RoleAssistant, "Follow-up response.")
		model.refreshTranscript()
		content := utils.StripANSI(model.lastTranscriptContent)
		if strings.HasSuffix(content, "\n") {
			t.Fatalf("recap tail spacing survived a completed follow-up:\n%s", content)
		}
		if !strings.Contains(content, "Review the changes.\n\n") || strings.Contains(content, "Review the changes.\n\n\n") {
			t.Fatalf("recap and follow-up did not keep one separator:\n%s", content)
		}
	})

	t.Run("live follow-up", func(t *testing.T) {
		model := recapTailTestModel(t, false)
		model.Transcript.activeAssistantDeltaItemID = "live-message"
		model.refreshTranscript()
		if content := utils.StripANSI(model.lastTranscriptContent); strings.HasSuffix(content, "\n") {
			t.Fatalf("recap tail spacing survived a live follow-up:\n%s", content)
		}
	})

	t.Run("inline transcript", func(t *testing.T) {
		model := recapTailTestModel(t, true)
		if content := utils.StripANSI(model.lastTranscriptContent); strings.HasSuffix(content, "\n") {
			t.Fatalf("inline transcript reserved recap spacing:\n%s", content)
		}
	})

	t.Run("single-row viewport", func(t *testing.T) {
		model := recapTailTestModel(t, false)
		content := model.lastTranscriptContent
		model.transcript.Height = 1
		if got := model.reserveRecapTailSpacing(content); got != content {
			t.Fatalf("single-row viewport reserved recap spacing:\n%s", got)
		}
	})
}
