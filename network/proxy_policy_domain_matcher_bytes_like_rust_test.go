package network

import "testing"

// Mirrors Rust network-proxy/src/domain_matcher_tests.rs
// `unicode_wildcards_preserve_utf8_byte_matching` (#52277 / commit
// 14ff9c3a43ebf9bf543cf5f7c45cff2bfea2e512): domain wildcards match UTF-8
// bytes, so `?` consumes exactly one byte of a multi-byte character and literal
// comparisons stay byte exact. Both the allowlist and denylist compile arms are
// covered, like the Rust test does.
func TestProxyDomainMatcherUsesUTF8ByteSemanticsLikeRust(t *testing.T) {
	cases := []struct {
		name    string
		pattern string
		host    string
		want    bool
	}{
		// Upstream cases, byte-for-byte.
		{name: "ascii question mark", pattern: "api?.com", host: "api1.com", want: true},
		{name: "two bytes cover e-acute", pattern: "api??.com", host: "api\u00e9.com", want: true},
		// Discriminating case: one `?` is one byte, so it cannot cover U+00E9.
		{name: "one byte cannot cover e-acute", pattern: "api?.com", host: "api\u00e9.com", want: false},
		{name: "four bytes cover emoji", pattern: "api????.com", host: "api\U0001F600.com", want: true},
		{name: "three bytes cover e plus combining accent", pattern: "api???.com", host: "apie\u0301.com", want: true},
		{name: "question mark then star", pattern: "api?*.com", host: "api\u00e9.com", want: true},
		{name: "multibyte label body", pattern: "b\u00fcch?.de", host: "b\u00fcch1.de", want: true},
		{name: "multibyte literal", pattern: "b\u00fccher.de", host: "b\u00fccher.de", want: true},
		{name: "no idna conversion", pattern: "b\u00fccher.de", host: "xn--bcher-kva.de", want: false},
		{name: "punycode glob", pattern: "xn--*.de", host: "xn--bcher-kva.de", want: true},
		// Three bytes per CJK scalar value: the subdomain arm still needs one byte
		// per `?`, so a rune-based matcher would wrongly match a.CJK-A-CJK-B.com.
		{name: "cjk subdomain bytes", pattern: "*.\u4f8b???.com", host: "a.\u4f8b\u5b50.com", want: true},
		{name: "cjk apex is not a subdomain", pattern: "*.\u4f8b???.com", host: "\u4f8b\u5b50.com", want: false},
		{name: "cjk apex expansion", pattern: "**.\u4f8b???.com", host: "\u4f8b\u5b50.com", want: true},
	}
	for _, tc := range cases {
		for _, rejectGlobal := range []bool{false, true} {
			matcher, err := CompileProxyDomainMatcher([]string{tc.pattern}, rejectGlobal)
			if err != nil {
				t.Fatalf("%s: CompileProxyDomainMatcher(%q, %v) error = %v", tc.name, tc.pattern, rejectGlobal, err)
			}
			if got := matcher.Match(tc.host); got != tc.want {
				t.Fatalf("%s: Match(%q) with pattern %q (rejectGlobalWildcard=%v) = %v, want %v",
					tc.name, tc.host, tc.pattern, rejectGlobal, got, tc.want)
			}
		}
	}
}

// ASCII hosts, ports, trailing dots, case folding, `*` and `[...]` classes keep
// the exact pre-change behavior (the mapping is the identity for ASCII), so the
// byte matcher is a pure widening for non-ASCII hosts.
func TestProxyDomainMatcherKeepsAsciiSemanticsLikeRust(t *testing.T) {
	cases := []struct {
		name    string
		pattern string
		host    string
		want    bool
	}{
		{name: "subdomains only", pattern: "*.example.com", host: "api.example.com", want: true},
		{name: "deep subdomain", pattern: "*.example.com", host: "deep.api.example.com", want: true},
		{name: "apex excluded", pattern: "*.example.com", host: "example.com", want: false},
		{name: "apex included", pattern: "**.example.com", host: "example.com", want: true},
		{name: "class matches", pattern: "api-[0-9].openai.com", host: "api-7.openai.com", want: true},
		{name: "class rejects", pattern: "api-[0-9].openai.com", host: "api-x.openai.com", want: false},
		{name: "single byte wildcard", pattern: "api?.openai.com", host: "api1.openai.com", want: true},
		{name: "single byte wildcard is one unit", pattern: "api?.openai.com", host: "api12.openai.com", want: false},
		{name: "star absorbs label", pattern: "api*.openai.com", host: "apiv2.openai.com", want: true},
		{name: "global wildcard", pattern: "*", host: "anything.example.com", want: true},
		{name: "global wildcard multibyte host", pattern: "*", host: "\u00e9\U0001F600.com", want: true},
		{name: "case insensitive pattern and host", pattern: "*.Example.COM.", host: "API.EXAMPLE.COM", want: true},
		{name: "port is stripped", pattern: "*.example.com", host: "api.example.com:8443", want: true},
		{name: "trailing dot is stripped", pattern: "api.example.com", host: "api.example.com.", want: true},
		{name: "exact mismatch", pattern: "api.example.com", host: "other.example.com", want: false},
	}
	for _, tc := range cases {
		matcher, err := CompileProxyDomainMatcher([]string{tc.pattern}, false)
		if err != nil {
			t.Fatalf("%s: CompileProxyDomainMatcher(%q) error = %v", tc.name, tc.pattern, err)
		}
		if got := matcher.Match(tc.host); got != tc.want {
			t.Fatalf("%s: Match(%q) with pattern %q = %v, want %v", tc.name, tc.host, tc.pattern, got, tc.want)
		}
	}
}
