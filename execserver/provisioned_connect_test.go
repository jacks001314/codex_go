package execserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// Rust #48575 (`985cf47a4e`) "Allow provisioned executors more time to come
// online": a provisioned executor can still be resuming after readiness is
// reported, so the initial Noise rendezvous connection retries
// `environment_offline` responses until a fixed five-minute deadline instead of
// the ordinary four-attempt / fourteen-second window. These tests port the
// Rust client_transport_tests.rs / client_refresh_tests.rs cases and compress
// the retry windows (they are vars) so the same branches run without Rust's
// paused clock.

// scriptedNoiseProvider injects a scripted sequence of registry failures ahead
// of a delegate provider, mirroring Rust's SequenceNoiseConnectProvider.
type scriptedNoiseProvider struct {
	mu       sync.Mutex
	calls    int
	script   []error
	delegate NoiseRendezvousConnectProvider
}

func (p *scriptedNoiseProvider) ConnectBundle(ctx context.Context, key RemotePublicKey) (*NoiseRendezvousConnectBundle, error) {
	p.mu.Lock()
	p.calls++
	call := p.calls
	var next error
	if call <= len(p.script) {
		next = p.script[call-1]
	}
	delegate := p.delegate
	p.mu.Unlock()
	if next != nil {
		return nil, next
	}
	if delegate == nil {
		return nil, errors.New("scripted registry provider exhausted")
	}
	return delegate.ConnectBundle(ctx, key)
}

func (p *scriptedNoiseProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// registryConflictError builds the registry's HTTP error shape the recovery
// classifier consumes (Rust ExecServerError::EnvironmentRegistryHttp).
func registryConflictError(status int, code string) error {
	value := code
	return &remoteRegistryHTTPError{StatusCode: status, Code: &value, message: code}
}

func offlineRegistryErrors(count int) []error {
	out := make([]error, 0, count)
	for i := 0; i < count; i++ {
		out = append(out, registryConflictError(http.StatusConflict, "environment_offline"))
	}
	return out
}

// compressRegistryWindows shrinks the rendezvous retry windows for one test.
func compressRegistryWindows(t *testing.T, operation, provisioning time.Duration) {
	t.Helper()
	prevMax := initialRegistryMaxRetries
	prevRequest := initialRegistryRequestTimeout
	prevOperation := initialRegistryOperationTimeout
	prevInitial := registryRecoveryInitialRetryMillis
	prevMaxInterval := registryRecoveryMaxRetryInterval
	prevProvisioned := provisionedEnvironmentConnectTimeout
	initialRegistryRequestTimeout = 20 * time.Millisecond
	initialRegistryOperationTimeout = operation
	registryRecoveryInitialRetryMillis = 1
	registryRecoveryMaxRetryInterval = 2 * time.Millisecond
	provisionedEnvironmentConnectTimeout = provisioning
	t.Cleanup(func() {
		initialRegistryMaxRetries = prevMax
		initialRegistryRequestTimeout = prevRequest
		initialRegistryOperationTimeout = prevOperation
		registryRecoveryInitialRetryMillis = prevInitial
		registryRecoveryMaxRetryInterval = prevMaxInterval
		provisionedEnvironmentConnectTimeout = prevProvisioned
	})
}

// startRelayRegistryProvider stands up the executor relay, registry, and
// registry-backed provider used by the success cases (same shape as
// TestNoiseRendezvousClientRecoveryFetchesFreshBundleAndResumesSessionLikeRust).
func startRelayRegistryProvider(t *testing.T) NoiseRendezvousConnectProvider {
	t.Helper()
	executorIdentity, err := generateRemoteNoiseIdentity()
	if err != nil {
		t.Fatalf("generate executor identity: %v", err)
	}
	t.Cleanup(executorIdentity.Destroy)
	executorServer := NewServer()
	t.Cleanup(executorServer.shutdownSessions)

	var registry *httptest.Server
	relayDone := make(chan error, 1)
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			relayDone <- err
			return
		}
		relayDone <- executorServer.serveNoiseRelayConnection(
			r.Context(),
			conn,
			RemoteEnvironmentConfig{BaseURL: registry.URL, EnvironmentID: remoteRelayTestEnvironmentID, HTTPClient: registry.Client()},
			&remoteRegistrationResponse{EnvironmentID: remoteRelayTestEnvironmentID, ExecutorRegistrationID: remoteRelayTestRegistrationID},
			executorIdentity,
		)
	}))
	t.Cleanup(relay.Close)
	relayURL := "ws" + strings.TrimPrefix(relay.URL, "http")

	registry = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cloud/environment/" + remoteRelayTestEnvironmentID + "/connect":
			var body remoteConnectRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, "bad connect body", http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(remoteConnectResponse{
				EnvironmentID:           remoteRelayTestEnvironmentID,
				URL:                     relayURL,
				SecurityProfile:         RemoteSecurityProfile,
				ExecutorRegistrationID:  remoteRelayTestRegistrationID,
				ExecutorPublicKey:       executorIdentity.PublicKey(),
				HarnessKeyAuthorization: "authorization-1",
			})
		case "/cloud/environment/" + remoteRelayTestEnvironmentID + "/validate":
			_ = json.NewEncoder(w).Encode(remoteHarnessValidationResponse{Valid: true})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(registry.Close)

	provider, err := NewRegistryNoiseRendezvousConnectProvider(RemoteEnvironmentConfig{
		BaseURL: registry.URL, EnvironmentID: remoteRelayTestEnvironmentID, HTTPClient: registry.Client(),
	})
	if err != nil {
		t.Fatalf("NewRegistryNoiseRendezvousConnectProvider() error = %v", err)
	}
	return provider
}

// TestProvisionedNoiseConnectWaitsForOfflineExecutorLikeRust ports Rust
// `provisioned_environment_waits_for_offline_executor_on_the_same_handle`
// (client_refresh_tests.rs): an executor that stays offline past the ordinary
// retry count still connects once it comes online, and the non-provisioned
// transport stays bounded by the ordinary window.
func TestProvisionedNoiseConnectWaitsForOfflineExecutorLikeRust(t *testing.T) {
	compressRegistryWindows(t, 100*time.Millisecond, 3*time.Second)
	realProvider := startRelayRegistryProvider(t)

	const offlineCalls = 8 // more than initialRegistryMaxRetries
	provisioned := &scriptedNoiseProvider{script: offlineRegistryErrors(offlineCalls), delegate: realProvider}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	client, err := DialNoiseRendezvousClient(ctx, provisioned, DialClientOptions{ClientName: "provisioned-wait-test", Provisioned: true})
	if err != nil {
		t.Fatalf("provisioned DialNoiseRendezvousClient() error = %v, want success once the executor is online", err)
	}
	defer client.Close()
	if got := provisioned.callCount(); got != offlineCalls+1 {
		t.Fatalf("provisioned registry calls = %d, want %d", got, offlineCalls+1)
	}
	if got := provisioned.callCount(); got <= initialRegistryMaxRetries+1 {
		t.Fatalf("provisioned registry calls = %d, want more than the ordinary cap %d", got, initialRegistryMaxRetries+1)
	}
	if _, err := client.EnvironmentInfo(ctx); err != nil {
		t.Fatalf("EnvironmentInfo() after provisioning wait error = %v", err)
	}

	// Reverse control: the exact same script without the provisioning flag is
	// bounded by the ordinary retry count and never reaches the executor.
	ordinary := &scriptedNoiseProvider{script: offlineRegistryErrors(offlineCalls), delegate: realProvider}
	if _, err := DialNoiseRendezvousClient(ctx, ordinary, DialClientOptions{ClientName: "ordinary-wait-test"}); err == nil {
		t.Fatal("non-provisioned DialNoiseRendezvousClient() succeeded, want the ordinary retry limit to stop it")
	} else if !isEnvironmentOfflineError(err) {
		t.Fatalf("non-provisioned error = %v, want environment_offline", err)
	}
	if got := ordinary.callCount(); got != initialRegistryMaxRetries+1 {
		t.Fatalf("ordinary registry calls = %d, want the bounded %d", got, initialRegistryMaxRetries+1)
	}
}

// TestProvisionedNoiseConnectBoundsOfflineRetriesAtTheDeadlineLikeRust ports
// Rust `provisioned_noise_connection_bounds_offline_retries`.
func TestProvisionedNoiseConnectBoundsOfflineRetriesAtTheDeadlineLikeRust(t *testing.T) {
	const provisioningWindow = 150 * time.Millisecond
	compressRegistryWindows(t, 30*time.Millisecond, provisioningWindow)

	provider := &scriptedNoiseProvider{script: offlineRegistryErrors(4096)}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	started := time.Now()
	_, err := DialNoiseRendezvousClient(ctx, provider, DialClientOptions{ClientName: "provisioned-offline-test", Provisioned: true})
	elapsed := time.Since(started)
	if err == nil {
		t.Fatal("an executor that stays offline must time out")
	}
	if !isEnvironmentOfflineError(err) {
		t.Fatalf("error = %v, want environment_offline", err)
	}
	if elapsed < provisioningWindow {
		t.Fatalf("provisioned connect returned after %v, before the %v provisioning deadline", elapsed, provisioningWindow)
	}
	if got := provider.callCount(); got <= initialRegistryMaxRetries+1 {
		t.Fatalf("provisioned registry calls = %d, want more than the ordinary cap %d", got, initialRegistryMaxRetries+1)
	}
}

// TestProvisionedNoiseConnectKeepsOrdinaryLimitsForOtherRegistryErrorsLikeRust
// ports Rust `provisioned_noise_connection_keeps_other_registry_retry_limits`.
func TestProvisionedNoiseConnectKeepsOrdinaryLimitsForOtherRegistryErrorsLikeRust(t *testing.T) {
	compressRegistryWindows(t, 120*time.Millisecond, 5*time.Second)

	script := make([]error, 0, 12)
	for i := 0; i < 12; i++ {
		script = append(script, registryConflictError(http.StatusServiceUnavailable, "unavailable"))
	}
	provider := &scriptedNoiseProvider{script: script}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	started := time.Now()
	_, err := DialNoiseRendezvousClient(ctx, provider, DialClientOptions{ClientName: "provisioned-503-test", Provisioned: true})
	elapsed := time.Since(started)
	if err == nil {
		t.Fatal("a retryable non-offline registry error must still fail")
	}
	var statusErr *remoteRegistryHTTPError
	if !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("error = %v, want a 503 registry error", err)
	}
	if got := provider.callCount(); got != initialRegistryMaxRetries+1 {
		t.Fatalf("registry calls = %d, want the ordinary cap %d", got, initialRegistryMaxRetries+1)
	}
	if elapsed > initialRegistryOperationTimeout {
		t.Fatalf("elapsed = %v, want <= the ordinary operation timeout %v", elapsed, initialRegistryOperationTimeout)
	}
}

// TestProvisionedNoiseConnectStopsOnPermanentRegistryErrorLikeRust ports Rust
// `provisioned_noise_connection_stops_on_a_late_permanent_error`.
func TestProvisionedNoiseConnectStopsOnPermanentRegistryErrorLikeRust(t *testing.T) {
	compressRegistryWindows(t, 100*time.Millisecond, 5*time.Second)

	script := offlineRegistryErrors(10)
	script = append(script, registryConflictError(http.StatusForbidden, "forbidden"))
	provider := &scriptedNoiseProvider{script: script}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	_, err := DialNoiseRendezvousClient(ctx, provider, DialClientOptions{ClientName: "provisioned-permanent-test", Provisioned: true})
	if err == nil {
		t.Fatal("a permanent registry error must stop provisioning retries")
	}
	var statusErr *remoteRegistryHTTPError
	if !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusForbidden {
		t.Fatalf("error = %v, want a 403 registry error", err)
	}
	if got := provider.callCount(); got != 11 {
		t.Fatalf("registry calls = %d, want 11 (ten offline then the permanent error)", got)
	}
}
