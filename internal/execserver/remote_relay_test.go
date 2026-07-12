package execserver

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
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

const (
	remoteRelayTestEnvironmentID  = "environment-1"
	remoteRelayTestRegistrationID = "registration-1"
)

type remoteRelayTestRig struct {
	conn                *websocket.Conn
	environmentIdentity *remoteNoiseIdentity
	harnessIdentity     *remoteNoiseIdentity
	done                <-chan error
	close               func()
}

func newRemoteRelayTestRig(t *testing.T, validation http.Handler) *remoteRelayTestRig {
	t.Helper()
	if validation == nil {
		validation = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(remoteHarnessValidationResponse{Valid: true})
		})
	}
	registry := httptest.NewServer(validation)
	environmentIdentity, err := generateRemoteNoiseIdentity()
	if err != nil {
		registry.Close()
		t.Fatalf("generate environment identity: %v", err)
	}
	harnessIdentity, err := generateRemoteNoiseIdentity()
	if err != nil {
		environmentIdentity.Destroy()
		registry.Close()
		t.Fatalf("generate harness identity: %v", err)
	}
	done := make(chan error, 1)
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			done <- err
			return
		}
		done <- NewServer().serveNoiseRelayConnection(
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
			environmentIdentity,
		)
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(relay.URL, "http"), nil)
	if err != nil {
		relay.Close()
		harnessIdentity.Destroy()
		environmentIdentity.Destroy()
		registry.Close()
		t.Fatalf("dial relay: %v", err)
	}
	var closeOnce sync.Once
	closeRig := func() {
		closeOnce.Do(func() {
			_ = conn.CloseNow()
			relay.CloseClientConnections()
			registry.CloseClientConnections()
			relay.Close()
			registry.Close()
			harnessIdentity.Destroy()
			environmentIdentity.Destroy()
		})
	}
	t.Cleanup(closeRig)
	return &remoteRelayTestRig{
		conn:                conn,
		environmentIdentity: environmentIdentity,
		harnessIdentity:     harnessIdentity,
		done:                done,
		close:               closeRig,
	}
}

func (r *remoteRelayTestRig) handshakeRequest(t *testing.T, streamID string, authorization []byte) []byte {
	t.Helper()
	handshake, err := clatter.NewHybridHandshake(
		clatter.PatternHybridIK,
		true,
		remoteNoiseCipherSuite(),
		clatter.WithStaticKey(r.harnessIdentity.dh),
		clatter.WithStaticKEMKey(r.harnessIdentity.kem),
		clatter.WithRemoteStatic(r.environmentIdentity.dh.Public),
		clatter.WithRemoteStaticKEMKey(r.environmentIdentity.kem.Public),
		clatter.WithPrologue(noiseChannelPrologue(remoteRelayTestEnvironmentID, remoteRelayTestRegistrationID, streamID)),
	)
	if err != nil {
		t.Fatalf("new HybridIK handshake: %v", err)
	}
	defer handshake.Destroy()
	request := make([]byte, clatter.MaxMessageLen)
	requestLen, err := handshake.WriteMessage(authorization, request)
	if err != nil {
		t.Fatalf("write HybridIK request: %v", err)
	}
	return append([]byte(nil), request[:requestLen]...)
}

func writeRemoteRelayTestFrame(t *testing.T, conn *websocket.Conn, frame relayMessageFrame) {
	t.Helper()
	encoded, err := encodeRelayMessageFrame(frame)
	if err != nil {
		t.Fatalf("encode relay frame: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageBinary, encoded); err != nil {
		t.Fatalf("write relay frame: %v", err)
	}
}

func readRemoteRelayTestReset(t *testing.T, conn *websocket.Conn, streamID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		messageType, payload, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("read relay reset: %v", err)
		}
		if messageType != websocket.MessageBinary {
			continue
		}
		frame, err := decodeRelayMessageFrame(payload)
		if err != nil {
			continue
		}
		if frame.StreamID == streamID && frame.Kind == relayFrameReset {
			return
		}
	}
}

func waitRemoteRelayFailure(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "repeated handshake failures") {
			t.Fatalf("relay error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("relay did not close after exhausting the handshake failure budget")
	}
}

func TestRemoteNoiseIdentityUsesRealRustCompatibleKeySizes(t *testing.T) {
	identity, err := generateRemoteNoiseIdentity()
	if err != nil {
		t.Fatalf("generateRemoteNoiseIdentity() error = %v", err)
	}
	defer identity.Destroy()
	publicKey := identity.PublicKey()
	dh, err := base64.StdEncoding.DecodeString(publicKey.X25519PublicKey)
	if err != nil {
		t.Fatalf("decode X25519 key: %v", err)
	}
	kem, err := base64.StdEncoding.DecodeString(publicKey.MLKEM768PublicKey)
	if err != nil {
		t.Fatalf("decode ML-KEM key: %v", err)
	}
	if publicKey.Suite != noiseChannelSuite || len(dh) != 32 || len(kem) != mlkem768PublicKeyBytes {
		t.Fatalf("public key = %#v, decoded sizes = %d, %d", publicKey, len(dh), len(kem))
	}
	if err := remoteNoiseCipherSuite().SKEM.ValidatePublicKey(kem); err != nil {
		t.Fatalf("ValidatePublicKey() error = %v", err)
	}
}

func TestRelayMessageFrameProtobufRoundTripsLikeRust(t *testing.T) {
	tests := []relayMessageFrame{
		newRelayDataFrame("stream-1", 7, []byte("ciphertext")),
		newRelayHandshakeFrame("stream-2", []byte("handshake")),
		newRelayResetFrame("stream-3"),
		{Version: relayMessageFrameVersion, StreamID: "stream-4", Kind: relayFrameAck},
		{Version: relayMessageFrameVersion, StreamID: "stream-5", Kind: relayFrameResume},
		{Version: relayMessageFrameVersion, StreamID: "stream-6", Kind: relayFrameHeartbeat},
	}
	for _, want := range tests {
		encoded, err := encodeRelayMessageFrame(want)
		if err != nil {
			t.Fatalf("encodeRelayMessageFrame(%v) error = %v", want.Kind, err)
		}
		got, err := decodeRelayMessageFrame(encoded)
		if err != nil {
			t.Fatalf("decodeRelayMessageFrame(%v) error = %v", want.Kind, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("round trip = %#v, want %#v", got, want)
		}
	}
}

func TestRemoteOrderedCiphertextsMatchesRustBounds(t *testing.T) {
	ordered := remoteOrderedCiphertexts{}
	frame := func(value byte) []byte { return bytes.Repeat([]byte{value}, 16) }
	if ready, err := ordered.Push(1, frame(1)); err != nil || len(ready) != 0 {
		t.Fatalf("Push(gap) = %#v, %v", ready, err)
	}
	ready, err := ordered.Push(0, frame(0))
	if err != nil || len(ready) != 2 || ready[0][0] != 0 || ready[1][0] != 1 {
		t.Fatalf("Push(close gap) = %#v, %v", ready, err)
	}
	if ready, err := ordered.Push(0, frame(9)); err != nil || len(ready) != 0 {
		t.Fatalf("Push(duplicate) = %#v, %v", ready, err)
	}
	if _, err := ordered.Push(67, frame(2)); err == nil || !strings.Contains(err.Error(), "reorder window") {
		t.Fatalf("Push(out of window) error = %v", err)
	}
}

func TestRemoteOrderedCiphertextsKeepsFirstDuplicateAndBoundsPendingBytesLikeRust(t *testing.T) {
	ordered := remoteOrderedCiphertexts{}
	if ready, err := ordered.Push(1, []byte("first copy")); err != nil || len(ready) != 0 {
		t.Fatalf("Push(first copy) = %#v, %v", ready, err)
	}
	if ready, err := ordered.Push(1, []byte("replacement")); err != nil || len(ready) != 0 {
		t.Fatalf("Push(replacement) = %#v, %v", ready, err)
	}
	ready, err := ordered.Push(0, []byte("zero"))
	if err != nil || !reflect.DeepEqual(ready, [][]byte{[]byte("zero"), []byte("first copy")}) {
		t.Fatalf("Push(close gap) = %#v, %v", ready, err)
	}
	if ready, err := ordered.Push(0, []byte("duplicate")); err != nil || len(ready) != 0 {
		t.Fatalf("Push(old duplicate) = %#v, %v", ready, err)
	}

	bounded := remoteOrderedCiphertexts{}
	if _, err := bounded.Push(1, make([]byte, maxNoiseRelayPendingBytes+1)); err == nil || !strings.Contains(err.Error(), "pending ciphertext buffer") {
		t.Fatalf("Push(oversized pending) error = %v", err)
	}
}

func TestRemoteJSONRPCDecoderFramesAndBoundsLikeRust(t *testing.T) {
	first := []byte(`{"id":1,"result":{}}`)
	second := []byte(`{"method":"initialized"}`)
	framed := func(message []byte) []byte {
		result := make([]byte, 4, len(message)+4)
		binary.BigEndian.PutUint32(result, uint32(len(message)))
		return append(result, message...)
	}
	joined := append(framed(first), framed(second)...)
	decoder := remoteJSONRPCDecoder{}
	if messages, err := decoder.Push(joined[:5]); err != nil || len(messages) != 0 {
		t.Fatalf("Push(partial) = %#v, %v", messages, err)
	}
	messages, err := decoder.Push(joined[5:])
	if err != nil || !reflect.DeepEqual(messages, [][]byte{first, second}) {
		t.Fatalf("Push(rest) = %#v, %v", messages, err)
	}
	invalid := make([]byte, 4)
	binary.BigEndian.PutUint32(invalid, 0)
	if _, err := (&remoteJSONRPCDecoder{}).Push(invalid); err == nil {
		t.Fatal("Push(zero length) error = nil")
	}
	oversizedDeclared := make([]byte, 4)
	binary.BigEndian.PutUint32(oversizedDeclared, uint32(maxNoiseJSONRPCMessageLen+1))
	if _, err := (&remoteJSONRPCDecoder{}).Push(oversizedDeclared); err == nil || err.Error() != "Noise relay JSON-RPC message has invalid length" {
		t.Fatalf("Push(oversized declared length) error = %v", err)
	}
	if _, err := (&remoteJSONRPCDecoder{}).Push(make([]byte, noiseRecordPlaintextLen+1)); err == nil || err.Error() != "Noise relay plaintext record exceeds maximum length" {
		t.Fatalf("Push(oversized plaintext record) error = %v", err)
	}
}

func TestRemoteJSONRPCDecoderFragmentsLargeMessageLikeRust(t *testing.T) {
	message, err := json.Marshal(map[string]any{
		"method": "large/test",
		"params": map[string]any{"data": strings.Repeat("x", 128*1024)},
	})
	if err != nil {
		t.Fatalf("marshal large message: %v", err)
	}
	framed := make([]byte, 4, len(message)+4)
	binary.BigEndian.PutUint32(framed, uint32(len(message)))
	framed = append(framed, message...)
	decoder := remoteJSONRPCDecoder{}
	var decoded [][]byte
	for len(framed) != 0 {
		chunkLen := min(len(framed), noiseRecordPlaintextLen)
		messages, err := decoder.Push(framed[:chunkLen])
		if err != nil {
			t.Fatalf("Push(fragment) error = %v", err)
		}
		decoded = append(decoded, messages...)
		framed = framed[chunkLen:]
	}
	if !reflect.DeepEqual(decoded, [][]byte{message}) {
		t.Fatalf("decoded %d messages", len(decoded))
	}
}

func TestValidateRemoteHarnessKeyDoesNotExposeAuthorization(t *testing.T) {
	const secret = "authorization-that-must-not-leak"
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, secret, http.StatusInternalServerError)
	}))
	defer registry.Close()
	err := validateRemoteHarnessKey(context.Background(), RemoteEnvironmentConfig{
		BaseURL: registry.URL, EnvironmentID: "environment-1", HTTPClient: registry.Client(),
	}, &remoteRegistrationResponse{ExecutorRegistrationID: "registration-1"}, RemotePublicKey{Suite: noiseChannelSuite}, secret)
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("validateRemoteHarnessKey() error = %v", err)
	}
}

func TestPendingHarnessKeyValidationDoesNotBlockNewHandshakesLikeRust(t *testing.T) {
	calls := make(chan string, 2)
	release := make(chan struct{})
	rig := newRemoteRelayTestRig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body remoteHarnessValidationRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		calls <- body.HarnessKeyAuthorization
		select {
		case <-release:
			_ = json.NewEncoder(w).Encode(remoteHarnessValidationResponse{Valid: true})
		case <-r.Context().Done():
		}
	}))
	defer close(release)

	for _, streamID := range []string{"stream-1", "stream-2"} {
		request := rig.handshakeRequest(t, streamID, []byte("authorization-"+streamID))
		writeRemoteRelayTestFrame(t, rig.conn, newRelayHandshakeFrame(streamID, request))
	}
	seen := map[string]bool{}
	for len(seen) != 2 {
		select {
		case authorization := <-calls:
			seen[authorization] = true
		case <-time.After(5 * time.Second):
			t.Fatalf("validation calls = %#v", seen)
		}
	}
}

func TestOversizedHarnessAuthorizationRejectedBeforeValidationLikeRust(t *testing.T) {
	calls := make(chan struct{}, 1)
	rig := newRemoteRelayTestRig(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls <- struct{}{}
		_ = json.NewEncoder(w).Encode(remoteHarnessValidationResponse{Valid: true})
	}))
	streamID := "stream-oversized"
	request := rig.handshakeRequest(t, streamID, bytes.Repeat([]byte{'a'}, maxHarnessKeyAuthorizationBytes+1))
	writeRemoteRelayTestFrame(t, rig.conn, newRelayHandshakeFrame(streamID, request))
	readRemoteRelayTestReset(t, rig.conn, streamID)
	select {
	case <-calls:
		t.Fatal("oversized authorization reached registry validation")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestDuplicateHandshakesExhaustFailureBudgetLikeRust(t *testing.T) {
	calls := make(chan struct{}, maxFailedNoiseHandshakes)
	release := make(chan struct{})
	rig := newRemoteRelayTestRig(t, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		calls <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer close(release)
	streamID := "stream-duplicate"
	request := rig.handshakeRequest(t, streamID, []byte("authorization"))
	sendHandshake := func() {
		writeRemoteRelayTestFrame(t, rig.conn, newRelayHandshakeFrame(streamID, request))
	}
	waitValidation := func() {
		select {
		case <-calls:
		case <-time.After(5 * time.Second):
			t.Fatal("registry validation did not start")
		}
	}

	sendHandshake()
	waitValidation()
	for failure := 1; failure <= maxFailedNoiseHandshakes; failure++ {
		sendHandshake()
		if failure == maxFailedNoiseHandshakes {
			break
		}
		readRemoteRelayTestReset(t, rig.conn, streamID)
		sendHandshake()
		waitValidation()
	}
	waitRemoteRelayFailure(t, rig.done)
}

func TestRepeatedMalformedHandshakesClosePhysicalRelayLikeRust(t *testing.T) {
	rig := newRemoteRelayTestRig(t, nil)
	for attempt := 0; attempt < maxFailedNoiseHandshakes; attempt++ {
		streamID := "malformed-" + string(rune('0'+attempt))
		request := rig.handshakeRequest(t, streamID, []byte("authorization"))
		request[len(request)-1] ^= 1
		writeRemoteRelayTestFrame(t, rig.conn, newRelayHandshakeFrame(streamID, request))
	}
	waitRemoteRelayFailure(t, rig.done)
}

func TestRepeatedEarlyDataDuringValidationClosesPhysicalRelayLikeRust(t *testing.T) {
	calls := make(chan struct{}, maxFailedNoiseHandshakes)
	release := make(chan struct{})
	rig := newRemoteRelayTestRig(t, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		calls <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer close(release)
	for attempt := 0; attempt < maxFailedNoiseHandshakes; attempt++ {
		streamID := "early-data-" + string(rune('0'+attempt))
		request := rig.handshakeRequest(t, streamID, []byte("authorization"))
		writeRemoteRelayTestFrame(t, rig.conn, newRelayHandshakeFrame(streamID, request))
		select {
		case <-calls:
		case <-time.After(5 * time.Second):
			t.Fatal("registry validation did not start")
		}
		writeRemoteRelayTestFrame(t, rig.conn, newRelayDataFrame(streamID, 0, []byte{0}))
	}
	waitRemoteRelayFailure(t, rig.done)
}

func TestRemoteNoiseHandshakeRejectsMismatchedPrologueAndTamperingLikeRust(t *testing.T) {
	rig := newRemoteRelayTestRig(t, nil)
	request := rig.handshakeRequest(t, "stream-1", []byte("authorization"))
	if handshake, _, err := readRemoteNoiseHandshake(
		rig.environmentIdentity,
		noiseChannelPrologue(remoteRelayTestEnvironmentID, remoteRelayTestRegistrationID, "stream-2"),
		request,
	); err == nil {
		handshake.Destroy()
		t.Fatal("mismatched prologue accepted")
	}
	tampered := append([]byte(nil), request...)
	tampered[len(tampered)-1] ^= 1
	if handshake, _, err := readRemoteNoiseHandshake(
		rig.environmentIdentity,
		noiseChannelPrologue(remoteRelayTestEnvironmentID, remoteRelayTestRegistrationID, "stream-1"),
		tampered,
	); err == nil {
		handshake.Destroy()
		t.Fatal("tampered handshake accepted")
	}
}

func TestNoiseVirtualStreamProcessorExitReportsClosedStreamLikeRust(t *testing.T) {
	executorIdentity, err := generateRemoteNoiseIdentity()
	if err != nil {
		t.Fatalf("generate executor identity: %v", err)
	}
	defer executorIdentity.Destroy()
	harnessIdentity, err := generateRemoteNoiseIdentity()
	if err != nil {
		t.Fatalf("generate harness identity: %v", err)
	}
	defer harnessIdentity.Destroy()
	prologue := []byte("test-prologue")
	initiator, err := clatter.NewHybridHandshake(
		clatter.PatternHybridIK,
		true,
		remoteNoiseCipherSuite(),
		clatter.WithStaticKey(harnessIdentity.dh),
		clatter.WithStaticKEMKey(harnessIdentity.kem),
		clatter.WithRemoteStatic(executorIdentity.dh.Public),
		clatter.WithRemoteStaticKEMKey(executorIdentity.kem.Public),
		clatter.WithPrologue(prologue),
	)
	if err != nil {
		t.Fatalf("new initiator handshake: %v", err)
	}
	request := make([]byte, clatter.MaxMessageLen)
	requestLen, err := initiator.WriteMessage([]byte("authorization"), request)
	if err != nil {
		initiator.Destroy()
		t.Fatalf("write initiator handshake: %v", err)
	}
	pending, _, err := readRemoteNoiseHandshake(executorIdentity, prologue, request[:requestLen])
	if err != nil {
		initiator.Destroy()
		t.Fatalf("read responder handshake: %v", err)
	}
	executorTransport, response, err := pending.Complete()
	if err != nil {
		initiator.Destroy()
		t.Fatalf("complete responder handshake: %v", err)
	}
	responsePayload := make([]byte, clatter.MaxMessageLen)
	if _, err := initiator.ReadMessage(response, responsePayload); err != nil {
		executorTransport.Destroy()
		initiator.Destroy()
		t.Fatalf("read responder handshake: %v", err)
	}
	harnessTransport, err := initiator.Finalize()
	if err != nil {
		executorTransport.Destroy()
		t.Fatalf("finalize initiator handshake: %v", err)
	}
	defer harnessTransport.Destroy()

	outgoing := make(chan []byte, remoteRelayChannelCapacity)
	closed := make(chan remoteRelayClosedStream, 1)
	stream := newRemoteNoiseVirtualStream(context.Background(), NewServer(), "stream-1", 7, executorTransport, outgoing, closed)
	defer stream.Close()
	message := []byte(`{"id":1,"result":null}`)
	framed := make([]byte, 4, len(message)+4)
	binary.BigEndian.PutUint32(framed, uint32(len(message)))
	framed = append(framed, message...)
	ciphertext := make([]byte, len(framed)+clatter.TagLen)
	ciphertextLen, err := harnessTransport.Send(framed, ciphertext)
	if err != nil {
		t.Fatalf("encrypt response: %v", err)
	}
	if err := stream.Receive(relayData{Seq: 0, SegmentCount: 1, Payload: ciphertext[:ciphertextLen]}); err != nil {
		t.Fatalf("Receive() error = %v", err)
	}
	select {
	case event := <-closed:
		if event.streamID != "stream-1" || event.instanceID != 7 {
			t.Fatalf("closed stream = %#v", event)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("virtual stream processor did not report closure")
	}
}

func TestNoiseChannelPrologueEncodingMatchesRust(t *testing.T) {
	want := []byte("\x00\x00\x00\x00\x00\x00\x00\x20codex-exec-server-relay-noise/v1" +
		"\x00\x00\x00\x00\x00\x00\x00\x05env-1" +
		"\x00\x00\x00\x00\x00\x00\x00\x0eregistration-1" +
		"\x00\x00\x00\x00\x00\x00\x00\x08stream-1")
	if got := noiseChannelPrologue("env-1", "registration-1", "stream-1"); !bytes.Equal(got, want) {
		t.Fatalf("noiseChannelPrologue() = %x, want %x", got, want)
	}
}

func TestValidateRemoteHarnessKeyRequiresExplicitValidResponseLikeRust(t *testing.T) {
	for _, response := range []string{`{}`, `{"valid":false}`, `not-json`} {
		response := response
		t.Run(response, func(t *testing.T) {
			registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(response))
			}))
			defer registry.Close()
			err := validateRemoteHarnessKey(context.Background(), RemoteEnvironmentConfig{
				BaseURL: registry.URL, EnvironmentID: remoteRelayTestEnvironmentID, HTTPClient: registry.Client(),
			}, &remoteRegistrationResponse{ExecutorRegistrationID: remoteRelayTestRegistrationID}, RemotePublicKey{Suite: noiseChannelSuite}, "authorization")
			if err == nil {
				t.Fatal("validation accepted a response without valid=true")
			}
		})
	}
}
