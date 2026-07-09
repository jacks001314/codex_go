package bottompane

import (
	"reflect"
	"strings"
	"testing"

	"codex_go/internal/tui"
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
	if height := popup.CalculateRequiredHeight(72); height != MaxPopupRows+2 {
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
	expected := tui.RenderSelectedRow(tui.SelectionPrefix(true) + selected.DisplayName + " - [Skill] Description " + strings.TrimPrefix(selected.DisplayName, "Mention "))
	if !bottomPaneContainsRow(rows, expected) {
		t.Fatalf("rows missing selected:\n%s", strings.Join(rows, "\n"))
	}
	if !bottomPaneContainsRow(rows, SkillPopupHintLine()) {
		t.Fatalf("rows missing hint:\n%s", strings.Join(rows, "\n"))
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
