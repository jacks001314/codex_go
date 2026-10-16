package model

import "testing"

// TestGuardianCyberAccessProgramNormalizesDaybreakLikeRust mirrors Rust
// `guardian-reviewer::settings::guardian_cyber_access_program` (#52689):
// `Standard => Standard`, `DaybreakBlue | DaybreakRed => DaybreakBlue`, and no
// selection stays None. An unknown program is passed through so the later
// `for_auth` gate decides it, exactly like Rust's typed enum would only ever
// hold its own variants.
func TestGuardianCyberAccessProgramNormalizesDaybreakLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		program string
		want    string
	}{
		{program: "daybreak_blue", want: "daybreak_blue"},
		{program: "daybreak_red", want: "daybreak_blue"},
		{program: "standard", want: "standard"},
		{program: "", want: ""},
		{program: "future_program", want: "future_program"},
	} {
		if got := GuardianCyberAccessProgram(testCase.program); got != testCase.want {
			t.Fatalf("GuardianCyberAccessProgram(%q) = %q, want %q", testCase.program, got, testCase.want)
		}
	}
}
