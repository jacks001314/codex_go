package app

import "testing"

// TestExecServerPreferMXCFromOverridesMatchesRust mirrors Rust
// cli/src/exec_server_command.rs: the executor retains exactly one startup
// value for executor-local config reads, an explicit boolean
// `features.prefer_mxc`, and ignores unparsable overrides or non-boolean values.
func TestExecServerPreferMXCFromOverridesMatchesRust(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		overrides []string
		want      *bool
	}{
		{name: "absent", overrides: nil},
		{name: "enabled", overrides: []string{"features.prefer_mxc=true"}, want: boolPointer(true)},
		{name: "disabled", overrides: []string{"features.prefer_mxc=false"}, want: boolPointer(false)},
		{name: "unrelated override", overrides: []string{"model=gpt-5"}},
		{name: "non-boolean value", overrides: []string{"features.prefer_mxc=yes"}},
		{name: "unparsable override", overrides: []string{"features.prefer_mxc"}},
		{name: "later override wins", overrides: []string{"features.prefer_mxc=false", "features.prefer_mxc=true"}, want: boolPointer(true)},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got := execServerPreferMXCFromOverrides(testCase.overrides)
			switch {
			case testCase.want == nil && got != nil:
				t.Fatalf("prefer_mxc = %v, want nil", *got)
			case testCase.want != nil && got == nil:
				t.Fatalf("prefer_mxc = nil, want %v", *testCase.want)
			case testCase.want != nil && got != nil && *got != *testCase.want:
				t.Fatalf("prefer_mxc = %v, want %v", *got, *testCase.want)
			}
		})
	}
}

func boolPointer(value bool) *bool { return &value }
