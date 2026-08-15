package execserver

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	clatter "github.com/shurlinet/go-clatter"
)

func TestRunRemoteEnvironmentRegistersAndServesRendezvousWebSocket(t *testing.T) {
	rpcDone := make(chan error, 1)
	registeredKey := make(chan RemotePublicKey, 1)
	harnessIdentity, err := generateRemoteNoiseIdentity()
	if err != nil {
		t.Fatalf("generateRemoteNoiseIdentity() error = %v", err)
	}
	defer harnessIdentity.Destroy()
	rendezvous := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			rpcDone <- err
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		executorKey := <-registeredKey
		transport, err := completeRemoteTestHarnessHandshake(ctx, conn, harnessIdentity, executorKey)
		if err != nil {
			rpcDone <- err
			return
		}
		defer transport.Destroy()
		request := []byte(`{"id":1,"method":"initialize","params":{"clientName":"remote-test"}}`)
		framed := make([]byte, 4, len(request)+4)
		binary.BigEndian.PutUint32(framed, uint32(len(request)))
		framed = append(framed, request...)
		ciphertext := make([]byte, len(framed)+clatter.TagLen)
		ciphertextLen, err := transport.Send(framed, ciphertext)
		if err != nil {
			rpcDone <- err
			return
		}
		encoded, err := encodeRelayMessageFrame(newRelayDataFrame("stream-1", 0, ciphertext[:ciphertextLen]))
		if err != nil {
			rpcDone <- err
			return
		}
		if err := conn.Write(ctx, websocket.MessageBinary, encoded); err != nil {
			rpcDone <- err
			return
		}
		data, err := readRemoteTestJSONRPC(ctx, conn, transport)
		if err != nil {
			rpcDone <- err
			return
		}
		if !bytes.Contains(data, []byte(`"id":1`)) || !bytes.Contains(data, []byte(`"sessionId"`)) {
			rpcDone <- errUnexpectedPayload(data)
			return
		}
		rpcDone <- nil
	}))
	defer rendezvous.Close()

	registerBody := make(chan remoteRegistrationRequest, 1)
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("registry request = %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer registry-token" {
			t.Errorf("Authorization = %q", got)
		}
		switch r.URL.Path {
		case "/cloud/environment/env-remote/register":
			var body remoteRegistrationRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode request: %v", err)
			}
			registerBody <- body
			registeredKey <- body.ExecutorPublicKey
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(remoteRegistrationResponse{
				EnvironmentID:          "env-remote",
				URL:                    "ws" + strings.TrimPrefix(rendezvous.URL, "http") + "/relay?role=environment",
				SecurityProfile:        RemoteSecurityProfile,
				ExecutorRegistrationID: "registration-1",
			})
		case "/cloud/environment/env-remote/validate":
			var body remoteHarnessValidationRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode validation request: %v", err)
			}
			if body.ExecutorRegistrationID != "registration-1" || body.HarnessKeyAuthorization != "test-authorization" || body.HarnessPublicKey != harnessIdentity.PublicKey() {
				t.Errorf("validation request = %#v", body)
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(remoteHarnessValidationResponse{Valid: true})
		default:
			http.NotFound(w, r)
		}
	}))
	defer registry.Close()

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- RunRemoteEnvironment(ctx, RemoteEnvironmentConfig{
			BaseURL:       registry.URL + "/",
			EnvironmentID: " env-remote ",
			AuthHeaders:   http.Header{"Authorization": []string{"Bearer registry-token"}},
			Backoff:       time.Millisecond,
			MaxBackoff:    time.Millisecond,
		})
	}()

	select {
	case err := <-rpcDone:
		if err != nil {
			t.Fatalf("rendezvous RPC failed: %v", err)
		}
	case err := <-errCh:
		t.Fatalf("RunRemoteEnvironment exited early: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("remote environment did not connect to rendezvous")
	}
	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("RunRemoteEnvironment returned error after cancel: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("RunRemoteEnvironment did not stop after cancel")
	}

	select {
	case body := <-registerBody:
		if body.SecurityProfile != RemoteSecurityProfile {
			t.Fatalf("security_profile = %q", body.SecurityProfile)
		}
		if body.ExecutorPublicKey.Suite != noiseChannelSuite || body.ExecutorPublicKey.X25519PublicKey == "" || body.ExecutorPublicKey.MLKEM768PublicKey == "" {
			t.Fatalf("executor_public_key = %#v", body.ExecutorPublicKey)
		}
	default:
		t.Fatal("registry did not receive register body")
	}
}

func TestRunRemoteEnvironmentReusesRegistrationUntilWebSocketURLIsRejectedLikeRust(t *testing.T) {
	var relayMu sync.Mutex
	relayConnections := 0
	thirdConnected := make(chan struct{}, 1)
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		relayMu.Lock()
		relayConnections++
		connectionNumber := relayConnections
		relayMu.Unlock()
		if connectionNumber == 1 {
			_ = conn.CloseNow()
			return
		}
		thirdConnected <- struct{}{}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		_, _, _ = conn.Read(ctx)
		_ = conn.CloseNow()
	}))
	defer relay.Close()
	relayURL := "ws" + strings.TrimPrefix(relay.URL, "http")

	var registrationMu sync.Mutex
	var registrationKeys []RemotePublicKey
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/cloud/environment/environment-requested/register" {
			http.NotFound(w, r)
			return
		}
		var body remoteRegistrationRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid registration", http.StatusBadRequest)
			return
		}
		registrationMu.Lock()
		registrationKeys = append(registrationKeys, body.ExecutorPublicKey)
		registrationMu.Unlock()
		_ = json.NewEncoder(w).Encode(remoteRegistrationResponse{
			EnvironmentID:          "environment-requested",
			URL:                    "ws://registry-issued.example/relay",
			SecurityProfile:        RemoteSecurityProfile,
			ExecutorRegistrationID: "registration-1",
		})
	}))
	defer registry.Close()

	var dialMu sync.Mutex
	dialAttempts := 0
	dial := func(ctx context.Context, _ string, options *websocket.DialOptions) (*websocket.Conn, *http.Response, error) {
		dialMu.Lock()
		dialAttempts++
		attempt := dialAttempts
		dialMu.Unlock()
		if attempt == 2 {
			return nil, &http.Response{
				StatusCode: http.StatusUnauthorized,
				Status:     "401 Unauthorized",
				Body:       http.NoBody,
			}, errors.New("websocket handshake rejected")
		}
		return websocket.Dial(ctx, relayURL, options)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- RunRemoteEnvironment(ctx, RemoteEnvironmentConfig{
			BaseURL:       registry.URL,
			EnvironmentID: "environment-requested",
			HTTPClient:    registry.Client(),
			Dial:          dial,
			Backoff:       time.Millisecond,
			MaxBackoff:    time.Millisecond,
		})
	}()

	select {
	case <-thirdConnected:
	case err := <-done:
		t.Fatalf("RunRemoteEnvironment exited before third connection: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("third relay connection did not arrive")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunRemoteEnvironment returned error after cancel: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunRemoteEnvironment did not stop after cancel")
	}

	registrationMu.Lock()
	keys := append([]RemotePublicKey(nil), registrationKeys...)
	registrationMu.Unlock()
	if len(keys) != 2 {
		t.Fatalf("registration count = %d, want 2", len(keys))
	}
	if keys[0] != keys[1] {
		t.Fatalf("executor identity changed across re-registration: %#v != %#v", keys[0], keys[1])
	}
	dialMu.Lock()
	attempts := dialAttempts
	dialMu.Unlock()
	if attempts != 3 {
		t.Fatalf("dial attempts = %d, want 3", attempts)
	}
}

func completeRemoteTestHarnessHandshake(
	ctx context.Context,
	conn *websocket.Conn,
	identity *remoteNoiseIdentity,
	executorKey RemotePublicKey,
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
		clatter.WithPrologue(noiseChannelPrologue("env-remote", "registration-1", "stream-1")),
	)
	if err != nil {
		return nil, err
	}
	request := make([]byte, clatter.MaxMessageLen)
	requestLen, err := handshake.WriteMessage([]byte("test-authorization"), request)
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
			return nil, errors.New("executor reset Noise relay handshake")
		}
		if frame.Kind != relayFrameHandshake {
			continue
		}
		responsePayload := make([]byte, clatter.MaxMessageLen)
		responsePayloadLen, err := handshake.ReadMessage(frame.HandshakeBytes, responsePayload)
		if err != nil {
			handshake.Destroy()
			return nil, err
		}
		if responsePayloadLen != 0 {
			handshake.Destroy()
			return nil, errors.New("Noise handshake response payload must be empty")
		}
		return handshake.Finalize()
	}
}

func readRemoteTestJSONRPC(ctx context.Context, conn *websocket.Conn, transport *clatter.TransportState) ([]byte, error) {
	decoder := remoteJSONRPCDecoder{}
	reorder := remoteOrderedCiphertexts{}
	for {
		messageType, payload, err := conn.Read(ctx)
		if err != nil {
			return nil, err
		}
		if messageType != websocket.MessageBinary {
			continue
		}
		frame, err := decodeRelayMessageFrame(payload)
		if err != nil || frame.StreamID != "stream-1" || frame.Kind != relayFrameData {
			continue
		}
		ciphertexts, err := reorder.Push(frame.Data.Seq, frame.Data.Payload)
		if err != nil {
			return nil, err
		}
		for _, ciphertext := range ciphertexts {
			plaintext := make([]byte, len(ciphertext)-clatter.TagLen)
			length, err := transport.Receive(ciphertext, plaintext)
			if err != nil {
				return nil, err
			}
			messages, err := decoder.Push(plaintext[:length])
			if err != nil {
				return nil, err
			}
			if len(messages) != 0 {
				return messages[0], nil
			}
		}
	}
}

func TestRegisterRemoteEnvironmentReportsRegistryErrors(t *testing.T) {
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"code":"bad_auth","message":"token expired"}}`, http.StatusUnauthorized)
	}))
	defer registry.Close()

	_, err := registerRemoteEnvironment(context.Background(), RemoteEnvironmentConfig{
		BaseURL:       registry.URL,
		EnvironmentID: "env-remote",
		AuthHeaders:   http.Header{"Authorization": []string{"Bearer expired"}},
		HTTPClient:    remoteHTTPClient(),
	}, RemotePublicKey{Suite: noiseChannelSuite, X25519PublicKey: "x", MLKEM768PublicKey: "m"})
	if err == nil || !strings.Contains(err.Error(), "environment registry authentication error") || !strings.Contains(err.Error(), "token expired") {
		t.Fatalf("register error = %v", err)
	}
}

func TestRegisterRemoteEnvironmentResolvesAuthHeadersPerRequestLikeRust(t *testing.T) {
	var receivedAuthHeader string
	resolveCount := 0
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuthHeader = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(remoteRegistrationResponse{
			EnvironmentID:   "env-1",
			SecurityProfile: RemoteSecurityProfile,
		})
	}))
	defer registry.Close()

	identity, err := generateRemoteNoiseIdentity()
	if err != nil {
		t.Fatalf("generateRemoteNoiseIdentity() error = %v", err)
	}
	defer identity.Destroy()

	cfg := RemoteEnvironmentConfig{
		BaseURL:       registry.URL,
		EnvironmentID: "env-1",
		AuthHeaders:   http.Header{"Authorization": []string{"Bearer static"}},
		ResolveAuthHeaders: func(ctx context.Context) (http.Header, error) {
			resolveCount++
			headers := http.Header{}
			headers.Set("Authorization", "Bearer fresh")
			return headers, nil
		},
		HTTPClient: registry.Client(),
	}
	if _, err := registerRemoteEnvironment(context.Background(), cfg, identity.PublicKey()); err != nil {
		t.Fatalf("registerRemoteEnvironment() error = %v", err)
	}
	if resolveCount != 1 {
		t.Fatalf("resolve count = %d, want 1 per registry request", resolveCount)
	}
	if receivedAuthHeader != "Bearer fresh" {
		t.Fatalf("Authorization = %q, want fresh managed credential from resolver", receivedAuthHeader)
	}
}

func TestRegisterRemoteEnvironmentAcceptsRegistryAllocationFieldsLikeRust(t *testing.T) {
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(remoteRegistrationResponse{
			EnvironmentID:   "env-remote",
			SecurityProfile: RemoteSecurityProfile,
		})
	}))
	defer registry.Close()
	response, err := registerRemoteEnvironment(context.Background(), RemoteEnvironmentConfig{
		BaseURL: registry.URL, EnvironmentID: "env-remote", HTTPClient: registry.Client(),
	}, RemotePublicKey{Suite: noiseChannelSuite})
	if err != nil {
		t.Fatalf("registerRemoteEnvironment() error = %v", err)
	}
	if response.URL != "" || response.ExecutorRegistrationID != "" {
		t.Fatalf("registration response = %#v", response)
	}
}

func TestRemoteRegistryErrorPreservesOptionalEmptyFieldsLikeRust(t *testing.T) {
	empty := ""
	tests := []struct {
		name        string
		body        string
		wantCode    *string
		wantMessage string
	}{
		{name: "missing message", body: `{"error":{"code":"bad"}}`, wantCode: stringPointer("bad"), wantMessage: `{"error":{"code":"bad"}}`},
		{name: "empty message", body: `{"error":{"code":"","message":""}}`, wantCode: &empty, wantMessage: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			code, message := registryHTTPErrorMessage(test.body)
			if !reflect.DeepEqual(code, test.wantCode) || message != test.wantMessage {
				t.Fatalf("registryHTTPErrorMessage() = %#v, %q; want %#v, %q", code, message, test.wantCode, test.wantMessage)
			}
		})
	}
	if message := registryErrorMessage(`{"error":{"message":""}}`); message != "" {
		t.Fatalf("registryErrorMessage(empty) = %q", message)
	}
}

func TestPreviewErrorBodyTruncatesUnicodeCharactersLikeRust(t *testing.T) {
	body := strings.Repeat("\u754c", errorBodyPreviewMaxBytes+1)
	preview := previewErrorBody(body)
	if len([]rune(preview)) != errorBodyPreviewMaxBytes || !strings.HasSuffix(preview, "\u754c") {
		t.Fatalf("preview rune length = %d", len([]rune(preview)))
	}
}

func stringPointer(value string) *string {
	return &value
}

func errUnexpectedMessageType(messageType websocket.MessageType) error {
	return &unexpectedRemoteTestValue{value: fmt.Sprintf("%d", messageType)}
}

func errUnexpectedPayload(data []byte) error {
	return &unexpectedRemoteTestValue{value: string(data)}
}

type unexpectedRemoteTestValue struct {
	value string
}

func (e *unexpectedRemoteTestValue) Error() string {
	return "unexpected rendezvous response: " + e.value
}
