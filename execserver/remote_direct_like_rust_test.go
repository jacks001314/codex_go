package execserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

type testDirectSigner struct {
	method       string
	signingURL   string
	signingHost  string
	signingBody  []byte
	signedURL    string
	signedHeader http.Header
	err          error
}

func (s *testDirectSigner) SignRequest(_ context.Context, method string, rawURL string, headers http.Header, body []byte) (string, http.Header, error) {
	s.method = method
	s.signingURL = rawURL
	s.signingHost = headers.Get("Host")
	s.signingBody = append([]byte(nil), body...)
	if s.err != nil {
		return "", nil, s.err
	}
	if s.signedURL == "" && s.signedHeader == nil {
		return rawURL, http.Header{"Authorization": {"AWS4-HMAC-SHA256 signed"}}, nil
	}
	return s.signedURL, s.signedHeader, nil
}

// Mirrors Rust #42781 direct_registration_validates_connection_data.
func TestDirectRegistrationValidatesConnectionDataLikeRust(t *testing.T) {
	var signer *testDirectSigner
	response := directRegistrationResponse{
		EnvironmentID:  "env-direct",
		Transport:      DirectTransport,
		RegistrationID: "registration-1",
		URL:            "ws://127.0.0.1:1/direct",
	}
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/cloud/environment/env-direct/direct/register" {
			http.NotFound(w, r)
			return
		}
		var body directRegistrationRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if body.Transport != DirectTransport {
			t.Errorf("registration transport = %q", body.Transport)
		}
		if got := r.Header.Get("Authorization"); got != "AWS4-HMAC-SHA256 signed" {
			t.Errorf("Authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer registry.Close()

	signer = &testDirectSigner{}
	cfg := RemoteEnvironmentConfig{
		BaseURL:       registry.URL,
		EnvironmentID: "env-direct",
		HTTPClient:    registry.Client(),
		Transport:     RemoteTransportDirect,
		DirectSigner:  signer,
	}
	registered, err := registerDirectEnvironment(context.Background(), cfg)
	if err != nil {
		t.Fatalf("registerDirectEnvironment: %v", err)
	}
	if registered.RegistrationID != "registration-1" || registered.URL != response.URL {
		t.Fatalf("registration = %+v", registered)
	}
	if signer.method != http.MethodPost || signer.signingURL != registry.URL+"/cloud/environment/env-direct/direct/register" {
		t.Fatalf("signer saw %s %s", signer.method, signer.signingURL)
	}
	if !bytes.Contains(signer.signingBody, []byte(DirectTransport)) {
		t.Fatalf("signer body = %q", signer.signingBody)
	}

	for name, mutate := range map[string]func(*directRegistrationResponse){
		"different environment": func(value *directRegistrationResponse) { value.EnvironmentID = "other" },
		"unsupported transport": func(value *directRegistrationResponse) { value.Transport = "noise_v1" },
		"missing registration":  func(value *directRegistrationResponse) { value.RegistrationID = "" },
		"missing url":           func(value *directRegistrationResponse) { value.URL = "" },
		"plain ws":              func(value *directRegistrationResponse) { value.URL = "ws://registry.example.com/direct" },
	} {
		broken := response
		mutate(&broken)
		response = broken
		_, err := registerDirectEnvironment(context.Background(), cfg)
		if err == nil {
			t.Fatalf("%s: expected an error", name)
		}
		response = directRegistrationResponse{
			EnvironmentID:  "env-direct",
			Transport:      DirectTransport,
			RegistrationID: "registration-1",
			URL:            "ws://127.0.0.1:1/direct",
		}
	}
}

// Mirrors Rust #42781 direct_websocket_signs_only_host_and_preserves_handshake_headers
// and direct_websocket_uses_authenticated_url_query_parameters.
func TestDirectWebSocketHandshakeSigningLikeRust(t *testing.T) {
	signer := &testDirectSigner{
		signedURL: "https://registry.example.com/direct?X-Amz-Signature=abc&X-Amz-Security-Token=t",
		signedHeader: http.Header{
			"Authorization": {"AWS4-HMAC-SHA256 signed"},
		},
	}
	headers := http.Header{"Host": {"registry.example.com"}}
	authenticatedURL, signedHeaders, err := authenticateDirectWebSocketRequest(context.Background(), signer, "wss://registry.example.com/direct", headers)
	if err != nil {
		t.Fatalf("authenticateDirectWebSocketRequest: %v", err)
	}
	if signer.method != http.MethodGet || signer.signingURL != "https://registry.example.com/direct" {
		t.Fatalf("signer saw %s %s", signer.method, signer.signingURL)
	}
	if signer.signingHost != "registry.example.com" {
		t.Fatalf("signer host = %q", signer.signingHost)
	}
	if authenticatedURL != "wss://registry.example.com/direct?X-Amz-Signature=abc&X-Amz-Security-Token=t" {
		t.Fatalf("authenticated URL = %q", authenticatedURL)
	}
	if got := signedHeaders.Get("Authorization"); got != "AWS4-HMAC-SHA256 signed" {
		t.Fatalf("signed Authorization = %q", got)
	}

	conflicting := &testDirectSigner{
		signedHeader: http.Header{"Sec-WebSocket-Protocol": {"changed"}},
	}
	if _, _, err := authenticateDirectWebSocketRequest(context.Background(), conflicting, "wss://registry.example.com/direct", headers); err == nil {
		t.Fatal("a signer that rewrites WebSocket handshake headers must fail")
	}
	unsupported := &testDirectSigner{signedURL: "ftp://registry.example.com/direct"}
	if _, _, err := authenticateDirectWebSocketRequest(context.Background(), unsupported, "wss://registry.example.com/direct", headers); err == nil {
		t.Fatal("an unsupported authenticated URL scheme must fail")
	}
}

// Mirrors Rust #42781: the direct transport serves plain exec-server JSON-RPC
// over the authenticated WebSocket and reuses its registration across
// transient disconnects (only a 409 handshake conflict re-registers).
func TestRunDirectEnvironmentServesJSONRPCAndReusesRegistrationLikeRust(t *testing.T) {
	rpcDone := make(chan error, 1)
	var registrations int32
	executor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			select {
			case rpcDone <- err:
			default:
			}
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		request := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientName":"direct-test"}}`)
		if err := conn.Write(ctx, websocket.MessageText, request); err != nil {
			select {
			case rpcDone <- err:
			default:
			}
			return
		}
		_, data, err := conn.Read(ctx)
		if err != nil {
			select {
			case rpcDone <- err:
			default:
			}
			return
		}
		if !bytes.Contains(data, []byte(`"sessionId"`)) {
			select {
			case rpcDone <- errors.New("initialize response missing sessionId: " + string(data)):
			default:
			}
			return
		}
		select {
		case rpcDone <- nil:
		default:
		}
	}))
	defer executor.Close()

	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&registrations, 1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(directRegistrationResponse{
			EnvironmentID:  "env-direct",
			Transport:      DirectTransport,
			RegistrationID: "registration-1",
			URL:            "ws" + strings.TrimPrefix(executor.URL, "http"),
		})
	}))
	defer registry.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErr := make(chan error, 1)
	go func() {
		runErr <- RunRemoteEnvironment(ctx, RemoteEnvironmentConfig{
			BaseURL:       registry.URL,
			EnvironmentID: "env-direct",
			Transport:     RemoteTransportDirect,
			HTTPClient:    registry.Client(),
		})
	}()
	select {
	case err := <-rpcDone:
		if err != nil {
			t.Fatalf("direct JSON-RPC exchange: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("direct executor did not serve the initialize request")
	}
	cancel()
	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("RunRemoteEnvironment: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunRemoteEnvironment did not stop after cancel")
	}
	if got := atomic.LoadInt32(&registrations); got != 1 {
		t.Fatalf("registrations = %d, want a reused registration", got)
	}
}

// Mirrors Rust #42781 direct_websocket_reuses_registration_and_stops_on_permanent_errors.
func TestRunDirectEnvironmentReRegistersOnConflictAndStopsOnPermanentErrorsLikeRust(t *testing.T) {
	var registrations int32
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&registrations, 1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(directRegistrationResponse{
			EnvironmentID:  "env-direct",
			Transport:      DirectTransport,
			RegistrationID: "registration-1",
			URL:            "ws://127.0.0.1:1/direct",
		})
	}))
	defer registry.Close()

	conflict := &http.Response{StatusCode: http.StatusConflict, Status: "409 Conflict", Body: http.NoBody}
	cfg := RemoteEnvironmentConfig{
		BaseURL:       registry.URL,
		EnvironmentID: "env-direct",
		Transport:     RemoteTransportDirect,
		HTTPClient:    registry.Client(),
		Dial: func(context.Context, string, *websocket.DialOptions) (*websocket.Conn, *http.Response, error) {
			if atomic.LoadInt32(&registrations) == 1 {
				return nil, conflict, errors.New("handshake conflict")
			}
			return nil, nil, errors.New("connection refused")
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- RunRemoteEnvironment(ctx, cfg) }()
	deadline := time.Now().Add(5 * time.Second)
	for atomic.LoadInt32(&registrations) < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RunRemoteEnvironment did not stop after cancel")
	}
	if got := atomic.LoadInt32(&registrations); got != 2 {
		t.Fatalf("registrations = %d, want a refreshed registration after 409", got)
	}

	permanent := &http.Response{StatusCode: http.StatusUnauthorized, Status: "401 Unauthorized", Body: http.NoBody}
	cfg.Dial = func(context.Context, string, *websocket.DialOptions) (*websocket.Conn, *http.Response, error) {
		return nil, permanent, errors.New("unauthorized handshake")
	}
	if err := RunRemoteEnvironment(context.Background(), cfg); err == nil {
		t.Fatal("a permanent handshake rejection must terminate the direct transport")
	}
}

func TestDirectConnectRetryabilityLikeRust(t *testing.T) {
	for status, want := range map[int]bool{
		0:                              true,
		http.StatusConflict:            true,
		http.StatusRequestTimeout:      true,
		http.StatusTooManyRequests:     true,
		http.StatusBadRequest:          false,
		http.StatusUnauthorized:        false,
		http.StatusInternalServerError: true,
		http.StatusBadGateway:          true,
	} {
		if got := directConnectRetryable(status); got != want {
			t.Errorf("directConnectRetryable(%d) = %v, want %v", status, got, want)
		}
	}
	if err := requireSecureOrLoopback("wss://registry.example.com/direct", "wss"); err != nil {
		t.Errorf("wss endpoint rejected: %v", err)
	}
	if err := requireSecureOrLoopback("ws://127.0.0.1:7777/direct", "wss"); err != nil {
		t.Errorf("loopback ws endpoint rejected: %v", err)
	}
	if err := requireSecureOrLoopback("ws://registry.example.com/direct", "wss"); err == nil {
		t.Error("plain ws on a non-loopback endpoint must be rejected")
	}
}
