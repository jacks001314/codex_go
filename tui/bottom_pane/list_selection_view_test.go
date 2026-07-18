package bottompane

import (
	"strings"
	"testing"

	"codex_go/tui"
)

func TestGenericDisplayRowsColumnModesSelectedAndDisabled(t *testing.T) {
	rows := []GenericDisplayRow{
		{Name: "gpt-5", Description: "Balanced reasoning", CategoryTag: "current"},
		{Name: "gpt-5-mini", Description: "Faster", DisabledReason: "not available", IsDisabled: true},
	}
	state := ScrollState{SelectedIdx: 0, HasSelection: true}
	rendered := RenderGenericRows(rows, state, 8, "no matches", 80, ColumnWidthConfig{})
	if len(rendered) != 2 {
		t.Fatalf("rendered rows = %#v", rendered)
	}
	if !strings.Contains(rendered[0], tui.RenderSelectedRow("gpt-5")) && !strings.Contains(rendered[0], "\x1b[") {
		t.Fatalf("selected row missing color style: %#v", rendered[0])
	}
	if !strings.Contains(rendered[0], "Balanced reasoning") || !strings.Contains(rendered[0], "current") {
		t.Fatalf("selected row missing description/tag: %#v", rendered[0])
	}
	if !strings.Contains(rendered[1], "gpt-5-mini (disabled)") || !strings.Contains(rendered[1], "disabled: not available") {
		t.Fatalf("disabled row missing reason: %#v", rendered[1])
	}

	fixed := RenderGenericRowsSingleLine(rows, state, 8, "no matches", 20, NewColumnWidthConfig(ColumnWidthFixed, nil))
	if len(fixed) != 2 || tui.DisplayWidth(stripANSIForSelectionTest(fixed[0])) > 20 {
		t.Fatalf("fixed single-line rows = %#v", fixed)
	}
}

func TestListSelectionInitialSelectionNavigationAndDisabledMatchRust(t *testing.T) {
	view := NewListSelectionView("Select model", []ListSelectionItem{
		{ID: "legacy", Label: "legacy", DisabledReason: "unsupported"},
		{ID: "mini", Label: "gpt-5-mini", Description: "Fast", Default: true},
		{ID: "pro", Label: "gpt-5", Description: "Balanced", Current: true},
	})
	item, ok := view.SelectedItem()
	if !ok || item.ID != "pro" {
		t.Fatalf("initial selected = %#v ok=%v", item, ok)
	}
	view.MoveDown()
	item, _ = view.SelectedItem()
	if item.ID != "mini" {
		t.Fatalf("down should wrap to first enabled item, got %#v", item)
	}
	view.MoveUp()
	item, _ = view.SelectedItem()
	if item.ID != "pro" {
		t.Fatalf("up should skip disabled row, got %#v", item)
	}
	view.State.SelectedIdx = 0
	view.State.HasSelection = true
	if _, ok := view.AcceptSelected(); ok {
		t.Fatal("disabled row should not be accepted")
	}
}

func TestListSelectionSearchableInitialSelectionIgnoresCurrentDefaultMatchRust(t *testing.T) {
	view := NewListSelectionView("Search", []ListSelectionItem{
		{ID: "first", Label: "First", SearchValue: "first"},
		{ID: "default", Label: "Default", Default: true, SearchValue: "default"},
		{ID: "current", Label: "Current", Current: true, SearchValue: "current"},
	})
	view.Searchable = true
	view.ApplyFilter()

	item, ok := view.SelectedItem()
	if !ok || item.ID != "first" {
		t.Fatalf("searchable initial selected = %#v ok=%v", item, ok)
	}
}

func TestListSelectionSearchToggleAcceptCancelAndRowsMatchRustCore(t *testing.T) {
	view := NewListSelectionView("Tools", []ListSelectionItem{
		{ID: "fast", Label: "Fast mode", Description: "Use faster inference", SearchValue: "fast speed", Toggle: &ListSelectionToggle{On: false}},
		{ID: "safe", Label: "Safe mode", Description: "Ask before risky commands", SearchValue: "safe approval", Toggle: &ListSelectionToggle{On: true}, SelectedDescription: "Currently safer"},
		{ID: "blocked", Label: "Blocked mode", Description: "Unavailable", Disabled: true, SearchValue: "blocked"},
	})
	view.Searchable = true
	view.SearchPlaceholder = "Search tools"
	view.ApplyFilter()

	for _, key := range []string{"a", "p", "p"} {
		view.HandleKey(key)
	}
	if len(view.FilteredIndices) != 1 || view.FilteredIndices[0] != 1 {
		t.Fatalf("filtered indices = %#v", view.FilteredIndices)
	}
	rows := view.Rows(80)
	if !bottomPaneContainsRow(rows, "> app") {
		t.Fatalf("rows missing search query:\n%s", strings.Join(rows, "\n"))
	}
	if !bottomPaneContainsRow(rows, tui.RenderSelectedRow("1. [*] Safe mode  Currently safer")) {
		t.Fatalf("rows missing selected safe mode:\n%s", strings.Join(rows, "\n"))
	}
	view.HandleKey("space")
	if view.Items[1].Toggle == nil || view.Items[1].Toggle.On {
		t.Fatalf("toggle did not flip off: %#v", view.Items[1].Toggle)
	}
	result, ok := view.HandleKey("enter")
	if !ok || !result.Accepted || result.Item.ID != "safe" || result.ActualIndex != 1 {
		t.Fatalf("accept result = %#v ok=%v", result, ok)
	}
	cancel, ok := view.HandleKey("esc")
	if !ok || !cancel.Cancelled {
		t.Fatalf("cancel result = %#v ok=%v", cancel, ok)
	}
}

func TestListSelectionSearchUsesExplicitSearchValueOnlyMatchRust(t *testing.T) {
	view := NewListSelectionView("Search", []ListSelectionItem{
		{ID: "alpha", Label: "Alpha", Description: "needle appears only in fallback fields"},
		{ID: "beta", Label: "Beta", SearchValue: "beta"},
		{ID: "spaced", Label: "Spaced", SearchValue: " beta"},
	})
	view.Searchable = true

	view.SearchQuery = "needle"
	view.ApplyFilter()
	if len(view.FilteredIndices) != 0 {
		t.Fatalf("fallback fields should not match search: %#v", view.FilteredIndices)
	}

	view.SearchQuery = " beta"
	view.ApplyFilter()
	if len(view.FilteredIndices) != 1 || view.FilteredIndices[0] != 2 {
		t.Fatalf("search query should preserve leading space, indices = %#v", view.FilteredIndices)
	}
}

func TestListSelectionRowsWrapAndNoMatches(t *testing.T) {
	view := NewListSelectionView("Debug", []ListSelectionItem{
		{ID: "long", Label: "Very long option label", Description: "Very long description that should wrap under the description column"},
	})
	rows := view.Rows(48)
	rendered := strings.Join(rows, "\n")
	if !strings.Contains(rendered, "Very long option") || !strings.Contains(rendered, "description") {
		t.Fatalf("wrapped rows missing content:\n%s", rendered)
	}

	view.Searchable = true
	view.SearchQuery = "missing"
	view.ApplyFilter()
	rows = view.Rows(32)
	if !bottomPaneContainsRow(rows, "no matches") {
		t.Fatalf("no match rows = %#v", rows)
	}
}

func stripANSIForSelectionTest(value string) string {
	replacer := strings.NewReplacer(
		"\x1b[1;94m", "",
		"\x1b[94;1m", "",
		"\x1b[0m", "",
		"\x1b[1m", "",
		"\x1b[94m", "",
	)
	return replacer.Replace(value)
}
