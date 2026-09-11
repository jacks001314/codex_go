package network

import (
	"math/rand"
	"strings"
	"testing"
)

// TestGenerateCredentialDummyMatchesPattern mirrors Rust #44056's requirement
// that a brokered dummy independently satisfies the configured credential
// pattern (so clients still see a plausibly-formatted credential).
func TestGenerateCredentialDummyMatchesPattern(t *testing.T) {
	patterns := []string{
		"vk-[a-z0-9]{16}",
		"[A-Z]{3}-[0-9]{4}",
		"ghp_[A-Za-z0-9]{36}",
		"(foo|bar)-[0-9]+",
		"sk-[a-zA-Z0-9]{10,20}",
	}
	rng := rand.New(rand.NewSource(1))
	for _, pattern := range patterns {
		matcher, err := compileCredentialPattern(pattern)
		if err != nil {
			t.Fatalf("compileCredentialPattern(%q) error = %v", pattern, err)
		}
		for attempt := 0; attempt < 20; attempt++ {
			dummy, ok := generateCredentialDummyWithRand(pattern, "", rng)
			if !ok {
				t.Fatalf("generateCredentialDummyWithRand(%q) failed", pattern)
			}
			if !matcher.MatchString(dummy) {
				t.Fatalf("dummy %q does not match %q", dummy, pattern)
			}
			if dummy == "" || len(dummy) > maxCredentialDummyBytes {
				t.Fatalf("dummy %q out of bounds", dummy)
			}
		}
	}
}

func TestCredentialPatternValidationLikeRust(t *testing.T) {
	if _, err := compileCredentialPattern("a|"); err == nil || !strings.Contains(err.Error(), "must not match an empty value") {
		t.Fatalf("empty-matching pattern error = %v", err)
	}
	if _, err := credentialPatternMatches("vk-[a-z0-9]{16}", "vk-0123456789abcdef"); err != nil {
		t.Fatalf("matching pattern error = %v", err)
	}
	if matches, err := credentialPatternMatches("vk-[a-z0-9]{16}", "vk-short"); err != nil || matches {
		t.Fatalf("non-matching pattern = %v/%v", matches, err)
	}
	if _, err := ParseCredentialProviderConfigs(map[string]any{"vendor": map[string]any{
		"env":          []any{"VENDOR_TOKEN"},
		"patterns":     []any{"a|"},
		"url_prefixes": []any{"https://api.vendor.example"},
		"auth":         []any{"bearer"},
	}}); err == nil || !strings.Contains(err.Error(), "must not match an empty value") {
		t.Fatalf("provider validation error = %v", err)
	}
}
