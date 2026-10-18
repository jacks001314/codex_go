package bottompane

import (
	"strings"
	"testing"

	"codex_go/tui"
)

// TestPickerRowsUseSharedColumnsAndSelection mirrors #46692's dedicated-picker
// adapter: measurement includes the two spacer rows, single-line rows use the
// shared hide-when-narrow columns, and selection fills the popup width.
func TestPickerRowsUseSharedColumnsAndSelection(t *testing.T) {
	rows := []GenericDisplayRow{{
		NamePrefix:  pickerRowSelectionPrefix(true),
		Name:        "Alpha",
		Description: "A description that stays in its column",
	}}
	state := ScrollState{HasSelection: true, SelectedIdx: 0}
	lines := RenderPickerRowsSingleLine(rows, state, 8, "no matches", 48)
	if len(lines) != 1 {
		t.Fatalf("single-line rows = %#v", lines)
	}
	plain := stripANSIForSelectionTest(lines[0])
	if width := tui.DisplayWidth(plain); width != 48 {
		t.Fatalf("selected row width = %d, want 48: %q", width, plain)
	}
	if !strings.HasPrefix(plain, selectedRowMarker) {
		t.Fatalf("selected row marker = %q", plain)
	}
	got := MeasurePickerRowsHeight(rows, state, 8, 48)
	want := MeasureGenericRowsHeight(rows, state, 8, 48, DefaultPickerRowColumns()) + 2
	if got != want {
		t.Fatalf("measure height = %d, want %d", got, want)
	}
}
