package config

import "testing"

// Mirrors Rust's [features.guardianv2].transcript_mode (#51465): line is the
// default and only the exact json value selects structured records.
func TestGuardianV2TranscriptFormatLikeRust(t *testing.T) {
	if got := (&Config{}).GuardianV2TranscriptFormat(); got != "line" {
		t.Fatalf("empty config transcript format = %q, want line", got)
	}
	cases := []struct {
		name  string
		value any
		want  string
	}{
		{name: "json", value: "json", want: "json"},
		{name: "padded json", value: " JSON ", want: "json"},
		{name: "line", value: "line", want: "line"},
		{name: "unknown", value: "yaml", want: "line"},
		{name: "wrong type", value: 3, want: "line"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			cfg := &Config{Values: map[string]any{
				"features": map[string]any{
					"guardianv2": map[string]any{"transcript_mode": testCase.value},
				},
			}}
			if got := cfg.GuardianV2TranscriptFormat(); got != testCase.want {
				t.Fatalf("GuardianV2TranscriptFormat() = %q, want %q", got, testCase.want)
			}
		})
	}
}
