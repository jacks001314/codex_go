package tea

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
	"codex_go/utils"
)

func userPromptMessage(text string) codextui.Message {
	return codextui.Message{Role: codextui.RoleUser, Text: text, RawText: text}
}

func transcriptLineIndex(lines []string, needle string) int {
	for index, line := range lines {
		if strings.Contains(utils.StripANSI(line), needle) {
			return index
		}
	}
	return -1
}

// TestConsecutiveUserPromptsShareBackgroundBlockLikeRust mirrors Rust #56280:
// adjacent visible prompts are separated by a single surface-painted row
// instead of an unpainted gap.
func TestConsecutiveUserPromptsShareBackgroundBlockLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.Messages = []codextui.Message{
		userPromptMessage("first prompt"),
		userPromptMessage("second prompt"),
	}
	lines := strings.Split(renderTranscript(state, false, 40, ""), "\n")
	first := transcriptLineIndex(lines, "first prompt")
	second := transcriptLineIndex(lines, "second prompt")
	if first < 0 || second < 0 {
		t.Fatalf("prompts missing from transcript:\n%s", strings.Join(lines, "\n"))
	}
	if second != first+2 {
		t.Fatalf("prompts at %d/%d, want exactly one shared separator row:\n%s", first, second, strings.Join(lines, "\n"))
	}
	separator := lines[first+1]
	if surface := userPromptSurfaceSGR(); surface == "" || !strings.Contains(separator, surface) {
		t.Fatalf("shared separator is not painted with the prompt surface %q: %q", surface, separator)
	}
	if visible := utils.StripANSI(separator); visible != strings.Repeat(" ", 40) {
		t.Fatalf("shared separator is not a full-width blank row: %q", visible)
	}
}

// TestVisibleAssistantSeparatesUserPromptsLikeRust keeps ordinary prompt
// separation when another visible cell sits between them.
func TestVisibleAssistantSeparatesUserPromptsLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.Messages = []codextui.Message{
		userPromptMessage("first prompt"),
		{Role: codextui.RoleAssistant, Text: "response"},
		userPromptMessage("second prompt"),
	}
	lines := strings.Split(renderTranscript(state, false, 40, ""), "\n")
	first := transcriptLineIndex(lines, "first prompt")
	second := transcriptLineIndex(lines, "second prompt")
	if first < 0 || second < 0 || second <= first+2 {
		t.Fatalf("assistant did not separate the prompts:\n%s", strings.Join(lines, "\n"))
	}
	beforeSecond := lines[second-2]
	if surface := userPromptSurfaceSGR(); surface != "" && strings.Contains(beforeSecond, surface) {
		t.Fatalf("prompt block separator was incorrectly grouped: %q", beforeSecond)
	}
}

// TestDetailedReasoningSeparatesUserPromptsLikeRust mirrors the hidden-cell
// rule: a transcript-only reasoning entry only breaks grouping when detailed
// mode makes it visible.
func TestDetailedReasoningSeparatesUserPromptsLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.Messages = []codextui.Message{
		userPromptMessage("first"),
		{Role: codextui.RoleAssistant, Text: "visible detail", TranscriptOnly: true},
		userPromptMessage("second"),
	}

	compact := strings.Join(strings.Split(renderTranscriptWithHistoryMode(state, false, 40, "", false), "\n"), "\n")
	if !strings.Contains(utils.StripANSI(compact), "first") || !strings.Contains(utils.StripANSI(compact), "second") {
		t.Fatalf("compact transcript lost a prompt:\n%s", compact)
	}
	compactLines := strings.Split(compact, "\n")
	first := transcriptLineIndex(compactLines, "first")
	second := transcriptLineIndex(compactLines, "second")
	if second != first+2 {
		t.Fatalf("hidden reasoning broke prompt grouping:\n%s", compact)
	}

	detailed := strings.Split(renderTranscriptWithHistoryMode(state, false, 40, "", true), "\n")
	first = transcriptLineIndex(detailed, "first")
	second = transcriptLineIndex(detailed, "second")
	if first < 0 || second <= first+2 {
		t.Fatalf("visible reasoning did not separate the prompts:\n%s", strings.Join(detailed, "\n"))
	}
}
