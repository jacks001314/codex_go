package execserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// Rust #58251: credential resolution that fails before the registration is sent
// is retried, and the registration itself is sent exactly once.
func TestRegistrationRetriesTransientCredentialFailureLikeRust(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(remoteRegistrationResponse{
			EnvironmentID:   "env",
			SecurityProfile: RemoteSecurityProfile,
		})
	}))
	defer server.Close()

	resolutions := 0
	cfg := RemoteEnvironmentConfig{
		BaseURL:       server.URL,
		EnvironmentID: "env",
		HTTPClient:    http.DefaultClient,
		ResolveAuthHeaders: func(context.Context) (http.Header, error) {
			resolutions++
			if resolutions == 1 {
				return nil, fmt.Errorf("%w: metadata unavailable", ErrRegistryCredentialsTransient)
			}
			return http.Header{}, nil
		},
	}
	registration, err := registerRemoteEnvironmentWithRetry(context.Background(), cfg, RemotePublicKey{Suite: noiseChannelSuite})
	if err != nil {
		t.Fatalf("registerRemoteEnvironmentWithRetry() error = %v", err)
	}
	if registration.EnvironmentID != "env" {
		t.Fatalf("registration = %#v", registration)
	}
	if resolutions != 2 {
		t.Fatalf("credential resolutions = %d, want 2", resolutions)
	}
	mu.Lock()
	defer mu.Unlock()
	if requests != 1 {
		t.Fatalf("registration requests = %d, want 1", requests)
	}
}

// A terminal credential failure (identity change) must never be replayed.
func TestRegistrationDoesNotRetryTerminalCredentialFailureLikeRust(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := RemoteEnvironmentConfig{
		BaseURL:       server.URL,
		EnvironmentID: "env",
		HTTPClient:    http.DefaultClient,
		ResolveAuthHeaders: func(context.Context) (http.Header, error) {
			return nil, fmt.Errorf("executor authentication changed")
		},
	}
	if _, err := registerRemoteEnvironmentWithRetry(context.Background(), cfg, RemotePublicKey{}); err == nil {
		t.Fatal("terminal credential failure was retried")
	}
	if requests != 0 {
		t.Fatalf("registration requests = %d, want 0", requests)
	}
}

// Rust #58355: a credential deadline that expires before the first send is
// RequestNotSent, so the registration replay loop recovers instead of treating
// it as an ambiguous transport timeout.
func TestRegistrationRetriesUnsentCredentialTimeoutLikeRust(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(remoteRegistrationResponse{
			EnvironmentID:   "env",
			SecurityProfile: RemoteSecurityProfile,
		})
	}))
	defer server.Close()

	resolutions := 0
	cfg := RemoteEnvironmentConfig{
		BaseURL:       server.URL,
		EnvironmentID: "env",
		HTTPClient:    http.DefaultClient,
		ResolveAuthHeaders: func(context.Context) (http.Header, error) {
			resolutions++
			if resolutions == 1 {
				return nil, fmt.Errorf("credential metadata: %w", context.DeadlineExceeded)
			}
			return http.Header{}, nil
		},
	}
	registration, err := registerRemoteEnvironmentWithRetry(context.Background(), cfg, RemotePublicKey{Suite: noiseChannelSuite})
	if err != nil {
		t.Fatalf("registerRemoteEnvironmentWithRetry() error = %v", err)
	}
	if registration.EnvironmentID != "env" {
		t.Fatalf("registration = %#v", registration)
	}
	if resolutions != 2 {
		t.Fatalf("credential resolutions = %d, want 2", resolutions)
	}
	if requests != 1 {
		t.Fatalf("registration requests = %d, want 1", requests)
	}
}

// Rust #58355: once a request was sent, a deadline is an ambiguous transport
// timeout and registration must not replay it.
func TestRegistrationDoesNotRetrySentTransportTimeoutLikeRust(t *testing.T) {
	requests := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests <- struct{}{}
		time.Sleep(time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	cfg := RemoteEnvironmentConfig{
		BaseURL:            server.URL,
		EnvironmentID:      "env",
		HTTPClient:         server.Client(),
		ResolveAuthHeaders: func(context.Context) (http.Header, error) { return http.Header{}, nil },
	}
	_, err := registerRemoteEnvironmentWithRetry(ctx, cfg, RemotePublicKey{Suite: noiseChannelSuite})
	if err == nil {
		t.Fatal("registerRemoteEnvironmentWithRetry() = nil, want the transport timeout")
	}
	if errors.Is(err, ErrRegistryCredentialsTransient) {
		t.Fatalf("sent transport timeout classified as unsent: %v", err)
	}
	select {
	case <-requests:
	case <-time.After(time.Second):
		t.Fatal("registration request was never sent")
	}
}
