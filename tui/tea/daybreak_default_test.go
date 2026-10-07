package tea

import (
	"testing"

	codextui "codex_go/tui"
	bottompane "codex_go/tui/bottom_pane"
)

// TestNewThreadDaybreakDefaultLikeRust covers Rust #49861's new-thread path:
// a fresh session starts from the configured Daybreak preference
// (`params.daybreak_enabled = config.daybreak_enabled && !ephemeral`) while the
// live value stays gated by the cli_daybreak feature.
func TestNewThreadDaybreakDefaultLikeRust(t *testing.T) {
	cases := []struct {
		name     string
		features map[string]bool
		def      bool
		want     bool
	}{
		{name: "configured default with feature on", features: map[string]bool{"cli_daybreak": true}, def: true, want: true},
		{name: "configured default with feature off", features: map[string]bool{"cli_daybreak": false}, def: true, want: false},
		{name: "no configured default", features: map[string]bool{"cli_daybreak": true}, def: false, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			model := NewModel(codextui.NewState(nil), Options{
				Width:           120,
				Height:          40,
				FeatureSettings: tc.features,
				DaybreakDefault: tc.def,
			})
			if model.daybreakEnabled != tc.want {
				t.Fatalf("daybreakEnabled = %v, want %v", model.daybreakEnabled, tc.want)
			}
			model.ensureStatusControls()
			model.syncStatusControlsRuntime()
			value, ok := model.statusControls.StatusLineValueForItem(bottompane.StatusLineDaybreak)
			want := "Daybreak off"
			if tc.want {
				want = "Daybreak on"
			}
			if !ok || value != want {
				t.Fatalf("status line daybreak = %q ok=%v, want %q", value, ok, want)
			}
		})
	}
}
