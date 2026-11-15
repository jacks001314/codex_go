package network

import (
	"strings"
	"testing"
)

// TestCanonicalProxyPatternComparisonKeysLikeRust mirrors Rust #61351: grant and
// restriction comparison keys keep the differences the proxy matcher sees.
func TestCanonicalProxyPatternComparisonKeysLikeRust(t *testing.T) {
	cases := []struct {
		selector    string
		grant       string
		restriction string
	}{
		{"?*.EXAMPLE.com.", "*.example.com", "*.example.com"},
		{"[::1]:*", "[::1]:*", "::1"},
		{"2001:0db8:0:0:0:0:0:1", "2001:0db8:0:0:0:0:0:1", "2001:db8::1"},
	}
	for _, testCase := range cases {
		grant, err := CanonicalAllowProxyPattern(testCase.selector)
		if err != nil {
			t.Fatalf("CanonicalAllowProxyPattern(%q) error = %v", testCase.selector, err)
		}
		if grant != testCase.grant {
			t.Fatalf("CanonicalAllowProxyPattern(%q) = %q, want %q", testCase.selector, grant, testCase.grant)
		}
		deny, err := CanonicalDenyProxyPattern(testCase.selector)
		if err != nil {
			t.Fatalf("CanonicalDenyProxyPattern(%q) error = %v", testCase.selector, err)
		}
		if deny != testCase.restriction {
			t.Fatalf("CanonicalDenyProxyPattern(%q) = %q, want %q", testCase.selector, deny, testCase.restriction)
		}
		review, err := CanonicalReviewProxyPattern(testCase.selector)
		if err != nil {
			t.Fatalf("CanonicalReviewProxyPattern(%q) error = %v", testCase.selector, err)
		}
		if review != testCase.restriction {
			t.Fatalf("CanonicalReviewProxyPattern(%q) = %q, want %q", testCase.selector, review, testCase.restriction)
		}
	}
}

// TestCanonicalProxyPatternValidationLikeRust mirrors the proxy matcher's
// validation rules, including the deny-only global wildcard restriction.
func TestCanonicalProxyPatternValidationLikeRust(t *testing.T) {
	rejected := []struct {
		pattern string
		want    string
	}{
		{"exa mple.com", "unsupported domain pattern syntax"},
		{"example.com/path", "unsupported domain pattern syntax"},
		{"example.com\\path", "unsupported domain pattern syntax"},
		{"{example.com,example.org}", "unsupported domain pattern syntax"},
		{"**.example.**.com", "unsupported repeated wildcard"},
		{"**example.com", "unsupported repeated wildcard"},
		{"example.com]", "brackets are only supported"},
		{"[example.com]", "brackets are only supported"},
		{"[::1", "brackets are only supported"},
		{"*.", "domain pattern is empty"},
		{"**.", "domain pattern is empty"},
		{"", "domain pattern is empty"},
	}
	for _, testCase := range rejected {
		for _, canonical := range []func(string) (string, error){CanonicalAllowProxyPattern, CanonicalDenyProxyPattern, CanonicalReviewProxyPattern} {
			if _, err := canonical(testCase.pattern); err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("canonical pattern %q error = %v, want %q", testCase.pattern, err, testCase.want)
			}
		}
	}
	for _, pattern := range []string{"*", "**.*"} {
		if _, err := CanonicalDenyProxyPattern(pattern); err == nil || !strings.Contains(err.Error(), "unsupported global wildcard") {
			t.Fatalf("CanonicalDenyProxyPattern(%q) error = %v, want the global wildcard rejection", pattern, err)
		}
	}
	if grant, err := CanonicalAllowProxyPattern("*"); err != nil || grant != "*" {
		t.Fatalf("CanonicalAllowProxyPattern(*) = %q, %v; want global wildcard grant", grant, err)
	}
	if review, err := CanonicalReviewProxyPattern("*"); err != nil || review != "*" {
		t.Fatalf("CanonicalReviewProxyPattern(*) = %q, %v; want global wildcard review", review, err)
	}
	if grant, err := CanonicalAllowProxyPattern("[*]"); err != nil || grant != "*" {
		t.Fatalf("CanonicalAllowProxyPattern([*]) = %q, %v; want bounded wildcard literal", grant, err)
	}
}
