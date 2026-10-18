package tea

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
	"codex_go/utils"
)

func trimmedDisplayText(lines []string) string {
	out := make([]string, len(lines))
	for index, line := range lines {
		out[index] = strings.TrimRight(utils.StripANSI(line), " ")
	}
	return strings.Join(out, "\n")
}

// Rust #46899: completed assistant lists use uniform sibling spacing, while a
// wide render with only single-line items stays compact. Re-rendering at a new
// width recomputes the placement.
func TestCompletedAssistantListsUseUniformSpacingLikeRust(t *testing.T) {
	message := codextui.Message{
		Role: codextui.RoleAssistant,
		Text: "1. One\n2. This item has enough words to wrap\n3. Three",
	}
	narrow := trimmedDisplayText(richMessageDisplayLines(message, 34, "", ""))
	if !strings.Contains(narrow, "\u2022 1. One\n\n  2. This item has") {
		t.Fatalf("narrow completed list missing uniform separator:\n%s", narrow)
	}
	if !strings.Contains(narrow, "wrap\n\n  3. Three") {
		t.Fatalf("narrow completed list missing separator after wrapped item:\n%s", narrow)
	}

	wide := trimmedDisplayText(richMessageDisplayLines(message, 100, "", ""))
	if !strings.Contains(wide, "\u2022 1. One\n  2. This item has enough words to wrap\n  3. Three") {
		t.Fatalf("wide single-line list did not stay compact:\n%s", wide)
	}
}
