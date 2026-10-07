package tea

import (
	"testing"

	codextui "codex_go/tui"
)

// Rust #51235 popups_and_settings.rs `model_selection_popup_snapshot`: the model
// picker marks the current model and no longer appends a default marker, even
// though the catalog default is still tracked for preselection.
func TestModelPickerLabelHasNoDefaultMarkerLikeRust(t *testing.T) {
	cases := []struct {
		option codextui.ModelPickerOption
		want   string
	}{
		{codextui.ModelPickerOption{ID: "gpt-a", Label: "GPT A", IsCurrent: true, IsDefault: true}, "GPT A (current)"},
		{codextui.ModelPickerOption{ID: "gpt-b", Label: "GPT B", IsDefault: true}, "GPT B"},
		{codextui.ModelPickerOption{ID: "gpt-c", IsCurrent: true}, "gpt-c (current)"},
	}
	for _, tc := range cases {
		if got := modelPickerLabel(tc.option); got != tc.want {
			t.Fatalf("label for %#v = %q, want %q", tc.option, got, tc.want)
		}
	}
}
