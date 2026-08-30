package execserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestRegistrationConflictRetryDelayBoundsLikeRust mirrors the Rust
// client_recovery_tests::registry_recovery_retry_delay_exponentially_backs_off_and_caps
// case expectations: base delay doubles every attempt, capped at the max, with
// jitter up to half the base.
func TestRegistrationConflictRetryDelayBoundsLikeRust(t *testing.T) {
	cases := []struct {
		attempt uint32
		base    time.Duration
	}{
		{0, 500 * time.Millisecond},
		{1, time.Second},
		{2, 2 * time.Second},
		{3, 4 * time.Second},
		{4, 5 * time.Second},
		{20, 5 * time.Second},
	}
	for _, tc := range cases {
		delay := registrationConflictRetryDelay("session-1", tc.attempt)
		if delay < tc.base {
			t.Fatalf("delay %v for attempt %d, want >= %v", delay, tc.attempt, tc.base)
		}
		if delay > tc.base+tc.base/2 {
			t.Fatalf("delay %v for attempt %d, want <= %v", delay, tc.attempt, tc.base+tc.base/2)
		}
	}
}

// TestRegisterRemoteEnvironmentWithRetry retries only explicit 503
// registration_conflict responses and fails fast on other errors (Rust #41219).
func TestRegisterRemoteEnvironmentWithRetry(t *testing.T) {
	key, err := generateRemotePublicKey()
	if err != nil {
		t.Fatalf("generateRemotePublicKey() error = %v", err)
	}

	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.Header().Set("Content-Type", "application/json")
		if attempts <= 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(registryErrorBody{Error: &registryError{
				Code:    strPtr("registration_conflict"),
				Message: strPtr("conflict"),
			}})
			return
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(remoteRegistrationResponse{
			EnvironmentID:   "env-1",
			SecurityProfile: RemoteSecurityProfile,
		})
	}))
	defer server.Close()

	cfg := RemoteEnvironmentConfig{
		BaseURL:       server.URL,
		EnvironmentID: "env-1",
		HTTPClient:    server.Client(),
	}
	registration, err := registerRemoteEnvironmentWithRetry(context.Background(), cfg, key)
	if err != nil {
		t.Fatalf("registerRemoteEnvironmentWithRetry() error = %v", err)
	}
	if registration == nil || registration.EnvironmentID != "env-1" {
		t.Fatalf("registration = %#v", registration)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
}

// TestRegisterRemoteEnvironmentWithRetryDoesNotRetryAmbiguousFailure verifies an
// error that is not an explicit 503 registration_conflict fails immediately.
func TestRegisterRemoteEnvironmentWithRetryDoesNotRetryAmbiguousFailure(t *testing.T) {
	key, err := generateRemotePublicKey()
	if err != nil {
		t.Fatalf("generateRemotePublicKey() error = %v", err)
	}

	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(registryErrorBody{Error: &registryError{
			Code:    strPtr("other_error"),
			Message: strPtr("boom"),
		}})
	}))
	defer server.Close()

	cfg := RemoteEnvironmentConfig{
		BaseURL:       server.URL,
		EnvironmentID: "env-1",
		HTTPClient:    server.Client(),
	}
	_, err = registerRemoteEnvironmentWithRetry(context.Background(), cfg, key)
	if err == nil {
		t.Fatalf("registerRemoteEnvironmentWithRetry() error = nil, want non-nil")
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
}

func strPtr(value string) *string {
	return &value
}
