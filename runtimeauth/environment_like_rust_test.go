package runtimeauth

import (
	"context"
	"errors"
	"testing"

	"codex_go/auth"
)

// TestValidateRuntimeEnvironmentLikeRust covers Rust #59823: runtime
// credentials must stay inside the production or staging environment the
// runtime registered with. Explicit registration targets stay exempt.
func TestValidateRuntimeEnvironmentLikeRust(t *testing.T) {
	cases := []struct {
		name            string
		registrationURL string
		destination     string
		wantErr         bool
	}{
		{"production registration, production destination", DefaultRegistrationURL, "https://api.openai.com/v1", false},
		{"production registration, staging destination", DefaultRegistrationURL, "https://api.openai.org/v1", true},
		{"production registration, staging chatgpt destination", DefaultRegistrationURL, "https://foo.chatgpt-staging.com/backend-api", true},
		{"staging registration, staging destination", StagingRegistrationURL, "https://api.openai.org/v1", false},
		{"staging registration, production destination", StagingRegistrationURL, "https://api.openai.com/v1", true},
		{"staging registration, implicit production default", StagingRegistrationURL, "", true},
		{"explicit registration target", "http://127.0.0.1:9/agent/register", "https://api.openai.com/v1", false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			err := ValidateRuntimeEnvironment(testCase.registrationURL, testCase.destination)
			if testCase.wantErr {
				if !errors.Is(err, ErrRuntimeEnvironmentMismatch) {
					t.Fatalf("error = %v, want ErrRuntimeEnvironmentMismatch", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("error = %v, want nil", err)
			}
		})
	}
}

// TestHeadersForDestinationRejectsEnvironmentMismatchLikeRust ensures the
// check runs before any runtime registration, so a mismatched request never
// sends credentials to the other environment.
func TestHeadersForDestinationRejectsEnvironmentMismatchLikeRust(t *testing.T) {
	fake := newFakeRegistrationServer(t)
	manager, err := NewManager(ManagerOptions{
		Credentials:      func(context.Context) (*auth.AuthDotJSON, error) { return chatgptSnapshot("token"), nil },
		SourceGeneration: func() uint64 { return 1 },
		InstallationID:   "88888888-8888-4888-8888-888888888888",
		RegistrationURL:  StagingRegistrationURL,
		HTTPClient:       fake.server.Client(),
	})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	provider := &Provider{Manager: manager, Mode: ModeDualHeader}
	if headers, err := provider.HeadersForDestination(context.Background(), "https://api.openai.com/v1"); !errors.Is(err, ErrRuntimeEnvironmentMismatch) || headers != nil {
		t.Fatalf("production destination = %#v, %v; want environment mismatch", headers, err)
	}
	if fake.count() != 0 {
		t.Fatalf("mismatched destination registered a runtime")
	}
}
