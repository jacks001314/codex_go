package turn

import "testing"

func TestSkillAliasSourceLikeRust(t *testing.T) {
	cases := []struct {
		locator string
		want    string
	}{
		{"c0/skills/alpha", "cloud"},
		{"e12/skills/beta", "executor"},
		{"r3/skills/gamma", "host"},
		{"e0", ""},
		{"e/alpha", ""},
		{"e01/alpha", ""},
		{"e-1/alpha", ""},
		{"x0/alpha", ""},
		{"\uff450/alpha", ""},
		{"e0/", ""},
	}
	for _, testCase := range cases {
		if got := skillAliasSource(testCase.locator); got != testCase.want {
			t.Fatalf("skillAliasSource(%q) = %q, want %q", testCase.locator, got, testCase.want)
		}
	}
}
