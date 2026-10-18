package chatwidget

import "testing"

// Rust #46883: the picker marks the stored preference and requires explicit
// confirmation for either renderer.
func TestTUIModePickerLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		fullscreen bool
		current    string
	}{
		{name: "scrollback current", fullscreen: false, current: TUIModeScrollback},
		{name: "fullscreen current", fullscreen: true, current: TUIModeFullscreen},
	} {
		view := NewTUIModePickerView(testCase.fullscreen)
		if view.ViewID != TUIModePickerViewID || view.Title != "TUI mode for next launch" {
			t.Fatalf("%s: view = %#v", testCase.name, view)
		}
		if view.FooterNote != "Restart to apply. Launch overrides still apply." {
			t.Fatalf("%s: footer = %q", testCase.name, view.FooterNote)
		}
		if len(view.Items) != 2 {
			t.Fatalf("%s: items = %#v", testCase.name, view.Items)
		}
		for _, item := range view.Items {
			if !item.RequireExplicitConfirmation || !item.DismissOnSelect {
				t.Fatalf("%s: item %#v lacks confirmation/dismissal", testCase.name, item)
			}
			if item.IsCurrent != (item.ID == testCase.current) {
				t.Fatalf("%s: current marker = %#v", testCase.name, item)
			}
		}
	}
}
