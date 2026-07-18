package bottompane

import (
	"reflect"
	"strings"
	"testing"

	"codex_go/tui"
)

func TestToggleMultiSelectCompatibility(t *testing.T) {
	items := []MultiSelectItem{{ID: "a"}, {ID: "b", Selected: true}}
	out := ToggleMultiSelect(items, "a")
	if !out[0].Selected || !out[0].Enabled || items[0].Selected {
		t.Fatalf("toggle should update copy and preserve input: out=%#v input=%#v", out, items)
	}
	out = ToggleMultiSelect(out, "b")
	if out[1].Selected || out[1].Enabled {
		t.Fatalf("toggle should turn selected item off: %#v", out[1])
	}
}

func TestMultiSelectPickerSearchToggleConfirmCancelRowsMatchRustCore(t *testing.T) {
	picker := NewMultiSelectPicker("Select Items", "Choose what to enable", []MultiSelectItem{
		{ID: "alpha", Name: "Alpha Item", Description: "First", Selected: true, Orderable: true},
		{ID: "beta", Name: "Beta Item", Description: "Second", Orderable: true},
		{ID: "gamma", Name: "Gamma Item", Description: "Third", Orderable: true},
	})
	if got := picker.SelectedIDs(); !reflect.DeepEqual(got, []string{"alpha"}) {
		t.Fatalf("selected ids = %#v", got)
	}
	for _, key := range []string{"b", "e"} {
		picker.HandleKey(key)
	}
	if picker.SearchQuery != "be" || len(picker.FilteredIndices) != 1 || picker.FilteredIndices[0] != 1 {
		t.Fatalf("search query=%q filtered=%#v", picker.SearchQuery, picker.FilteredIndices)
	}
	rows := picker.Rows(80)
	if !bottomPaneContainsRow(rows, "> be") || !bottomPaneContainsRow(rows, tui.RenderSelectedRow("\u203a [ ] Beta Item  Second")) {
		t.Fatalf("rows missing search/selected beta:\n%s", strings.Join(rows, "\n"))
	}
	picker.HandleKey("space")
	if got := picker.SelectedIDs(); !reflect.DeepEqual(got, []string{"alpha", "beta"}) {
		t.Fatalf("selected ids after toggle = %#v", got)
	}
	ids := picker.Confirm()
	if !picker.Complete || picker.Cancelled || !reflect.DeepEqual(ids, []string{"alpha", "beta"}) {
		t.Fatalf("confirm ids=%#v complete=%v cancelled=%v", ids, picker.Complete, picker.Cancelled)
	}

	cancelled := NewMultiSelectPicker("Select", "", nil)
	cancelled.HandleKey("esc")
	if !cancelled.Complete || !cancelled.Cancelled {
		t.Fatalf("cancelled picker complete=%v cancelled=%v", cancelled.Complete, cancelled.Cancelled)
	}
}

func TestMultiSelectPickerOrderingAndSectionBreakMatchRustCore(t *testing.T) {
	picker := NewMultiSelectPicker("Order", "", []MultiSelectItem{
		{ID: "theme", Name: "Theme Colors", Orderable: false, SectionBreakAfter: true},
		{ID: "model", Name: "Model", Orderable: true},
		{ID: "branch", Name: "Branch", Orderable: true},
	})
	picker.OrderingEnabled = true
	rows := picker.Rows(80)
	if !bottomPaneContainsRow(rows, MultiSelectSectionBreakRow) {
		t.Fatalf("rows missing section break:\n%s", strings.Join(rows, "\n"))
	}
	picker.HandleKey("right")
	if got := multiSelectIDs(picker.Items); !reflect.DeepEqual(got, []string{"theme", "model", "branch"}) {
		t.Fatalf("non-orderable item should not move: %#v", got)
	}
	picker.HandleKey("down")
	picker.HandleKey("right")
	if got := multiSelectIDs(picker.Items); !reflect.DeepEqual(got, []string{"theme", "branch", "model"}) {
		t.Fatalf("model should move down: %#v", got)
	}
	picker.HandleKey("left")
	if got := multiSelectIDs(picker.Items); !reflect.DeepEqual(got, []string{"theme", "model", "branch"}) {
		t.Fatalf("model should move up: %#v", got)
	}
	picker.SearchQuery = "branch"
	picker.ApplyFilter()
	if picker.MoveSelectedUp() {
		t.Fatal("reordering should be disabled while searching")
	}
}

func TestMultiSelectPickerNavigationPreviewNoMatchesAndTruncation(t *testing.T) {
	picker := NewMultiSelectPicker("Select", "", []MultiSelectItem{
		{ID: "a", Name: "A very very very very long item name", Selected: true},
		{ID: "b", Name: "Bravo"},
	})
	picker.MoveDown()
	item, ok := picker.SelectedItem()
	if !ok || item.ID != "b" {
		t.Fatalf("selected after down = %#v ok=%v", item, ok)
	}
	picker.MoveDown()
	item, _ = picker.SelectedItem()
	if item.ID != "a" {
		t.Fatalf("down should wrap to first item, got %#v", item)
	}
	rows := picker.Rows(24)
	if !bottomPaneContainsRow(rows, tui.RenderSelectedRow("\u203a [x] A very very ver\u2026")) {
		t.Fatalf("rows missing selected truncated row:\n%s", strings.Join(rows, "\n"))
	}
	if !bottomPaneContainsRow(rows, "Selected: a") {
		t.Fatalf("rows missing preview:\n%s", strings.Join(rows, "\n"))
	}
	picker.SearchQuery = "zzz"
	picker.ApplyFilter()
	rows = picker.Rows(40)
	if !bottomPaneContainsRow(rows, "no matches") {
		t.Fatalf("rows missing no matches:\n%s", strings.Join(rows, "\n"))
	}
}

func TestMultiSelectPickerSearchAndTruncateMatchRustTextFormatting(t *testing.T) {
	picker := NewMultiSelectPicker("Select", "", []MultiSelectItem{
		{ID: "alpha-id", Name: "", Orderable: true},
		{ID: "beta-id", Name: "Beta Name", Orderable: true},
	})

	picker.SearchQuery = "alpha"
	picker.ApplyFilter()
	if len(picker.FilteredIndices) != 0 {
		t.Fatalf("search should not fall back to id, filtered=%#v", picker.FilteredIndices)
	}

	picker.SearchQuery = "beta"
	picker.ApplyFilter()
	if len(picker.FilteredIndices) != 1 || picker.FilteredIndices[0] != 1 {
		t.Fatalf("search should match name only, filtered=%#v", picker.FilteredIndices)
	}

	if got := truncateMultiSelectText("abcdefghijklmnopqrstuvwxyz", MultiSelectItemNameTruncateLen); got != "abcdefghijklmnopqr..." {
		t.Fatalf("truncate text = %q", got)
	}
}

func multiSelectIDs(items []MultiSelectItem) []string {
	ids := make([]string, len(items))
	for i, item := range items {
		ids[i] = item.ID
	}
	return ids
}
