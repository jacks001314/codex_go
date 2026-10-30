package state

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestGuardianDeliveredTextPartsStayBoundedLikeRust covers Rust #58071: the
// delivered context is split into UTF-8-safe text parts of at most the shared
// 9,000-token (36KB) limit, and native call evidence uses the same cap.
func TestGuardianDeliveredTextPartsStayBoundedLikeRust(t *testing.T) {
	if GuardianMaxTextPartBytes != GuardianMaxTextPartEstimatedTokens*4 {
		t.Fatalf("GuardianMaxTextPartBytes = %d", GuardianMaxTextPartBytes)
	}
	if GuardianNativeToolCallMaxTokens != GuardianMaxTextPartEstimatedTokens {
		t.Fatalf("native call cap = %d, want the shared text-part cap %d", GuardianNativeToolCallMaxTokens, GuardianMaxTextPartEstimatedTokens)
	}

	// Multibyte text past the budget exercises the rune-boundary split.
	unit := "guardian-context-\u00e9\u4e2d\u6587-"
	huge := strings.Repeat(unit, (GuardianMaxTextPartBytes/len(unit))*2+17)
	transcript := []GuardianTranscriptEntry{
		{Author: "user", Index: 1, Text: huge},
		{Author: "assistant", Index: 2, Text: huge},
	}
	options := BuildPromptOptions{TranscriptFormat: GuardianTranscriptFormatLine}
	parts, costs, err := BuildPromptPartsWithSectionCosts(Action{
		Type: "execve", Source: CommandSourceShell, EnvironmentID: "local",
		Program: "/bin/echo", Argv: []string{"hi"}, CWD: "/repo",
	}, transcript, options)
	if err != nil {
		t.Fatalf("BuildPromptPartsWithSectionCosts() error = %v", err)
	}
	textParts := 0
	var combined strings.Builder
	var transcriptBytes int
	for _, part := range parts {
		if len(part.Native) > 0 {
			continue
		}
		textParts++
		if len(part.Text) > GuardianMaxTextPartBytes {
			t.Fatalf("text part = %d bytes, want <= %d", len(part.Text), GuardianMaxTextPartBytes)
		}
		if !utf8.ValidString(part.Text) {
			t.Fatal("text part is not valid UTF-8")
		}
		combined.WriteString(part.Text)
	}
	if textParts < 4 {
		t.Fatalf("text parts = %d, want the oversized context split", textParts)
	}
	rendered := combined.String()
	if !strings.Contains(rendered, "Recent transcript:") {
		t.Fatalf("rendered context lost its transcript header")
	}
	// Splitting must not change the source text.
	if got := strings.Count(rendered, unit[:len(unit)-2]); got != strings.Count(huge, unit[:len(unit)-2])*2 {
		t.Fatalf("rendered occurrences = %d", got)
	}
	for _, cost := range costs {
		if cost.Section == "conversation_transcript" {
			transcriptBytes = cost.TextBytes
		}
	}
	if transcriptBytes == 0 {
		t.Fatalf("costs = %#v, want a transcript section", costs)
	}
}
