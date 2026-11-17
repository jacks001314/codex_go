package turn

import (
	"testing"

	"codex_go/model"
)

// TestMultiAgentToolsInCodeModeLikeRust pins Rust #61854: model metadata wins
// when present, and its absence preserves the runtime's non_code_mode_only
// setting across all three tool modes.
func TestMultiAgentToolsInCodeModeLikeRust(t *testing.T) {
	enabled, disabled := true, false
	cases := []struct {
		name            string
		info            *model.ModelInfo
		nonCodeModeOnly bool
		want            bool
	}{
		{name: "metadata true overrides runtime", info: &model.ModelInfo{MultiAgentToolsInCodeMode: &enabled}, nonCodeModeOnly: true, want: true},
		{name: "metadata false stays direct only", info: &model.ModelInfo{MultiAgentToolsInCodeMode: &disabled}, nonCodeModeOnly: false, want: false},
		{name: "absent preserves non_code_mode_only", info: &model.ModelInfo{}, nonCodeModeOnly: true, want: false},
		{name: "absent follows code mode", info: &model.ModelInfo{}, nonCodeModeOnly: false, want: true},
		{name: "missing catalog follows code mode", info: nil, nonCodeModeOnly: false, want: true},
		{name: "missing catalog honors non_code_mode_only", info: nil, nonCodeModeOnly: true, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := MultiAgentToolsInCodeMode(tc.info, tc.nonCodeModeOnly); got != tc.want {
				t.Fatalf("MultiAgentToolsInCodeMode = %v, want %v", got, tc.want)
			}
		})
	}
}
