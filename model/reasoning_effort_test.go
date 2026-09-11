package model

import "testing"

func TestResolveReasoningEffortLikeRust(t *testing.T) {
	effortPtr := func(value string) *string { return &value }

	cases := []struct {
		name   string
		info   *ModelInfo
		effort string
		want   string
	}{
		{
			name: "multi agent override wins for ultra",
			info: &ModelInfo{
				MultiAgentReasoningEffort: effortPtr("high"),
				SupportedReasoningLevels:  []string{"low", "medium", "high", "ultra"},
			},
			effort: "ultra",
			want:   "high",
		},
		{
			name: "ultra ignores override not in supported levels",
			info: &ModelInfo{
				MultiAgentReasoningEffort: effortPtr("xhigh"),
				SupportedReasoningLevels:  []string{"low", "medium", "high", "ultra"},
			},
			effort: "ultra",
			want:   "high",
		},
		{
			name:   "ultra prefers max",
			info:   &ModelInfo{SupportedReasoningLevels: []string{"low", "medium", "high", "xhigh", "max", "ultra"}},
			effort: "ultra",
			want:   "max",
		},
		{
			name:   "ultra falls back to highest non-ultra",
			info:   &ModelInfo{SupportedReasoningLevels: []string{"low", "medium", "high"}},
			effort: "ultra",
			want:   "high",
		},
		{
			name:   "ultra defaults to medium without levels",
			info:   &ModelInfo{},
			effort: "ultra",
			want:   "medium",
		},
		{
			name:   "persistent becomes disabled",
			info:   &ModelInfo{SupportedReasoningLevels: []string{"low", "medium", "high"}},
			effort: "persistent",
			want:   "disabled",
		},
		{
			name:   "other efforts pass through",
			info:   &ModelInfo{SupportedReasoningLevels: []string{"low", "medium", "high"}},
			effort: "medium",
			want:   "medium",
		},
		{
			name:   "custom efforts pass through resolve but are not known",
			info:   &ModelInfo{},
			effort: "turbo",
			want:   "turbo",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolveReasoningEffort(tc.info, tc.effort); got != tc.want {
				t.Fatalf("ResolveReasoningEffort(%q) = %q, want %q", tc.effort, got, tc.want)
			}
		})
	}
}

func TestIsKnownReasoningEffortLikeRust(t *testing.T) {
	for _, effort := range []string{"none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra", "persistent", "disabled"} {
		if !IsKnownReasoningEffort(effort) {
			t.Fatalf("IsKnownReasoningEffort(%q) = false, want true", effort)
		}
	}
	for _, effort := range []string{"", "turbo", "custom-effort"} {
		if IsKnownReasoningEffort(effort) {
			t.Fatalf("IsKnownReasoningEffort(%q) = true, want false", effort)
		}
	}
}
