package bottompane

import (
	"reflect"
	"strings"
	"testing"

	"codex_go/tui"
	"codex_go/utils"
)

func TestGenericDisplayLineRustNameTruncation(t *testing.T) {
	got := BuildGenericDisplayLine(GenericDisplayRow{
		Name:         "abcdef",
		MatchIndices: []int{0, 1},
		Description:  "desc",
	}, 5)
	want := "ab\u2026  desc"
	if got != want {
		t.Fatalf("BuildGenericDisplayLine = %q, want %q", got, want)
	}

	got = BuildGenericDisplayLine(GenericDisplayRow{
		Name:        "abcdef",
		CategoryTag: "tag",
	}, 3)
	want = "abcdef  tag"
	if got != want {
		t.Fatalf("name without description should not truncate: %q", got)
	}
}

// Rust #46680: the keymap picker opts into a selected row that fills the popup
// width while ordinary pickers keep their compact selection.
func TestGenericRowsFullWidthSelectionLikeRust(t *testing.T) {
	rows := []GenericDisplayRow{{Name: "alpha"}}
	state := ScrollState{SelectedIdx: 0, HasSelection: true}
	full := RenderGenericRows(rows, state, 1, "", 40, ColumnWidthConfig{}.WithFullWidthSelection())
	if len(full) != 1 || tui.DisplayWidth(utils.StripANSI(full[0])) != 40 {
		t.Fatalf("full-width selection = %#v", full)
	}
	compact := RenderGenericRows(rows, state, 1, "", 40, ColumnWidthConfig{})
	if len(compact) != 1 || tui.DisplayWidth(utils.StripANSI(compact[0])) >= 40 {
		t.Fatalf("compact selection = %#v", compact)
	}
}

func TestGenericDisplayLineDisabledOnlyDescriptionMatchesRust(t *testing.T) {
	rows := []GenericDisplayRow{{
		Name:           "legacy",
		DisabledReason: "unsupported",
		IsDisabled:     true,
	}}
	rendered := RenderGenericRows(rows, ScrollState{}, 1, "no matches", 80, ColumnWidthConfig{})
	want := "legacy (disabled)  disabled: unsupported"
	if len(rendered) != 1 || rendered[0] != want {
		t.Fatalf("disabled-only row = %#v, want %#v", rendered, []string{want})
	}
}

func TestGenericDescColIgnoresDisplayShortcut(t *testing.T) {
	rows := []GenericDisplayRow{{
		Name:            "aa",
		DisplayShortcut: "ctrl+shift+very-long",
		Description:     "desc",
	}}
	if got := computeGenericDescCol(rows, 0, 1, 80, ColumnWidthConfig{}); got != 4 {
		t.Fatalf("desc col = %d, want 4", got)
	}
}

func TestGenericRowsTwoColumnWrapMatchesRustShape(t *testing.T) {
	indent := 2
	row := GenericDisplayRow{
		Name:        "alpha beta gamma",
		Description: "one two three four",
		WrapIndent:  &indent,
	}
	got := wrapSelectionRowLines(row, 8, 16)
	want := []string{
		"alpha   one two",
		"  beta  three",
		"  gamm  four",
		"  a",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("two-column wrap = %#v, want %#v", got, want)
	}
	for _, line := range got {
		if width := tui.DisplayWidth(line); width > 16 {
			t.Fatalf("wrapped line %q exceeds width: %d", line, width)
		}
	}
}

func TestMeasureGenericRowsHeightEmptyPlaceholderMatchesRust(t *testing.T) {
	if got := MeasureGenericRowsHeight(nil, ScrollState{}, 8, 80, ColumnWidthConfig{}); got != 1 {
		t.Fatalf("empty measured height = %d, want 1", got)
	}
}

// Mirrors Rust's narrow_description_columns_hide_without_stacking (#46691): a
// picker with HideWhenNarrow drops the description column instead of stacking it
// below the name, so every row still occupies one line.
func TestGenericRowsHideNarrowDescriptionColumnsWithoutStacking(t *testing.T) {
	layout := NewHideWhenNarrowDescriptionLayout(24)
	rows := []GenericDisplayRow{
		{NamePrefix: "› 1. ", Name: "Replace binding", Description: "Capture one key and replace `ctrl-t`."},
		{NamePrefix: "  –  ", Name: "Remove custom binding", DisabledReason: "No custom root override to remove.", IsDisabled: true},
	}
	config := NewColumnWidthConfig(ColumnWidthAutoAllRows, nil).WithDescriptionLayout(layout)

	for _, width := range []int{48, 49} {
		if got := MeasureGenericRowsHeight(rows, ScrollState{}, 2, width, config); got != 2 {
			t.Fatalf("measured height at %d = %d, want 2", width, got)
		}
		rendered := RenderGenericRowsWithDescriptionLayout(rows, ScrollState{}, 2, "", width, config, layout)
		if len(rendered) != 2 {
			t.Fatalf("narrow rows at %d = %#v", width, rendered)
		}
		for _, line := range rendered {
			if strings.Contains(line, "Capture one key") || strings.Contains(line, "No custom root override") {
				t.Fatalf("description column not hidden at width %d: %#v", width, rendered)
			}
		}
		if !strings.Contains(rendered[0], "Replace binding") || !strings.Contains(rendered[1], "Remove custom binding") {
			t.Fatalf("names missing at width %d: %#v", width, rendered)
		}
	}

	// The description column reappears once the remaining width clears the
	// minimum, with the disabled reason rendered alone (Rust's combined_description).
	wide := RenderGenericRowsWithDescriptionLayout(rows, ScrollState{}, 2, "", 80, config, layout)
	if len(wide) != 2 || !strings.Contains(wide[0], "Replace binding") || !strings.Contains(wide[0], "Capture one key") {
		t.Fatalf("wide responsive rows = %#v", wide)
	}
	if strings.HasPrefix(wide[1], "  2.") || !strings.HasPrefix(wide[1], "  –  ") {
		t.Fatalf("disabled gutter marker missing from wide rows = %#v", wide)
	}
	if !strings.Contains(wide[1], "No custom root override to remove.") || strings.Contains(wide[1], "disabled: ") {
		t.Fatalf("wide disabled reason = %#v", wide[1])
	}
	for _, line := range wide {
		if got := tui.DisplayWidth(line); got > 80 {
			t.Fatalf("responsive line %q width=%d exceeds 80", line, got)
		}
	}
}

func containsStringSelectionTest(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// TestGenericRowsLinkURLsLikeRust mirrors Rust #51472: web URLs in selection
// labels and descriptions render as terminal hyperlinks with their complete
// destination.
func TestGenericRowsLinkURLsLikeRust(t *testing.T) {
	url := "https://example.com/docs/page?section=2"
	rows := []GenericDisplayRow{{Name: "open docs", Description: "see " + url}}
	rendered := RenderGenericRows(rows, ScrollState{}, 10, "", 80, NewColumnWidthConfig(ColumnWidthAutoAllRows, nil))
	joined := strings.Join(rendered, "\n")
	if !strings.Contains(joined, tui.OSC8Hyperlink(url, url)) {
		t.Fatalf("selection row missing complete hyperlink:\n%s", joined)
	}
	if !strings.Contains(tui.StripOSC8(joined), url) {
		t.Fatalf("visible text lost the URL:\n%s", joined)
	}
}

func TestGenericRowsKeepURLDestinationsWhenWrappedOrTruncatedLikeRust(t *testing.T) {
	const url = "https://example.com/releases/review/complete-destination"
	rows := []GenericDisplayRow{
		{Name: "read " + url},
		{Name: "open docs", Description: "read " + url},
		{NamePrefix: "1. ", Name: "read " + url},
	}
	for _, row := range rows {
		wrapped := RenderGenericRows([]GenericDisplayRow{row}, ScrollState{}, 1, "", 40, NewColumnWidthConfig(ColumnWidthAutoAllRows, nil))
		if joined := strings.Join(wrapped, "\n"); !strings.Contains(joined, tui.OSC8Hyperlink(url, url)) {
			t.Errorf("wrapped row %q missing complete hyperlink:\n%s", row.Name, joined)
		}

		single := RenderGenericRowsSingleLine([]GenericDisplayRow{row}, ScrollState{}, 1, "", 40, NewColumnWidthConfig(ColumnWidthAutoAllRows, nil))
		if len(single) != 1 {
			t.Fatalf("single-line row %q rendered %d lines", row.Name, len(single))
		}
		line := single[0]
		if !strings.Contains(line, "\x1b]8;;"+url+"\x07") {
			t.Errorf("truncated row %q missing full hyperlink destination: %q", row.Name, line)
		}
		if strings.Contains(line, "…\x1b]8;;\x07") {
			t.Errorf("truncated row %q linked the ellipsis: %q", row.Name, line)
		}
		if !strings.Contains(line, "\x1b]8;;\x07…") {
			t.Errorf("truncated row %q did not leave the ellipsis outside the link: %q", row.Name, line)
		}
		if visible := tui.StripOSC8(line); !strings.HasSuffix(visible, "…") || tui.DisplayWidth(visible) > 40 {
			t.Errorf("truncated row %q visible text = %q width=%d", row.Name, visible, tui.DisplayWidth(visible))
		}
	}
}
