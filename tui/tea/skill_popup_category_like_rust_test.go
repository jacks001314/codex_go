package tea

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
	"codex_go/utils"
)

// TestSkillPopupCategoryTagsSurviveHiddenDescriptionsLikeRust mirrors #46694's
// skill_popup_category_tags snapshots: narrow rows drop the description column
// but keep the category tag at the right edge.
func TestSkillPopupCategoryTagsSurviveHiddenDescriptionsLikeRust(t *testing.T) {
	item := skillPopupItem{
		DisplayName: "Shared",
		Description: "Secondary details",
		CategoryTag: "[Skill]",
	}
	for _, width := range []int{32, 72} {
		line := skillPopupRenderLine(item, false, 10, width)
		plain := utils.StripANSI(line)
		if !strings.Contains(plain, "[Skill]") {
			t.Fatalf("width %d lost category tag: %q", width, plain)
		}
		if contains := strings.Contains(plain, "Secondary details"); contains != (width == 72) {
			t.Fatalf("width %d description visible=%v: %q", width, contains, plain)
		}
		if got := codextui.DisplayWidth(plain); got != width {
			t.Fatalf("width %d rendered line width = %d: %q", width, got, plain)
		}
	}
	selected := utils.StripANSI(skillPopupRenderLine(item, true, 10, 32))
	if !strings.HasPrefix(selected, "\u203a") || !strings.Contains(selected, "[Skill]") {
		t.Fatalf("selected skill row = %q", selected)
	}
	if got := codextui.DisplayWidth(selected); got != 32 {
		t.Fatalf("selected skill row width = %d: %q", got, selected)
	}
}
