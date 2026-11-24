package execserver

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	clatter "github.com/shurlinet/go-clatter"
)

type inboundNoiseTestFixture struct {
	address  string
	identity *remoteNoiseIdentity
	control  *InboundNoiseListenerControl
	done     chan error
	cancel   context.CancelFunc
	registry *httptest.Server
}

func newInboundNoiseTestFixture(t *testing.T, validation http.Handler) *inboundNoiseTestFixture {
	t.Helper()
	if validation == nil {
		validation = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(remoteHarnessValidationResponse{Valid: true})
		})
	}
	registry := httptest.NewServer(validation)
	identity, err := generateRemoteNoiseIdentity()
	if err != nil {
		registry.Close()
		t.Fatalf("generateRemoteNoiseIdentity() error = %v", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		identity.Destroy()
		registry.Close()
		t.Fatalf("listen: %v", err)
	}
	control := NewInboundNoiseListenerControl()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- NewServer().ServeInboundNoiseWebSocketListener(
			ctx,
			listener,
			RemoteEnvironmentConfig{
				BaseURL:       registry.URL,
				EnvironmentID: "env-remote",
				HTTPClient:    registry.Client(),
			},
			remoteRegistrationResponse{
				EnvironmentID:          "env-remote",
				ExecutorRegistrationID: "registration-1",
			},
			identity,
			control,
		)
	}()
	fixture := &inboundNoiseTestFixture{
		address:  listener.Addr().String(),
		identity: identity,
		control:  control,
		done:     done,
		cancel:   cancel,
		registry: registry,
	}
	t.Cleanup(func() {
		cancel()
		control.Stop()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("inbound listener stopped with %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Errorf("inbound listener did not stop")
		}
		identity.Destroy()
		registry.Close()
	})
	return fixture
}

func (f *inboundNoiseTestFixture) dial(t *testing.T) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws://"+f.address, nil)
	if err != nil {
		t.Fatalf("dial inbound listener: %v", err)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })
	return conn
}

// Rust #62325: an inbound WebSocket reaches the same environment RPC surface
// once the registry authorizes the harness, and the exchange stays encrypted.
func TestInboundNoiseListenerServesAuthorizedHarnessLikeRust(t *testing.T) {
	validations := make(chan remoteHarnessValidationRequest, 1)
	fixture := newInboundNoiseTestFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body remoteHarnessValidationRequest
		_ = json.NewDecoder(r.Body).Decode(&body)
		validations <- body
		_ = json.NewEncoder(w).Encode(remoteHarnessValidationResponse{Valid: true})
	}))
	harnessIdentity, err := generateRemoteNoiseIdentity()
	if err != nil {
		t.Fatalf("generate harness identity: %v", err)
	}
	defer harnessIdentity.Destroy()
	conn := fixture.dial(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	transport, err := completeRemoteTestHarnessHandshake(ctx, conn, harnessIdentity, fixture.identity.PublicKey())
	if err != nil {
		t.Fatalf("harness handshake: %v", err)
	}
	defer transport.Destroy()
	select {
	case body := <-validations:
		if body.ExecutorRegistrationID != "registration-1" || body.HarnessPublicKey != harnessIdentity.PublicKey() {
			t.Fatalf("validation request = %#v", body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("inbound listener did not validate the harness key")
	}

	request := []byte(`{"id":1,"method":"initialize","params":{"clientName":"inbound-test"}}`)
	framed := make([]byte, 4, len(request)+4)
	binary.BigEndian.PutUint32(framed, uint32(len(request)))
	framed = append(framed, request...)
	ciphertext := make([]byte, len(framed)+clatter.TagLen)
	ciphertextLen, err := transport.Send(framed, ciphertext)
	if err != nil {
		t.Fatalf("encrypt request: %v", err)
	}
	encoded, err := encodeRelayMessageFrame(newRelayDataFrame("stream-1", 0, ciphertext[:ciphertextLen]))
	if err != nil {
		t.Fatalf("encode request frame: %v", err)
	}
	if err := conn.Write(ctx, websocket.MessageBinary, encoded); err != nil {
		t.Fatalf("write request frame: %v", err)
	}
	response, err := readRemoteTestJSONRPC(ctx, conn, transport)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if !strings.Contains(string(response), `"id":1`) || !strings.Contains(string(response), `"sessionId"`) {
		t.Fatalf("response = %s, want the initialize result", response)
	}
}

// Rust #62325: a registry-denied harness is reset instead of served.
func TestInboundNoiseListenerRejectsDeniedHarnessLikeRust(t *testing.T) {
	fixture := newInboundNoiseTestFixture(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(remoteHarnessValidationResponse{Valid: false})
	}))
	harnessIdentity, err := generateRemoteNoiseIdentity()
	if err != nil {
		t.Fatalf("generate harness identity: %v", err)
	}
	defer harnessIdentity.Destroy()
	conn := fixture.dial(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if transport, err := completeRemoteTestHarnessHandshake(ctx, conn, harnessIdentity, fixture.identity.PublicKey()); err == nil {
		transport.Destroy()
		t.Fatal("registry-denied harness completed the inbound handshake")
	}
}

// Rust #62325: the inbound listener never answers a plaintext JSON-RPC frame.
func TestInboundNoiseListenerKeepsPlaintextOffTheWireLikeRust(t *testing.T) {
	fixture := newInboundNoiseTestFixture(t, nil)
	conn := fixture.dial(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	plaintext := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientName":"plaintext"}}`)
	if err := conn.Write(ctx, websocket.MessageText, plaintext); err != nil {
		t.Fatalf("write plaintext request: %v", err)
	}
	readCtx, readCancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer readCancel()
	messageType, payload, err := conn.Read(readCtx)
	if err == nil {
		t.Fatalf("plaintext upgrade answered with %d %s", messageType, payload)
	}
	if !strings.Contains(err.Error(), "deadline") && websocket.CloseStatus(err) == -1 {
		t.Fatalf("plaintext read error = %v, want a closed or idle socket", err)
	}
}

// Rust #62325: shutting the listener down drains live sockets.
func TestInboundNoiseListenerDrainsConnectionsOnStopLikeRust(t *testing.T) {
	fixture := newInboundNoiseTestFixture(t, nil)
	conn := fixture.dial(t)
	deadline := time.Now().Add(5 * time.Second)
	for fixture.control.ActiveConnections() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := fixture.control.ActiveConnections(); got != 1 {
		t.Fatalf("active connections = %d, want 1", got)
	}
	fixture.control.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, _, err := conn.Read(ctx); err == nil {
		t.Fatal("stopped listener kept the client socket open")
	}
	select {
	case err := <-fixture.done:
		if err != nil {
			t.Fatalf("listener stopped with %v", err)
		}
		fixture.done <- nil
	case <-time.After(5 * time.Second):
		t.Fatal("listener did not return after Stop")
	}
}

// Rust #62325 admission.rs: the budget bounds concurrent handshakes and the
// handshake start rate, then recovers once the window passes.
func TestInboundNoiseHandshakeBudgetLikeRust(t *testing.T) {
	budget := newNoiseHandshakeBudget()
	now := time.Unix(1000, 0)
	budget.now = func() time.Time { return now }

	releases := make([]func(), 0, inboundNoiseMaxHandshakes)
	for index := 0; index < inboundNoiseMaxHandshakes; index++ {
		release, ok := budget.tryAdmit()
		if !ok {
			t.Fatalf("handshake %d refused below the concurrency limit", index)
		}
		releases = append(releases, release)
	}
	if _, ok := budget.tryAdmit(); ok {
		t.Fatal("handshake admitted past the concurrency limit")
	}
	for _, release := range releases {
		release()
	}
	for index := inboundNoiseMaxHandshakes; index < inboundNoiseHandshakesPerSecond; index++ {
		release, ok := budget.tryAdmit()
		if !ok {
			t.Fatalf("handshake %d refused below the window limit", index)
		}
		release()
	}
	if _, ok := budget.tryAdmit(); ok {
		t.Fatal("handshake admitted past the per-second limit")
	}
	now = now.Add(inboundNoiseHandshakeWindow)
	release, ok := budget.tryAdmit()
	if !ok {
		t.Fatal("handshake refused after the window passed")
	}
	release()
	release()
	if got := len(budget.permits); got != inboundNoiseMaxHandshakes {
		t.Fatalf("available permits = %d, want %d after every release", got, inboundNoiseMaxHandshakes)
	}
}

// The listener refuses more than its fixed number of live connections.
func TestInboundNoiseListenerBoundsConnectionsLikeRust(t *testing.T) {
	control := NewInboundNoiseListenerControl()
	for index := 0; index < inboundNoiseMaxConnections; index++ {
		if !control.tryAcquire() {
			t.Fatalf("connection %d refused below the limit", index)
		}
	}
	if control.tryAcquire() {
		t.Fatal("connection admitted past the limit")
	}
	control.release()
	if !control.tryAcquire() {
		t.Fatal("refused connection after a permit returned")
	}
}

// completeInboundHarnessHandshake performs the harness side of the inbound
// Noise handshake with explicit environment and registration identities.
func completeInboundHarnessHandshake(
	ctx context.Context,
	conn *websocket.Conn,
	identity *remoteNoiseIdentity,
	executorKey RemotePublicKey,
	prologue []byte,
) (*clatter.TransportState, error) {
	executorDH, err := base64.StdEncoding.DecodeString(executorKey.X25519PublicKey)
	if err != nil {
		return nil, err
	}
	executorKEM, err := base64.StdEncoding.DecodeString(executorKey.MLKEM768PublicKey)
	if err != nil {
		return nil, err
	}
	handshake, err := clatter.NewHybridHandshake(
		clatter.PatternHybridIK,
		true,
		remoteNoiseCipherSuite(),
		clatter.WithStaticKey(identity.dh),
		clatter.WithStaticKEMKey(identity.kem),
		clatter.WithRemoteStatic(executorDH),
		clatter.WithRemoteStaticKEMKey(executorKEM),
		clatter.WithPrologue(prologue),
	)
	if err != nil {
		return nil, err
	}
	request := make([]byte, clatter.MaxMessageLen)
	requestLen, err := handshake.WriteMessage([]byte("inbound-authorization"), request)
	if err != nil {
		handshake.Destroy()
		return nil, err
	}
	encoded, err := encodeRelayMessageFrame(newRelayHandshakeFrame("stream-1", request[:requestLen]))
	if err != nil {
		handshake.Destroy()
		return nil, err
	}
	if err := conn.Write(ctx, websocket.MessageBinary, encoded); err != nil {
		handshake.Destroy()
		return nil, err
	}
	for {
		messageType, payload, err := conn.Read(ctx)
		if err != nil {
			handshake.Destroy()
			return nil, err
		}
		if messageType != websocket.MessageBinary {
			continue
		}
		frame, err := decodeRelayMessageFrame(payload)
		if err != nil || frame.StreamID != "stream-1" {
			continue
		}
		if frame.Kind == relayFrameReset {
			handshake.Destroy()
			return nil, errors.New("inbound listener reset the Noise handshake")
		}
		if frame.Kind != relayFrameHandshake {
			continue
		}
		responsePayload := make([]byte, clatter.MaxMessageLen)
		if _, err := handshake.ReadMessage(frame.HandshakeBytes, responsePayload); err != nil {
			handshake.Destroy()
			return nil, err
		}
		transport, err := handshake.Finalize()
		if err != nil {
			return nil, err
		}
		return transport, nil
	}
}

// Rust #62325 run_inbound_noise_environment: the executor registers its
// generated Noise identity with the environment registry and then serves
// inbound harness connections on the published listening socket.
func TestRunInboundNoiseEnvironmentRegistersThenServesLikeRust(t *testing.T) {
	executorKeys := make(chan RemotePublicKey, 1)
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/register"):
			var body remoteRegistrationRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode register request: %v", err)
			}
			executorKeys <- body.ExecutorPublicKey
			_ = json.NewEncoder(w).Encode(remoteRegistrationResponse{
				EnvironmentID:          "env-inbound",
				SecurityProfile:        RemoteSecurityProfile,
				ExecutorRegistrationID: "reg-inbound",
				URL:                    "ws://127.0.0.1:1/relay",
			})
		case strings.HasSuffix(r.URL.Path, "/validate"):
			var body remoteHarnessValidationRequest
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.ExecutorRegistrationID != "reg-inbound" || body.HarnessKeyAuthorization != "inbound-authorization" {
				t.Errorf("validation request = %#v", body)
			}
			_ = json.NewEncoder(w).Encode(remoteHarnessValidationResponse{Valid: true})
		default:
			http.NotFound(w, r)
		}
	}))
	defer registry.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	control := NewInboundNoiseListenerControl()
	done := make(chan error, 1)
	go func() {
		done <- RunInboundNoiseEnvironmentListener(ctx, listener, RemoteEnvironmentConfig{
			BaseURL:       registry.URL,
			EnvironmentID: "env-inbound",
			HTTPClient:    registry.Client(),
		}, control)
	}()
	defer func() {
		cancel()
		control.Stop()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("inbound environment stopped with %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Errorf("inbound environment did not stop")
		}
	}()

	var executorKey RemotePublicKey
	select {
	case executorKey = <-executorKeys:
	case <-time.After(5 * time.Second):
		t.Fatal("inbound environment did not register with the registry")
	}
	harnessIdentity, err := generateRemoteNoiseIdentity()
	if err != nil {
		t.Fatalf("generate harness identity: %v", err)
	}
	defer harnessIdentity.Destroy()
	dialCtx, dialCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer dialCancel()
	conn, _, err := websocket.Dial(dialCtx, "ws://"+listener.Addr().String(), nil)
	if err != nil {
		t.Fatalf("dial inbound environment: %v", err)
	}
	defer conn.CloseNow()
	handshakeCtx, handshakeCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer handshakeCancel()
	transport, err := completeInboundHarnessHandshake(
		handshakeCtx,
		conn,
		harnessIdentity,
		executorKey,
		noiseChannelPrologue("env-inbound", "reg-inbound", "stream-1"),
	)
	if err != nil {
		t.Fatalf("inbound harness handshake: %v", err)
	}
	defer transport.Destroy()
	request := []byte(`{"id":1,"method":"initialize","params":{"clientName":"inbound-test"}}`)
	framed := make([]byte, 4, len(request)+4)
	binary.BigEndian.PutUint32(framed, uint32(len(request)))
	framed = append(framed, request...)
	ciphertext := make([]byte, len(framed)+clatter.TagLen)
	ciphertextLen, err := transport.Send(framed, ciphertext)
	if err != nil {
		t.Fatalf("encrypt request: %v", err)
	}
	encoded, err := encodeRelayMessageFrame(newRelayDataFrame("stream-1", 0, ciphertext[:ciphertextLen]))
	if err != nil {
		t.Fatalf("encode request frame: %v", err)
	}
	if err := conn.Write(handshakeCtx, websocket.MessageBinary, encoded); err != nil {
		t.Fatalf("write request frame: %v", err)
	}
	response, err := readRemoteTestJSONRPC(handshakeCtx, conn, transport)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if !strings.Contains(string(response), `"sessionId"`) {
		t.Fatalf("response = %s, want the initialize result", response)
	}
}

// Rust #62325 accepts exactly `ws://IP:PORT` for a remote Noise listener.
func TestParseInboundNoiseListenURLMatchesRust(t *testing.T) {
	for _, listen := range []string{"ws://127.0.0.1:8080", "ws://[::1]:8080"} {
		if _, err := ParseInboundNoiseListenURL(listen); err != nil {
			t.Fatalf("ParseInboundNoiseListenURL(%q) error = %v", listen, err)
		}
	}
	for _, listen := range []string{"", "stdio", "wss://127.0.0.1:8080", "ws://localhost:8080", "ws://127.0.0.1", "tcp://127.0.0.1:1"} {
		if _, err := ParseInboundNoiseListenURL(listen); err == nil {
			t.Fatalf("ParseInboundNoiseListenURL(%q) succeeded", listen)
		}
	}
}

// Rust #62325: a Noise executor started with --listen serves inbound
// connections through the ordinary remote-environment entry point.
func TestRunRemoteEnvironmentServesInboundNoiseWhenConfiguredLikeRust(t *testing.T) {
	executorKeys := make(chan RemotePublicKey, 1)
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/register"):
			var body remoteRegistrationRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode register request: %v", err)
			}
			executorKeys <- body.ExecutorPublicKey
			_ = json.NewEncoder(w).Encode(remoteRegistrationResponse{
				EnvironmentID:          "env-listen",
				SecurityProfile:        RemoteSecurityProfile,
				ExecutorRegistrationID: "reg-listen",
				URL:                    "ws://127.0.0.1:1/relay",
			})
		case strings.HasSuffix(r.URL.Path, "/validate"):
			_ = json.NewEncoder(w).Encode(remoteHarnessValidationResponse{Valid: true})
		default:
			http.NotFound(w, r)
		}
	}))
	defer registry.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- RunRemoteEnvironment(ctx, RemoteEnvironmentConfig{
			BaseURL:         registry.URL,
			EnvironmentID:   "env-listen",
			HTTPClient:      registry.Client(),
			InboundListener: listener,
		})
	}()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("inbound environment stopped with %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Errorf("inbound environment did not stop")
		}
	}()
	var executorKey RemotePublicKey
	select {
	case executorKey = <-executorKeys:
	case <-time.After(5 * time.Second):
		t.Fatal("inbound environment did not register with the registry")
	}
	harnessIdentity, err := generateRemoteNoiseIdentity()
	if err != nil {
		t.Fatalf("generate harness identity: %v", err)
	}
	defer harnessIdentity.Destroy()
	dialCtx, dialCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer dialCancel()
	conn, _, err := websocket.Dial(dialCtx, "ws://"+listener.Addr().String(), nil)
	if err != nil {
		t.Fatalf("dial inbound environment: %v", err)
	}
	defer conn.CloseNow()
	handshakeCtx, handshakeCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer handshakeCancel()
	transport, err := completeInboundHarnessHandshake(
		handshakeCtx,
		conn,
		harnessIdentity,
		executorKey,
		noiseChannelPrologue("env-listen", "reg-listen", "stream-1"),
	)
	if err != nil {
		t.Fatalf("inbound harness handshake: %v", err)
	}
	defer transport.Destroy()
	request := []byte(`{"id":1,"method":"initialize","params":{"clientName":"listen-test"}}`)
	framed := make([]byte, 4, len(request)+4)
	binary.BigEndian.PutUint32(framed, uint32(len(request)))
	framed = append(framed, request...)
	ciphertext := make([]byte, len(framed)+clatter.TagLen)
	ciphertextLen, err := transport.Send(framed, ciphertext)
	if err != nil {
		t.Fatalf("encrypt request: %v", err)
	}
	encoded, err := encodeRelayMessageFrame(newRelayDataFrame("stream-1", 0, ciphertext[:ciphertextLen]))
	if err != nil {
		t.Fatalf("encode request frame: %v", err)
	}
	if err := conn.Write(handshakeCtx, websocket.MessageBinary, encoded); err != nil {
		t.Fatalf("write request frame: %v", err)
	}
	response, err := readRemoteTestJSONRPC(handshakeCtx, conn, transport)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if !strings.Contains(string(response), `"sessionId"`) {
		t.Fatalf("response = %s, want the initialize result", response)
	}
}

// Rust #62325 rejects forwarding and the direct transport for an inbound
// Noise executor.
func TestRunRemoteEnvironmentRejectsInboundCombinationsLikeRust(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()
	err = RunRemoteEnvironment(context.Background(), RemoteEnvironmentConfig{
		BaseURL:         "http://127.0.0.1:1",
		EnvironmentID:   "env-listen",
		InboundListener: listener,
		ForwardURL:      "ws://127.0.0.1:2",
	})
	if err == nil || !strings.Contains(err.Error(), "does not support forwarding") {
		t.Fatalf("forwarding inbound error = %v", err)
	}
	err = RunRemoteEnvironment(context.Background(), RemoteEnvironmentConfig{
		BaseURL:         "http://127.0.0.1:1",
		EnvironmentID:   "env-listen",
		Transport:       RemoteTransportDirect,
		InboundListener: listener,
	})
	if err == nil || !strings.Contains(err.Error(), "cannot listen for Noise connections") {
		t.Fatalf("direct inbound error = %v", err)
	}
}
