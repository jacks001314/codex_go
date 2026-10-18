package tui

import (
	"strings"
	"testing"
)

func TestStatusCardUsesServerRequiresOpenAIAuthLikeRust(t *testing.T) {
	const usageURL = "https://chatgpt.com/codex/settings/usage"
	tests := []struct {
		name      string
		provider  string
		requires  *bool
		wantUsage bool
	}{
		{name: "openai proxy without server auth", provider: "openai-proxy", requires: boolPtrStatusTest(false)},
		{name: "custom provider with server auth", provider: "custom-proxy", requires: boolPtrStatusTest(true), wantUsage: true},
		{name: "fallback openai provider", provider: "openai", wantUsage: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			state := NewState(&Options{
				Provider:           tc.provider,
				RequiresOpenAIAuth: tc.requires,
			})
			state.SetThreadID("thread-1")
			card := state.RenderStatusCardWidth(100)
			if got := strings.Contains(card, usageURL); got != tc.wantUsage {
				t.Fatalf("usage link present = %v, want %v:\n%s", got, tc.wantUsage, card)
			}
		})
	}
}

func boolPtrStatusTest(value bool) *bool {
	return &value
}
