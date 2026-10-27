package model

import (
	"errors"
	"testing"

	"codex_go/codexapi"
)

// Mirrors Rust #55607: a provider that requires Codex authentication must
// report a transient error when managed authentication is unavailable instead
// of returning headers that would send the request anonymously.
func TestResolveProviderAuthReportsUnavailableManagedAuthLikeRust(t *testing.T) {
	managed := ProviderInfo{Name: "openai", RequiresOpenAIAuth: true}
	if _, err := ResolveProviderAuth(nil, managed); !errors.Is(err, codexapi.ErrAuthTransient) {
		t.Fatalf("error = %v, want ErrAuthTransient", err)
	}

	// Providers that never use Codex auth still send unauthenticated requests.
	headers, err := ResolveProviderAuth(nil, ProviderInfo{Name: "local", RequiresOpenAIAuth: false})
	if err != nil {
		t.Fatalf("local provider error = %v", err)
	}
	if len(headers.Headers) != 0 {
		t.Fatalf("local provider headers = %v, want empty", headers.Headers)
	}

	// A provider-configured credential resolves without managed auth.
	withToken, err := ResolveProviderAuth(nil, ProviderInfo{
		Name:                    "openai",
		RequiresOpenAIAuth:      true,
		ExperimentalBearerToken: "static-token",
	})
	if err != nil {
		t.Fatalf("static bearer error = %v", err)
	}
	if got := withToken.Headers.Get("Authorization"); got != "Bearer static-token" {
		t.Fatalf("static bearer header = %q", got)
	}
}
