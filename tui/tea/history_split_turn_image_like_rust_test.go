package tea

import (
	"testing"

	codextui "codex_go/tui"
)

// TestOlderHistoryKeepsSplitTurnImageWithItsAnswerLikeRust covers Rust #61919
// split_image_insertion: when the loaded window already holds a turn's boundary
// row and an older page carries that turn's beginning, the page's generated
// image lands directly before the boundary row instead of at the top.
func TestOlderHistoryKeepsSplitTurnImageWithItsAnswerLikeRust(t *testing.T) {
	page := OlderHistoryPage{
		Messages: []codextui.Message{
			{Role: codextui.RoleUser, Text: "older-prompt"},
			{Role: codextui.RoleAssistant, Text: "older-answer"},
		},
		SplitTurnID: "turn-split",
		SplitImages: []codextui.Message{{
			Role:           codextui.RoleHistory,
			Text:           "Generated Image cat",
			ImagePath:      "/tmp/cat.png",
			ImageGenerated: true,
		}},
	}
	loader := func(string, string) (OlderHistoryPage, error) { return page, nil }
	model := historyPaginationModel(t, loader)
	model.transcriptHistoryPartial = true
	model.olderHistoryCursor = "cursor-1"
	model.State.Messages = append(model.State.Messages,
		codextui.Message{Role: codextui.RoleAssistant, Text: "answer-of-split-turn"},
		codextui.Message{Role: codextui.RoleHistory, Text: "turn separator", TurnID: "turn-split"},
		codextui.Message{Role: codextui.RoleUser, Text: "next-prompt"},
	)
	model.refreshTranscript()

	model.applyOlderHistoryPage("thread-history", page, nil)

	separator, image := -1, -1
	for index, message := range model.State.Messages {
		switch {
		case message.TurnID == "turn-split":
			separator = index
		case message.ImageGenerated && message.ImagePath == "/tmp/cat.png":
			image = index
		}
	}
	if separator < 0 || image < 0 {
		t.Fatalf("separator=%d image=%d in %d messages", separator, image, len(model.State.Messages))
	}
	if image != separator-1 {
		t.Fatalf("split image index %d, want directly before the separator %d", image, separator)
	}
	if prev := model.State.Messages[image-1]; prev.Text != "answer-of-split-turn" {
		t.Fatalf("row before the split image = %q", prev.Text)
	}
	if first := model.State.Messages[0]; first.Text != "older-prompt" {
		t.Fatalf("prepended page row = %q", first.Text)
	}
}
