package execserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// Rust #55987 fixtures (codex-rs/exec-server/src/remote/auth_tests.rs). The
// hyphens keep both credentials out of randomly generated Base64 key material.
const (
	rejectedRegistryAuthorization  = "Bearer test-old-token"
	refreshedRegistryAuthorization = "Bearer test-new-token"
)

// rotatingRegistryAuth is Rust's `RotatingAuth`: it serves the old credential
// until refresh_after_unauthorized reports a replacement.
type rotatingRegistryAuth struct {
	mu        sync.Mutex
	refreshes int
	rejected  []http.Header
}

func (a *rotatingRegistryAuth) ResolveAuthHeaders(context.Context) (http.Header, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	authorization := rejectedRegistryAuthorization
	if a.refreshes > 0 {
		authorization = refreshedRegistryAuthorization
	}
	return http.Header{"Authorization": []string{authorization}}, nil
}

func (a *rotatingRegistryAuth) RefreshAfterUnauthorized(_ context.Context, rejected http.Header) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.rejected = append(a.rejected, rejected.Clone())
	a.refreshes++
	return true
}

func (a *rotatingRegistryAuth) refreshCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.refreshes
}

// registryAuthTestConfig builds a registry config for one operation and auth
// mode. Scoped mode requires the single-bearer provider and a loopback URL.
func registryAuthTestConfig(t *testing.T, serverURL string, auth *rotatingRegistryAuth, scoped bool) RemoteEnvironmentConfig {
	t.Helper()
	cfg := RemoteEnvironmentConfig{
		BaseURL:                  serverURL,
		EnvironmentID:            "env",
		HTTPClient:               http.DefaultClient,
		ResolveAuthHeaders:       auth.ResolveAuthHeaders,
		RefreshAfterUnauthorized: auth.RefreshAfterUnauthorized,
	}
	if !scoped {
		return cfg
	}
	scopedConfig, err := cfg.WithExecutorCredentialAuth()
	if err != nil {
		t.Fatalf("WithExecutorCredentialAuth() error = %v", err)
	}
	return scopedConfig
}

// TestRegistryOperationsReplayOnceWithReplacementCredentialsLikeRust mirrors
// Rust's `noise_registry_operations_retry_with_new_credentials`: register,
// connect, and harness-key validation all resolve auth per attempt and replay
// the identical request exactly once after a 401.
func TestRegistryOperationsReplayOnceWithReplacementCredentialsLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		operation string
		scoped    bool
	}{
		{"register", false},
		{"connect", false},
		{"validate", false},
		{"register", true},
		{"validate", true},
	} {
		name := testCase.operation + "/legacy"
		if testCase.scoped {
			name = testCase.operation + "/executor_credential"
		}
		t.Run(name, func(t *testing.T) {
			var mu sync.Mutex
			var authorizations []string
			var bodies [][]byte
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				mu.Lock()
				authorizations = append(authorizations, r.Header.Get("Authorization"))
				bodies = append(bodies, body)
				mu.Unlock()
				if r.Header.Get("Authorization") == rejectedRegistryAuthorization {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				switch testCase.operation {
				case "register":
					_ = json.NewEncoder(w).Encode(remoteRegistrationResponse{
						EnvironmentID:   "env",
						SecurityProfile: RemoteSecurityProfile,
					})
				case "connect":
					_ = json.NewEncoder(w).Encode(remoteConnectResponse{
						EnvironmentID:           "env",
						URL:                     "wss://relay.example/ws",
						SecurityProfile:         RemoteSecurityProfile,
						ExecutorRegistrationID:  "registration",
						HarnessKeyAuthorization: "authorization",
					})
				case "validate":
					_ = json.NewEncoder(w).Encode(remoteHarnessValidationResponse{Valid: true})
				}
			}))
			defer server.Close()

			auth := &rotatingRegistryAuth{}
			cfg := registryAuthTestConfig(t, server.URL, auth, testCase.scoped)
			key := RemotePublicKey{Suite: noiseChannelSuite}
			var err error
			switch testCase.operation {
			case "register":
				_, err = registerRemoteEnvironmentWithRetry(context.Background(), cfg, key)
			case "connect":
				provider, providerErr := NewRegistryNoiseRendezvousConnectProvider(cfg)
				if providerErr != nil {
					t.Fatalf("NewRegistryNoiseRendezvousConnectProvider() error = %v", providerErr)
				}
				_, err = provider.ConnectBundle(context.Background(), key)
			case "validate":
				registration := &remoteRegistrationResponse{
					EnvironmentID:          "env",
					ExecutorRegistrationID: "registration",
				}
				err = validateRemoteHarnessKey(context.Background(), cfg, registration, key, "authorization")
			}
			if err != nil {
				t.Fatalf("%s error = %v, want success after the 401 replay", testCase.operation, err)
			}
			if got := auth.refreshCount(); got != 1 {
				t.Fatalf("refresh count = %d, want 1", got)
			}
			mu.Lock()
			defer mu.Unlock()
			if len(authorizations) != 2 {
				t.Fatalf("registry attempts = %d, want 2", len(authorizations))
			}
			if authorizations[0] != rejectedRegistryAuthorization || authorizations[1] != refreshedRegistryAuthorization {
				t.Fatalf("authorizations = %v, want old then new", authorizations)
			}
			if !bytes.Equal(bodies[0], bodies[1]) {
				t.Fatalf("replayed body = %s, want %s", bodies[1], bodies[0])
			}
		})
	}
}

// TestRegistryUnauthorizedReplayOnlyOn401AndOnlyOnceLikeRust mirrors Rust's
// `only_401_retries_and_only_once`: other statuses and a repeated 401 are never
// replayed past the single refresh.
func TestRegistryUnauthorizedReplayOnlyOn401AndOnlyOnceLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		status    int
		attempts  int
		refreshes int
	}{
		{"unauthorized", http.StatusUnauthorized, 2, 1},
		{"forbidden", http.StatusForbidden, 1, 0},
		{"server_error", http.StatusInternalServerError, 1, 0},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var mu sync.Mutex
			attempts := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				mu.Lock()
				attempts++
				mu.Unlock()
				w.WriteHeader(testCase.status)
			}))
			defer server.Close()

			auth := &rotatingRegistryAuth{}
			cfg := RemoteEnvironmentConfig{
				BaseURL:                  server.URL,
				RefreshAfterUnauthorized: auth.RefreshAfterUnauthorized,
				HTTPClient:               server.Client(),
			}
			response, _, err := sendRegistryAuthenticated(context.Background(), cfg, func() (*http.Request, error) {
				return http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL, nil)
			})
			if err != nil {
				t.Fatalf("sendRegistryAuthenticated() error = %v", err)
			}
			defer response.Body.Close()
			if response.StatusCode != testCase.status {
				t.Fatalf("status = %d, want %d", response.StatusCode, testCase.status)
			}
			mu.Lock()
			gotAttempts := attempts
			mu.Unlock()
			if gotAttempts != testCase.attempts {
				t.Fatalf("attempts = %d, want %d", gotAttempts, testCase.attempts)
			}
			if got := auth.refreshCount(); got != testCase.refreshes {
				t.Fatalf("refresh count = %d, want %d", got, testCase.refreshes)
			}
		})
	}
}

// TestRegistryUnauthorizedWithoutReplacementIsNotReplayedLikeRust covers the
// provider reporting that no replacement credential exists: the watchdog must
// not spend a second request on the same rejected credential.
func TestRegistryUnauthorizedWithoutReplacementIsNotReplayedLikeRust(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	cfg := RemoteEnvironmentConfig{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
		RefreshAfterUnauthorized: func(context.Context, http.Header) bool {
			return false
		},
	}
	response, _, err := sendRegistryAuthenticated(context.Background(), cfg, func() (*http.Request, error) {
		return http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL, nil)
	})
	if err != nil {
		t.Fatalf("sendRegistryAuthenticated() error = %v", err)
	}
	defer response.Body.Close()
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
}

// TestRegistryConnectDeadlineCoversAuthRefreshAndReplayLikeRust mirrors Rust's
// `connect_deadline_covers_initial_auth_refresh_and_retry`: a stalled
// credential resolution, refresh, or replay attempt still ends at the connect
// deadline instead of extending it.
func TestRegistryConnectDeadlineCoversAuthRefreshAndReplayLikeRust(t *testing.T) {
	const stageInitial, stageRefresh, stageReplay = "initial", "refresh", "replay"
	for _, stage := range []string{stageInitial, stageRefresh, stageReplay} {
		t.Run(stage, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
			}))
			defer server.Close()

			var mu sync.Mutex
			resolutions := 0
			stalled := func(ctx context.Context, during string) {
				if during != stage {
					return
				}
				<-ctx.Done()
			}
			provider, err := NewRegistryNoiseRendezvousConnectProvider(RemoteEnvironmentConfig{
				BaseURL:       server.URL,
				EnvironmentID: "env",
				HTTPClient:    server.Client(),
				ResolveAuthHeaders: func(ctx context.Context) (http.Header, error) {
					mu.Lock()
					resolutions++
					current := resolutions
					mu.Unlock()
					if current == 1 {
						stalled(ctx, stageInitial)
					} else {
						stalled(ctx, stageReplay)
					}
					if ctx.Err() != nil {
						return nil, ctx.Err()
					}
					return http.Header{"Authorization": []string{rejectedRegistryAuthorization}}, nil
				},
				RefreshAfterUnauthorized: func(ctx context.Context, _ http.Header) bool {
					stalled(ctx, stageRefresh)
					return ctx.Err() == nil
				},
			})
			if err != nil {
				t.Fatalf("NewRegistryNoiseRendezvousConnectProvider() error = %v", err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
			defer cancel()
			started := time.Now()
			_, err = provider.ConnectBundle(ctx, RemotePublicKey{Suite: noiseChannelSuite})
			elapsed := time.Since(started)
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("ConnectBundle() error = %v, want the connect deadline", err)
			}
			if elapsed > 5*time.Second {
				t.Fatalf("ConnectBundle() took %v, want it bounded by the deadline", elapsed)
			}
		})
	}
}

// TestExecutorCredentialRefreshAfterUnauthorizedLikeRust mirrors Rust's
// `ExecutorCredentialProvider::refresh_after_unauthorized` (#55987): the parent
// owns refresh, so a replay is only allowed once a different credential
// arrived.
func TestExecutorCredentialRefreshAfterUnauthorizedLikeRust(t *testing.T) {
	writer, pipe := credentialPipeForTest("env-a")
	expires := time.Now().Unix() + 60
	writeCredentialFrameForTest(t, writer, credentialFrameForTest(t, "env-a", "first-scoped-token", expires))
	if err := pipe.WaitForInitial(context.Background()); err != nil {
		t.Fatalf("WaitForInitial() = %v", err)
	}
	rejected := waitForResolvedBearerForTest(t, pipe, "Bearer first-scoped-token")
	if pipe.RefreshAfterUnauthorized(context.Background(), rejected) {
		t.Fatal("RefreshAfterUnauthorized() = true for an unchanged credential")
	}
	writeCredentialFrameForTest(t, writer, credentialFrameForTest(t, "env-a", "rotated-scoped-token", expires))
	waitForResolvedBearerForTest(t, pipe, "Bearer rotated-scoped-token")
	if !pipe.RefreshAfterUnauthorized(context.Background(), rejected) {
		t.Fatal("RefreshAfterUnauthorized() = false for a parent-supplied replacement")
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close credential writer: %v", err)
	}
	waitForPipeEndForTest(t, pipe)
	if pipe.RefreshAfterUnauthorized(context.Background(), rejected) {
		t.Fatal("RefreshAfterUnauthorized() = true after the credential pipe closed")
	}
}
