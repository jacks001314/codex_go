package app

import "testing"

// TestInteractiveRightClickPasteHonorsConfig pins Rust's `tui.right_click_paste`
// config surface (#48118): the default is `auto`, the three lowercase modes are
// recognized, and a malformed value falls back to the default because Go's
// `[tui]` sub-table is not value-validated.
func TestInteractiveRightClickPasteHonorsConfig(t *testing.T) {
	if got := interactiveRightClickPaste(nil); got == nil || *got != "auto" {
		t.Fatalf("default right_click_paste = %v, want auto", got)
	}
	cases := map[string]string{
		"auto":  "auto",
		"on":    "on",
		"off":   "off",
		" OFF ": "off",
		"bogus": "auto",
	}
	for raw, want := range cases {
		values := map[string]any{"tui": map[string]any{"right_click_paste": raw}}
		got := interactiveRightClickPaste(values)
		if got == nil || *got != want {
			t.Fatalf("right_click_paste=%q resolved to %v, want %q", raw, got, want)
		}
	}
	if got := interactiveRightClickPaste(map[string]any{"tui": map[string]any{"right_click_paste": 7}}); got == nil || *got != "auto" {
		t.Fatalf("non-string right_click_paste = %v, want auto", got)
	}
	if got := interactiveRightClickPasteValue(nil); got != "auto" {
		t.Fatalf("unset option value = %q, want auto", got)
	}
	on := "on"
	if got := interactiveRightClickPasteValue(&on); got != "on" {
		t.Fatalf("resolved option value = %q, want on", got)
	}
}
