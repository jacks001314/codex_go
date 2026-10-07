package streaming

import (
	"strings"
	"testing"

	"codex_go/utils"
)

// Rust #48623 (upstream 98072cf5f6): "Preserve empty Markdown list markers in
// the TUI". A trailing bare list marker ("-", "+", "*", "8.", "8)") in the
// committed source is kept in the mutable tail so a continuation that arrives in
// a later chunk, and a terminal resize, both re-render the item from the whole
// source instead of emitting a marker the next chunk would change.
//
// Ported Rust tests: `empty_list_item_stays_mutable_until_finalization` and
// `resizing_does_not_drop_held_list_marker` (streaming/rendering_preferences_tests.rs).

func TestBareListMarkerSourceStartLikeRust(t *testing.T) {
	cases := []struct {
		source string
		start  int
		want   bool
	}{
		{"8.", 0, true},
		{"8.\n", 0, true},
		{"8)\n", 0, true},
		{"-\n", 0, true},
		{"+\n", 0, true},
		{"*\n", 0, true},
		{"  *  \n", 0, true},
		{"> 8.\n", 0, true},
		{"> -  \n", 0, true},
		{"prose\n8.\n", 6, true},
		{"> prose\n> 8.\n", 8, true},
		{"8. answer\n", 0, false},
		{"prose\n", 0, false},
		{"-\nprose\n", 0, false},
		{"one\ntwo\n", 4, false},
		{"1.2\n", 0, false},
		{"\n", 0, false},
		{"", 0, false},
	}
	for _, tc := range cases {
		start, ok := bareListMarkerSourceStart(tc.source)
		if ok != tc.want || (ok && start != tc.start) {
			t.Fatalf("bareListMarkerSourceStart(%q) = (%d, %v), want (%d, %v)", tc.source, start, ok, tc.start, tc.want)
		}
	}
}

// TestEmptyListItemStaysMutableUntilFinalizationLikeRust mirrors Rust
// `empty_list_item_stays_mutable_until_finalization`: whatever was committed
// while the marker was pending, the emitted lines plus the finalized remainder
// equal the render of the whole source.
func TestEmptyListItemStaysMutableUntilFinalizationLikeRust(t *testing.T) {
	const width = 24
	for _, tc := range []struct {
		name         string
		marker       string
		continuation string
	}{
		{"bare marker without newline", "8.", ""},
		{"ordered marker line", "8.\n", "   answer\n"},
		{"blockquoted marker line", "> 8.\n", ">    answer\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			controller := NewStreamControllerWithTheme(width, "dark")
			controller.Push(tc.marker)
			emitted := []string{}
			if cell, _ := controller.OnCommitTickBatch(1 << 20); cell != nil {
				emitted = append(emitted, cell.RawLines()...)
			}
			controller.Push(tc.continuation)
			final, source := controller.Finalize()
			if final != nil {
				emitted = append(emitted, final.RawLines()...)
			}
			want := renderSourceLines(source, width, "dark")
			// The emitted cells normalize a whitespace-only line to "", so the
			// comparison normalizes the full render the same way.
			if got, expected := joinStreamLines(emitted), joinStreamLines(want); got != expected {
				t.Fatalf("marker=%q emitted=%q want render of %q = %q", tc.marker, got, source, expected)
			}
		})
	}
}

// TestResizingDoesNotDropHeldListMarkerLikeRust mirrors Rust
// `resizing_does_not_drop_held_list_marker`: the marker line a resize re-renders
// is still emitted as the item's own line.
func TestResizingDoesNotDropHeldListMarkerLikeRust(t *testing.T) {
	controller := NewStreamControllerWithTheme(10, "dark")
	controller.Push("Long prose with enough words to wrap.\n\n")
	if cell, _ := controller.OnCommitTickBatch(1 << 20); cell == nil || len(cell.RawLines()) < 2 {
		t.Fatalf("expected the wrapped prose to be emitted first, got %#v", cell)
	}
	controller.Push("8.\n")
	controller.SetWidth(40)
	final, _ := controller.Finalize()
	if final == nil {
		t.Fatal("expected remaining lines after the resize")
	}
	lines := final.RawLines()
	last := strings.TrimRight(utils.StripANSI(lines[len(lines)-1]), " ")
	// Rust expects the held item's own line "8. "; the Go renderer pads every
	// rendered line to the wrap width, so the padded form is the equivalent.
	if last != "8." || !strings.Contains(lines[len(lines)-1], "8. ") {
		t.Fatalf("last remaining line = %q, want the held marker %q (all=%q)", last, "8. ", lines)
	}
}

// joinStreamLines normalizes whitespace-only lines the way the emitted history
// cells do (AgentMessageCell blanking) so emitted and freshly rendered lines can
// be compared directly.
func joinStreamLines(lines []string) string {
	normalized := make([]string, len(lines))
	for index, line := range lines {
		if strings.TrimSpace(utils.StripANSI(line)) == "" {
			normalized[index] = ""
			continue
		}
		normalized[index] = strings.TrimRight(line, " ")
	}
	return strings.Join(normalized, "\n")
}
