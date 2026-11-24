package execserver

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// Rust #62325: admission headers from the connect bundle only travel with a
// wss:// endpoint and never replace WebSocket handshake headers.
func TestNoiseAdmissionHeadersRequireSecureWebSocketLikeRust(t *testing.T) {
	headers := http.Header{"X-Admission": []string{"token"}}
	if _, err := noiseAdmissionHeaders("ws://127.0.0.1:8080/relay", headers); err == nil || err.Error() != "Noise WebSocket admission headers require wss://" {
		t.Fatalf("plaintext admission error = %v", err)
	}
	if _, err := noiseAdmissionHeaders("wss://relay.example/relay", headers); err != nil {
		t.Fatalf("wss admission error = %v", err)
	}
	got, err := noiseAdmissionHeaders("wss://relay.example/relay", headers)
	if err != nil {
		t.Fatalf("noiseAdmissionHeaders() error = %v", err)
	}
	if got.Get("X-Admission") != "token" {
		t.Fatalf("admission headers = %#v", got)
	}
	if none, err := noiseAdmissionHeaders("ws://127.0.0.1:8080/relay", nil); err != nil || none != nil {
		t.Fatalf("empty admission headers = %#v, %v", none, err)
	}
	for _, reserved := range []string{"Host", "Connection", "Upgrade", "Content-Length", "Transfer-Encoding", "Sec-WebSocket-Protocol"} {
		_, err := noiseAdmissionHeaders("wss://relay.example/relay", http.Header{reserved: []string{"x"}})
		if err == nil || err.Error() != "invalid Noise WebSocket admission header" {
			t.Fatalf("reserved header %q error = %v", reserved, err)
		}
	}
	// The returned header is a copy, so a later bundle mutation cannot change a
	// dial in progress.
	headers.Set("X-Admission", "changed")
	if got.Get("X-Admission") != "token" {
		t.Fatalf("admission headers aliased the bundle: %#v", got)
	}
}

// Rust #62325 rejects admission headers before dialing a plaintext WebSocket.
func TestNoiseHarnessDialRejectsAdmissionHeadersOnPlaintextLikeRust(t *testing.T) {
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
	bundle := &NoiseRendezvousConnectBundle{
		WebSocketURL:      "ws://127.0.0.1:1/relay",
		EnvironmentID:     "env-1",
		ExecutorPublicKey: executorIdentity.PublicKey(),
		HTTPHeaders:       http.Header{"X-Admission": []string{"token"}},
	}
	_, err = dialNoiseHarnessConnection(context.Background(), bundle, harnessIdentity, nil)
	if err == nil || !strings.Contains(err.Error(), "admission headers require wss://") {
		t.Fatalf("plaintext admission dial error = %v", err)
	}
	// Without headers the dial proceeds and fails on the closed port instead of
	// the admission check.
	bundle.HTTPHeaders = nil
	_, err = dialNoiseHarnessConnection(context.Background(), bundle, harnessIdentity, nil)
	if err == nil || strings.Contains(err.Error(), "admission headers") {
		t.Fatalf("headerless dial error = %v", err)
	}
}
