package markdown

import (
	"strings"
	"testing"

	"codex_go/utils"
)

// TestRenderingListsPreferenceLikeRust covers #47322's `tui.rendering.lists`
// switch: enabled uses Unicode bullets/checkboxes, disabled preserves textual
// task markers and hyphen bullets while ordered numbering stays intact.
func TestRenderingListsPreferenceLikeRust(t *testing.T) {
	previous := CurrentRendering()
	defer InitRendering(previous)

	source := strings.Join([]string{
		"- first",
		"  - nested",
		"1. [x] ordered done",
		"- [x] done",
		"- [ ] todo",
		"> - [x] quoted",
		"",
		"```text",
		"- [x] literal code",
		"```",
		"",
	}, "\n")

	InitRendering(DefaultRendering())
	enabled, err := Render(source, 60)
	if err != nil {
		t.Fatal(err)
	}
	plainEnabled := utils.StripANSI(enabled)
	for _, want := range []string{"• first", "• nested", "1. ☑ ordered done", "☑ done", "☐ todo", "☑ quoted"} {
		if !strings.Contains(plainEnabled, want) {
			t.Fatalf("enabled lists missing %q:\n%s", want, plainEnabled)
		}
	}
	disabledRendering := DefaultRendering()
	disabledRendering.Lists = false
	InitRendering(disabledRendering)
	disabled, err := Render(source, 60)
	if err != nil {
		t.Fatal(err)
	}
	plainDisabled := utils.StripANSI(disabled)
	for _, want := range []string{"- first", "- nested", "1. [x] ordered done", "- [x] done", "- [ ] todo", "- [x] quoted"} {
		if !strings.Contains(plainDisabled, want) {
			t.Fatalf("disabled lists missing %q:\n%s", want, plainDisabled)
		}
	}
	if strings.Contains(plainDisabled, "• ") || strings.Contains(plainDisabled, "☑") || strings.Contains(plainDisabled, "☐") {
		t.Fatalf("disabled lists retained Unicode markers:\n%s", plainDisabled)
	}
	if !strings.Contains(plainDisabled, "- [x] literal code") {
		t.Fatalf("disabled rendering changed literal code:\n%s", plainDisabled)
	}
}
