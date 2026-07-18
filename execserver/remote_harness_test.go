package execserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestDialNoiseRendezvousClientRefreshesUnauthorizedBundleAndRunsEncryptedRPCLikeRust(t *testing.T) {
	executorIdentity, err := generateRemoteNoiseIdentity()
	if err != nil {
		t.Fatalf("generate executor identity: %v", err)
	}
	defer executorIdentity.Destroy()
	executorServer := NewServer()
	defer executorServer.shutdownSessions()

	unauthorized := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer unauthorized.Close()
	unauthorizedURL := "ws" + strings.TrimPrefix(unauthorized.URL, "http")

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
			RemoteEnvironmentConfig{
				BaseURL:       registry.URL,
				EnvironmentID: remoteRelayTestEnvironmentID,
				AuthHeaders:   http.Header{"Authorization": []string{"Bearer registry-token"}},
				HTTPClient:    registry.Client(),
			},
			&remoteRegistrationResponse{
				EnvironmentID:          remoteRelayTestEnvironmentID,
				ExecutorRegistrationID: remoteRelayTestRegistrationID,
			},
			executorIdentity,
		)
	}))
	defer relay.Close()
	relayURL := "ws" + strings.TrimPrefix(relay.URL, "http")

	var mu sync.Mutex
	var connectKeys []RemotePublicKey
	validationSeen := false
	registry = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer registry-token" {
			http.Error(w, "missing auth", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/cloud/environment/environment-1/connect":
			var body remoteConnectRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, "bad connect body", http.StatusBadRequest)
				return
			}
			mu.Lock()
			connectKeys = append(connectKeys, body.HarnessPublicKey)
			attempt := len(connectKeys)
			mu.Unlock()
			url := relayURL
			if attempt == 1 {
				url = unauthorizedURL
			}
			_ = json.NewEncoder(w).Encode(remoteConnectResponse{
				EnvironmentID:           remoteRelayTestEnvironmentID,
				URL:                     url,
				SecurityProfile:         RemoteSecurityProfile,
				ExecutorRegistrationID:  remoteRelayTestRegistrationID,
				ExecutorPublicKey:       executorIdentity.PublicKey(),
				HarnessKeyAuthorization: "authorization-1",
			})
		case "/cloud/environment/environment-1/validate":
			var body remoteHarnessValidationRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, "bad validation body", http.StatusBadRequest)
				return
			}
			mu.Lock()
			if len(connectKeys) == 2 && body.HarnessPublicKey == connectKeys[1] && body.HarnessKeyAuthorization == "authorization-1" && body.ExecutorRegistrationID == remoteRelayTestRegistrationID {
				validationSeen = true
			}
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(remoteHarnessValidationResponse{Valid: true})
		default:
			http.NotFound(w, r)
		}
	}))
	defer registry.Close()

	provider, err := NewRegistryNoiseRendezvousConnectProvider(RemoteEnvironmentConfig{
		BaseURL:       registry.URL,
		EnvironmentID: remoteRelayTestEnvironmentID,
		AuthHeaders:   http.Header{"Authorization": []string{"Bearer registry-token"}},
		HTTPClient:    registry.Client(),
	})
	if err != nil {
		t.Fatalf("NewRegistryNoiseRendezvousConnectProvider() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := DialNoiseRendezvousClient(ctx, provider, DialClientOptions{ClientName: "noise-harness-test"})
	if err != nil {
		t.Fatalf("DialNoiseRendezvousClient() error = %v", err)
	}
	info, err := client.EnvironmentInfo(ctx)
	if err != nil {
		_ = client.Close()
		t.Fatalf("EnvironmentInfo() error = %v", err)
	}
	if info.Shell.Name == "" || info.Shell.Path == "" {
		_ = client.Close()
		t.Fatalf("environment info = %#v", info)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("Client.Close() error = %v", err)
	}

	mu.Lock()
	keys := append([]RemotePublicKey(nil), connectKeys...)
	validated := validationSeen
	mu.Unlock()
	if len(keys) != 2 {
		t.Fatalf("connect bundle requests = %d, want 2", len(keys))
	}
	if keys[0] != keys[1] {
		t.Fatalf("harness identity changed after 401: %#v != %#v", keys[0], keys[1])
	}
	if !validated {
		t.Fatal("executor validation did not bind the refreshed harness key and authorization")
	}
	select {
	case err := <-relayDone:
		if err != nil {
			t.Fatalf("relay returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("relay did not stop after client close")
	}
}

func TestRegistryNoiseRendezvousProviderRequiresCompleteBundleLikeRust(t *testing.T) {
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(remoteConnectResponse{
			EnvironmentID:   remoteRelayTestEnvironmentID,
			SecurityProfile: RemoteSecurityProfile,
		})
	}))
	defer registry.Close()
	provider, err := NewRegistryNoiseRendezvousConnectProvider(RemoteEnvironmentConfig{
		BaseURL: registry.URL, EnvironmentID: remoteRelayTestEnvironmentID, HTTPClient: registry.Client(),
	})
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	if _, err := provider.ConnectBundle(context.Background(), RemotePublicKey{Suite: noiseChannelSuite}); err == nil || !strings.Contains(err.Error(), "incomplete Noise connection data") {
		t.Fatalf("ConnectBundle() error = %v", err)
	}
}

func TestNoiseRendezvousClientRecoveryFetchesFreshBundleAndResumesSessionLikeRust(t *testing.T) {
	executorIdentity, err := generateRemoteNoiseIdentity()
	if err != nil {
		t.Fatalf("generate executor identity: %v", err)
	}
	defer executorIdentity.Destroy()
	executorServer := NewServer()
	executorServer.detachedSessionTTL = time.Minute
	defer executorServer.shutdownSessions()

	var registry *httptest.Server
	accepted := make(chan *websocket.Conn, 2)
	relayDone := make(chan error, 2)
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			relayDone <- err
			return
		}
		accepted <- conn
		relayDone <- executorServer.serveNoiseRelayConnection(
			r.Context(),
			conn,
			RemoteEnvironmentConfig{
				BaseURL:       registry.URL,
				EnvironmentID: remoteRelayTestEnvironmentID,
				HTTPClient:    registry.Client(),
			},
			&remoteRegistrationResponse{
				EnvironmentID:          remoteRelayTestEnvironmentID,
				ExecutorRegistrationID: remoteRelayTestRegistrationID,
			},
			executorIdentity,
		)
	}))
	defer relay.Close()
	relayURL := "ws" + strings.TrimPrefix(relay.URL, "http")

	var mu sync.Mutex
	var connectKeys []RemotePublicKey
	registry = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cloud/environment/environment-1/connect":
			var body remoteConnectRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, "bad connect body", http.StatusBadRequest)
				return
			}
			mu.Lock()
			connectKeys = append(connectKeys, body.HarnessPublicKey)
			attempt := len(connectKeys)
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(remoteConnectResponse{
				EnvironmentID:           remoteRelayTestEnvironmentID,
				URL:                     relayURL,
				SecurityProfile:         RemoteSecurityProfile,
				ExecutorRegistrationID:  remoteRelayTestRegistrationID,
				ExecutorPublicKey:       executorIdentity.PublicKey(),
				HarnessKeyAuthorization: "authorization-" + string(rune('0'+attempt)),
			})
		case "/cloud/environment/environment-1/validate":
			_ = json.NewEncoder(w).Encode(remoteHarnessValidationResponse{Valid: true})
		default:
			http.NotFound(w, r)
		}
	}))
	defer registry.Close()
	provider, err := NewRegistryNoiseRendezvousConnectProvider(RemoteEnvironmentConfig{
		BaseURL: registry.URL, EnvironmentID: remoteRelayTestEnvironmentID, HTTPClient: registry.Client(),
	})
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client, err := DialNoiseRendezvousClient(ctx, provider, DialClientOptions{ClientName: "noise-recovery-test"})
	if err != nil {
		t.Fatalf("DialNoiseRendezvousClient() error = %v", err)
	}
	defer client.Close()
	firstSessionID := client.SessionID()
	if _, err := client.EnvironmentInfo(ctx); err != nil {
		t.Fatalf("first EnvironmentInfo() error = %v", err)
	}
	var firstConn *websocket.Conn
	select {
	case firstConn = <-accepted:
	case <-time.After(5 * time.Second):
		t.Fatal("first relay connection was not accepted")
	}
	if err := firstConn.CloseNow(); err != nil {
		t.Fatalf("close first relay connection: %v", err)
	}
	select {
	case <-accepted:
	case <-time.After(10 * time.Second):
		t.Fatal("recovered relay connection was not accepted")
	}
	if _, err := client.EnvironmentInfo(ctx); err != nil {
		t.Fatalf("EnvironmentInfo() after recovery error = %v", err)
	}
	if client.SessionID() != firstSessionID {
		t.Fatalf("recovery session id = %q, want %q", client.SessionID(), firstSessionID)
	}
	mu.Lock()
	keys := append([]RemotePublicKey(nil), connectKeys...)
	mu.Unlock()
	if len(keys) < 2 {
		t.Fatalf("connect bundle requests = %d, want at least 2", len(keys))
	}
	if keys[0] != keys[1] {
		t.Fatalf("harness identity changed during recovery: %#v != %#v", keys[0], keys[1])
	}
}

func TestDecodeRemoteNoisePublicKeyRejectsUnknownSuiteLikeRust(t *testing.T) {
	identity, err := generateRemoteNoiseIdentity()
	if err != nil {
		t.Fatalf("generate identity: %v", err)
	}
	defer identity.Destroy()
	key := identity.PublicKey()
	key.Suite = "unknown"
	if _, _, err := decodeRemoteNoisePublicKey(key); err == nil {
		t.Fatal("unknown Noise suite accepted")
	}
}

func TestRegistryNoiseRendezvousConnectProviderFromValuesMatchesRustEnvironmentRules(t *testing.T) {
	if provider, configured, err := registryNoiseRendezvousConnectProviderFromValues(nil, nil, nil, nil); err != nil || configured || provider != nil {
		t.Fatalf("empty values = %#v, %v, %v", provider, configured, err)
	}
	registryURL := "https://registry.example"
	environmentID := "environment-1"
	token := "registry-token"
	accountID := "workspace-123"
	if _, configured, err := registryNoiseRendezvousConnectProviderFromValues(&registryURL, nil, &token, nil); err == nil || configured || !strings.Contains(err.Error(), CodexExecServerNoiseEnvironmentIDEnvVar) {
		t.Fatalf("partial values error = %v, configured = %v", err, configured)
	}
	provider, configured, err := registryNoiseRendezvousConnectProviderFromValues(&registryURL, &environmentID, &token, &accountID)
	if err != nil || !configured || provider == nil {
		t.Fatalf("complete values = %#v, %v, %v", provider, configured, err)
	}
	registryProvider, ok := provider.(*registryNoiseRendezvousConnectProvider)
	if !ok {
		t.Fatalf("provider type = %T", provider)
	}
	if got := registryProvider.config.AuthHeaders.Get("Authorization"); got != "Bearer registry-token" {
		t.Fatalf("Authorization = %q", got)
	}
	if got := registryProvider.config.AuthHeaders.Get("ChatGPT-Account-ID"); got != "workspace-123" {
		t.Fatalf("ChatGPT-Account-ID = %q", got)
	}
	invalidToken := "bad\ntoken"
	if _, _, err := registryNoiseRendezvousConnectProviderFromValues(&registryURL, &environmentID, &invalidToken, nil); err == nil || !strings.Contains(err.Error(), "bearer token is not a valid HTTP header") {
		t.Fatalf("invalid token error = %v", err)
	}
	invalidAccount := "bad\raccount"
	if _, _, err := registryNoiseRendezvousConnectProviderFromValues(&registryURL, &environmentID, &token, &invalidAccount); err == nil || !strings.Contains(err.Error(), "account id is not a valid HTTP header") {
		t.Fatalf("invalid account error = %v", err)
	}
}

func TestRegistryNoiseRendezvousConnectProviderFromEnvTrimsAndOmitsEmptyAccountLikeRust(t *testing.T) {
	t.Setenv(CodexExecServerNoiseRegistryURLEnvVar, " https://registry.example/ ")
	t.Setenv(CodexExecServerNoiseEnvironmentIDEnvVar, " environment-1 ")
	t.Setenv(CodexExecServerNoiseAuthTokenEnvVar, " registry-token ")
	t.Setenv(CodexExecServerNoiseChatGPTAccountIDEnvVar, "   ")
	provider, configured, err := RegistryNoiseRendezvousConnectProviderFromEnv()
	if err != nil || !configured {
		t.Fatalf("provider from env = %#v, %v, %v", provider, configured, err)
	}
	registryProvider := provider.(*registryNoiseRendezvousConnectProvider)
	if registryProvider.config.BaseURL != "https://registry.example" || registryProvider.config.EnvironmentID != "environment-1" {
		t.Fatalf("provider config = %#v", registryProvider.config)
	}
	if got := registryProvider.config.AuthHeaders.Get("ChatGPT-Account-ID"); got != "" {
		t.Fatalf("ChatGPT-Account-ID = %q", got)
	}
}
