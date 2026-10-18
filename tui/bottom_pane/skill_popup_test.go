package bottompane

import (
	"reflect"
	"strings"
	"testing"

	"codex_go/tui"
)

func TestSkillPopupFilteringSortingAndSelectionMatchRust(t *testing.T) {
	popup := NewSkillPopup([]MentionItem{
		mention("GitHub", []string{"github", "pull requests", "pr"}, "[Plugin]", 0),
		mention("pr-review-triage", []string{"pr-review-triage"}, "[Skill]", 1),
		mention("prd", []string{"prd"}, "[Skill]", 1),
		mention("Plugin Creator", []string{"plugin-creator", "Plugin Creator"}, "[Skill]", 1),
		mention("Logging Best Practices", []string{"logging-best-practices", "Logging Best Practices"}, "[Skill]", 1),
		mention("PR Babysitter", []string{"babysit-pr", "PR Babysitter"}, "[Skill]", 1),
	})
	popup.SetQuery("pr")
	names := []string{}
	for _, idx := range popup.filteredItems() {
		names = append(names, popup.Mentions[idx].DisplayName)
	}
	want := []string{"PR Babysitter", "pr-review-triage", "prd", "Plugin Creator", "Logging Best Practices", "GitHub"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("names = %#v, want %#v", names, want)
	}
	selected, ok := popup.SelectedMention()
	if !ok || selected.DisplayName != "PR Babysitter" {
		t.Fatalf("selected = %#v ok=%v", selected, ok)
	}
}

func TestSkillPopupRowsScrollAndHeightMatchRust(t *testing.T) {
	items := []MentionItem{}
	for idx := 0; idx < MaxPopupRows+2; idx++ {
		items = append(items, MentionItem{
			DisplayName: "Mention " + formatInt(idx),
			Description: stringPtr("Description " + formatInt(idx)),
			InsertText:  "$mention-" + formatInt(idx),
			SearchTerms: []string{"mention-" + formatInt(idx)},
			CategoryTag: stringPtr("[Skill]"),
			SortRank:    1,
		})
	}
	popup := NewSkillPopup(items)
	if height := popup.CalculateRequiredHeight(72); height != MaxPopupRows+3 {
		t.Fatalf("height = %d", height)
	}
	for i := 0; i <= MaxPopupRows; i++ {
		popup.MoveDown()
	}
	rows := popup.Rows(72)
	selected, ok := popup.SelectedMention()
	if !ok {
		t.Fatalf("expected selected mention")
	}
	selectedRow := ""
	for _, row := range rows {
		if strings.Contains(stripANSIForSelectionTest(row), selected.DisplayName) && strings.Contains(row, "›") {
			selectedRow = row
			break
		}
	}
	if selectedRow == "" {
		t.Fatalf("rows missing selected %q:\n%s", selected.DisplayName, strings.Join(rows, "\n"))
	}
	plainSelected := stripANSIForSelectionTest(selectedRow)
	if !strings.Contains(plainSelected, "Description") || !strings.Contains(plainSelected, "[Skill]") {
		t.Fatalf("selected row content = %q", plainSelected)
	}
	if width := tui.DisplayWidth(plainSelected); width != 72 {
		t.Fatalf("selected row width = %d, want 72: %q", width, plainSelected)
	}
	if !bottomPaneContainsRow(rows, SkillPopupHintLine()) {
		t.Fatalf("rows missing hint:\n%s", strings.Join(rows, "\n"))
	}
}

func TestSkillPopupCategoryTagsSurviveHiddenDescriptionsLikeRust(t *testing.T) {
	popup := NewSkillPopup([]MentionItem{
		{
			DisplayName: "Shared",
			Description: stringPtr("Secondary details"),
			InsertText:  "@App",
			SearchTerms: []string{"Shared"},
			CategoryTag: stringPtr("[App]"),
			SortRank:    0,
		},
		{
			DisplayName: "Shared",
			Description: stringPtr("Secondary details"),
			InsertText:  "$Skill",
			SearchTerms: []string{"Shared"},
			CategoryTag: stringPtr("[Skill]"),
			SortRank:    1,
		},
	})
	popup.SetQuery("Shared")
	for _, width := range []int{32, 72} {
		joined := stripANSIForSelectionTest(strings.Join(popup.Rows(width), "\n"))
		if !strings.Contains(joined, "[App]") || !strings.Contains(joined, "[Skill]") {
			t.Fatalf("width %d lost category tags:\n%s", width, joined)
		}
		if contains := strings.Contains(joined, "Secondary details"); contains != (width == 72) {
			t.Fatalf("width %d description visible=%v:\n%s", width, contains, joined)
		}
		for _, row := range popup.Rows(width) {
			if lineWidth := tui.DisplayWidth(stripANSIForSelectionTest(row)); lineWidth > width {
				t.Fatalf("width %d row exceeds width: %q width=%d", width, row, lineWidth)
			}
		}
	}
}

func mention(display string, terms []string, tag string, rank int) MentionItem {
	return MentionItem{
		DisplayName: display,
		SearchTerms: terms,
		InsertText:  "$" + display,
		CategoryTag: &tag,
		SortRank:    rank,
	}
}
