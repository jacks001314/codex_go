package tea

import (
	"fmt"
	"strings"
	"testing"

	codextui "codex_go/tui"
	"codex_go/utils"
)

// TestSkillPopupRendersPickerFrameLikeRust pins the frame Rust draws around the
// `$` popup (bottom_pane/skill_popup.rs + bottom_pane/picker_rows.rs): a blank
// overflow row on each scrolled-away side (marked with an arrow) and the
// "enter insert · esc close" hint underneath.
func TestSkillPopupRendersPickerFrameLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 120, Height: 40})
	model.skillPopup.Active = true
	for i := 0; i < 12; i++ {
		model.skillPopup.Items = append(model.skillPopup.Items, skillPopupItem{
			DisplayName: fmt.Sprintf("Skill %02d", i),
			Description: fmt.Sprintf("Description %02d", i),
			CategoryTag: "[Skill]",
		})
	}
	lines := strings.Split(utils.StripANSI(model.renderSkillPopup()), "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " ")
	}
	if got, want := len(lines), model.skillPopupRows(); got != want {
		t.Fatalf("rendered rows = %d, skillPopupRows() = %d", got, want)
	}
	if last := lines[len(lines)-1]; last != "  enter insert · esc close" {
		t.Fatalf("hint row = %q", last)
	}
	if below := lines[len(lines)-2]; below != "↓" {
		t.Fatalf("overflow row = %q, want ↓", below)
	}
	if strings.Contains(strings.Join(lines, "\n"), "↑") {
		t.Fatalf("popup shows an up arrow while at the top:\n%s", strings.Join(lines, "\n"))
	}

	model.skillPopup.Selected = len(model.skillPopup.Items) - 1
	lines = strings.Split(utils.StripANSI(model.renderSkillPopup()), "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " ")
	}
	if first := lines[0]; first != "↑" {
		t.Fatalf("scrolled popup top row = %q, want ↑", first)
	}
	if strings.Contains(strings.Join(lines, "\n"), "↓") {
		t.Fatalf("popup shows a down arrow at the end of the list:\n%s", strings.Join(lines, "\n"))
	}
}

// TestSkillPopupCategoryTagsSurviveHiddenDescriptionsLikeRust mirrors #46694's
// skill_popup_category_tags snapshots: narrow rows drop the description column
// but keep the category tag in the description column.
func TestSkillPopupCategoryTagsSurviveHiddenDescriptionsLikeRust(t *testing.T) {
	item := skillPopupItem{
		DisplayName: "Shared",
		Description: "Secondary details",
		CategoryTag: "[Skill]",
	}
	nameWidth := skillPopupNameWidth([]skillPopupItem{item}, 72)
	for _, tc := range []struct {
		width int
		want  string
	}{
		{32, "  Shared  [Skill]"},
		{72, "  Shared  [Skill] Secondary details"},
	} {
		plain := utils.StripANSI(skillPopupRenderLine(item, false, nameWidth, tc.width))
		if plain != tc.want {
			t.Fatalf("width %d line = %q, want %q", tc.width, plain, tc.want)
		}
	}
	selected := utils.StripANSI(skillPopupRenderLine(item, true, nameWidth, 32))
	if !strings.HasPrefix(selected, "\u203a Shared  [Skill]") {
		t.Fatalf("selected skill row = %q", selected)
	}
	if got := codextui.DisplayWidth(selected); got != 32 {
		t.Fatalf("selected skill row width = %d: %q", got, selected)
	}
}
