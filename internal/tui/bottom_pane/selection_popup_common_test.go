package bottompane

import (
	"reflect"
	"testing"

	"codex_go/internal/tui"
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
