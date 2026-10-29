package network

import "testing"

// Mirrors Rust #46027 `compile_globset_supports_question_mark_wildcards`: a `?`
// matches exactly one byte in allow and deny domain patterns, including a dot,
// and combines with `*`, `*.`, and `**.` prefixes.
func TestCompileProxyDomainMatcherSupportsQuestionMarkWildcardsLikeRust(t *testing.T) {
	for _, tc := range []struct {
		pattern string
		host    string
		want    bool
	}{
		{"api?.example.com", "api1.example.com", true},
		{"api?.example.com", "api.example.com", false},
		{"api?.example.com", "api12.example.com", false},
		{"api??.example.com", "api12.example.com", true},
		{"api??.example.com", "api1.example.com", false},
		{"api*?.example.com", "api.example.com", false},
		{"api*?.example.com", "api1.example.com", true},
		{"api*?.example.com", "api123.example.com", true},
		{"api?example.com", "api.example.com", true},
		{"*.api?.example.com", "api1.example.com", false},
		{"*.api?.example.com", "www.api1.example.com", true},
		{"*.api?.example.com", "nested.www.api1.example.com", true},
		{"*.api?.example.com", "www.api12.example.com", false},
		{"**.api?.example.com", "api1.example.com", true},
		{"**.api?.example.com", "www.api1.example.com", true},
		{"**.api?.example.com", "nested.www.api1.example.com", true},
		{"**.api?.example.com", "api12.example.com", false},
		{"**.api?.example.com", "www.api12.example.com", false},
		{" API?.EXAMPLE.COM. ", "API1.EXAMPLE.COM.", true},
	} {
		for _, rejectGlobal := range []bool{false, true} {
			matcher, err := CompileProxyDomainMatcher([]string{tc.pattern}, rejectGlobal)
			if err != nil {
				t.Fatalf("CompileProxyDomainMatcher(%q, %v) error = %v", tc.pattern, rejectGlobal, err)
			}
			if got := matcher.Match(tc.host); got != tc.want {
				t.Fatalf("Match(pattern %q, host %q, rejectGlobal %v) = %v, want %v", tc.pattern, tc.host, rejectGlobal, got, tc.want)
			}
		}
	}
}
