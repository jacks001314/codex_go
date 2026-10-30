package execserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
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
