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

// TestRegistryErrorDetailEnvelopePrecedenceLikeRust mirrors Rust #50465
// `ef8cfe5e96` (RegistryErrorBody::into_error): the canonical `error` envelope
// stays authoritative when a structured `detail` is also present, while a bare
// structured `detail` is accepted as a fallback (codex-rs/exec-server/src/remote.rs,
// test `canonical_error_overrides_auth_detail`).
func TestRegistryErrorDetailEnvelopePrecedenceLikeRust(t *testing.T) {
	cases := []struct {
		name        string
		body        string
		wantCode    *string
		wantMessage string
	}{
		{
			name:        "structured_detail_fallback",
			body:        `{"detail":{"code":"authentication_service_unavailable","message":"Authentication service unavailable"}}`,
			wantCode:    strPtr("authentication_service_unavailable"),
			wantMessage: "Authentication service unavailable",
		},
		{
			name:        "canonical_error_overrides_auth_detail",
			body:        `{"error":{"code":"registration_denied","message":"registration unavailable"},"detail":{"code":"authentication_service_unavailable","message":"Authentication service unavailable"}}`,
			wantCode:    strPtr("registration_denied"),
			wantMessage: "registration unavailable",
		},
		{
			name:        "string_detail_is_not_an_error_envelope",
			body:        `{"detail":"Authentication service unavailable"}`,
			wantCode:    nil,
			wantMessage: `{"detail":"Authentication service unavailable"}`,
		},
	}
	for _, tc := range cases {
		code, message := registryHTTPErrorMessage(tc.body)
		if !equalOptionalString(code, tc.wantCode) {
			t.Fatalf("%s: registryHTTPErrorMessage code = %v, want %v", tc.name, derefString(code), derefString(tc.wantCode))
		}
		if message != tc.wantMessage {
			t.Fatalf("%s: registryHTTPErrorMessage message = %q, want %q", tc.name, message, tc.wantMessage)
		}
	}
}

// TestRegistryRegistrationPreWriteRetryClassificationLikeRust mirrors Rust
// #50465 `ef8cfe5e96`
// (codex-rs/exec-server/src/remote/registration_retry_tests.rs,
// registration_requires_a_confirmed_pre_write_failure_before_replay): a
// registration is only replayed for a confirmed pre-write failure — HTTP 503
// `registration_conflict` or HTTP 502 `authentication_service_unavailable` —
// while the canonical error envelope overrides a structured detail and every
// other status/code stays terminal.
func TestRegistryRegistrationPreWriteRetryClassificationLikeRust(t *testing.T) {
	key, err := generateRemotePublicKey()
	if err != nil {
		t.Fatalf("generateRemotePublicKey() error = %v", err)
	}

	const (
		conflictBody               = `{"error":{"code":"registration_conflict","message":"registration unavailable"}}`
		conflictWithStringDetail   = `{"error":{"code":"registration_conflict","message":"registration unavailable"},"detail":"additional diagnostics"}`
		conflictWithStructuredDest = `{"error":{"code":"registration_conflict","message":"registration unavailable"},"detail":{"code":"registration_denied","message":"additional diagnostics"}}`
		authUnavailableBody        = `{"detail":{"code":"authentication_service_unavailable","message":"Authentication service unavailable"}}`
		errorWithAuthDetailBody    = `{"error":{"code":"registration_denied","message":"registration unavailable"},"detail":{"code":"authentication_service_unavailable","message":"Authentication service unavailable"}}`
		errorBody                  = `{"error":{"code":"registration_denied","message":"registration unavailable"}}`
		legacyStringDetailBody     = `{"detail":"Authentication service unavailable"}`
	)

	cases := []struct {
		name      string
		status    int
		body      string
		wantRetry bool
	}{
		{"confirmed_conflict", http.StatusServiceUnavailable, conflictBody, true},
		{"conflict_with_string_detail", http.StatusServiceUnavailable, conflictWithStringDetail, true},
		{"conflict_with_structured_detail", http.StatusServiceUnavailable, conflictWithStructuredDest, true},
		{"auth_outage", http.StatusBadGateway, authUnavailableBody, true},
		{"canonical_error_overrides_auth_detail", http.StatusBadGateway, errorWithAuthDetailBody, false},
		{"auth_outage_wrong_status", http.StatusServiceUnavailable, authUnavailableBody, false},
		{"gateway_with_conflict_code", http.StatusBadGateway, conflictBody, false},
		{"unconfirmed_gateway_error", http.StatusBadGateway, "not JSON", false},
		{"legacy_string_detail", http.StatusBadGateway, legacyStringDetailBody, false},
		{"different_error_code", http.StatusServiceUnavailable, errorBody, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			attempts := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attempts++
				w.Header().Set("Content-Type", "application/json")
				if attempts == 1 {
					w.WriteHeader(tc.status)
					_, _ = w.Write([]byte(tc.body))
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
			_, err := registerRemoteEnvironmentWithRetry(context.Background(), cfg, key)
			if tc.wantRetry {
				if err != nil {
					t.Fatalf("registerRemoteEnvironmentWithRetry() error = %v, want success after replay", err)
				}
				if attempts != 2 {
					t.Fatalf("attempts = %d, want 2 (confirmed pre-write failure must be replayed)", attempts)
				}
				return
			}
			if err == nil {
				t.Fatal("registerRemoteEnvironmentWithRetry() = nil error, want a terminal failure")
			}
			if attempts != 1 {
				t.Fatalf("attempts = %d, want 1 (unconfirmed failure must stay terminal)", attempts)
			}
		})
	}
}

// TestRegistryAuthOutageMessageLikeRust checks the retryable 502 body keeps the
// registry's message and code (Rust #50465 test
// `auth_outage_backoff_is_cancellable` asserts message == "Authentication service unavailable").
func TestRegistryAuthOutageMessageLikeRust(t *testing.T) {
	body := `{"detail":{"code":"authentication_service_unavailable","message":"Authentication service unavailable"}}`
	code, message := registryHTTPErrorMessage(body)
	if code == nil || *code != "authentication_service_unavailable" {
		t.Fatalf("code = %v, want authentication_service_unavailable", derefString(code))
	}
	if message != "Authentication service unavailable" {
		t.Fatalf("message = %q, want %q", message, "Authentication service unavailable")
	}
}

func equalOptionalString(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func derefString(v *string) string {
	if v == nil {
		return "<nil>"
	}
	return *v
}
