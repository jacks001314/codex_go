package codexapi

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// Mirrors Rust #57597: a probe keeps the upgrade status and the allowlisted
// tracking headers, and redacted debug output never exposes the URL, header
// values, or close reason.
func TestProbeResponsesWebsocketRetainsUpgradeEvidenceLikeRust(t *testing.T) {
	const secret = "private-wire-value"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("x-request-id", "req-1")
		w.Header().Set("cf-ray", "ray-1")
		w.Header().Set("set-cookie", "session="+secret)
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("Accept() error = %v", err)
			return
		}
		_ = conn.Close(websocket.StatusPolicyViolation, secret)
	}))
	defer server.Close()
	endpoint := strings.Replace(server.URL, "http://", "ws://", 1) + "/v1/responses?token=" + secret
	probe, probeErr := ProbeResponsesWebsocket(t.Context(), endpoint, &ResponsesWebsocketProbeOptions{ImmediateCloseTimeout: time.Second})
	if probeErr != nil {
		t.Fatalf("probe error = %v", probeErr)
	}
	if probe.Status != http.StatusSwitchingProtocols {
		t.Fatalf("status = %d, want 101", probe.Status)
	}
	if got := probe.Headers.Get("x-request-id"); got != "req-1" {
		t.Fatalf("x-request-id = %q", got)
	}
	if got := probe.Headers.Get("cf-ray"); got != "ray-1" {
		t.Fatalf("cf-ray = %q", got)
	}
	if got := probe.Headers.Get("set-cookie"); got != "" {
		t.Fatalf("non-allowlisted header retained: %q", got)
	}
	if probe.ImmediateClose == nil || probe.ImmediateClose.Code != "1008" || probe.ImmediateClose.Reason != secret {
		t.Fatalf("immediate close = %#v", probe.ImmediateClose)
	}
	rendered := fmt.Sprintf("%v %+v %s", probe, probe, RedactedWebsocketProbeDebug(probe))
	for _, leaked := range []string{secret, "token=", server.URL} {
		if strings.Contains(rendered, leaked) {
			t.Fatalf("probe debug leaked %q: %s", leaked, rendered)
		}
	}
}

// A read failure after the upgrade retains the status and tracking headers in
// the structured probe error while its String hides wire values.
func TestProbeResponsesWebsocketKeepsEvidenceOnPostUpgradeReadFailureLikeRust(t *testing.T) {
	server := newRawProbeServer(t, func(_ net.Conn, buffered *bufio.ReadWriter) {
		_, _ = buffered.Write([]byte{0xC1, 0x00})
		_ = buffered.Flush()
	})
	endpoint := strings.Replace(server.URL, "http://", "ws://", 1) + "/v1/responses"
	probe, probeErr := ProbeResponsesWebsocket(t.Context(), endpoint, &ResponsesWebsocketProbeOptions{ImmediateCloseTimeout: time.Second})
	if probeErr == nil {
		t.Fatalf("probe = %#v, want a read failure", probe)
	}
	if probeErr.Status != http.StatusSwitchingProtocols {
		t.Fatalf("probe error status = %d, want 101", probeErr.Status)
	}
	if got := probeErr.Headers.Get("x-request-id"); got != "req-read" {
		t.Fatalf("retained x-request-id = %q", got)
	}
	if probeErr.Err == nil {
		t.Fatal("the underlying read error was not preserved")
	}
	if rendered := fmt.Sprintf("%v %+v", probeErr, probeErr); strings.Contains(rendered, "rsv") {
		t.Fatalf("probe error debug leaked the raw read error: %s", rendered)
	}
}

// A certificate rejection is typed as a TLS failure while the underlying error
// stays available to callers.
func TestProbeResponsesWebsocketClassifiesTLSFailureLikeRust(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()
	endpoint := strings.Replace(server.URL, "https://", "wss://", 1) + "/v1/responses"
	_, probeErr := ProbeResponsesWebsocket(t.Context(), endpoint, nil)
	if probeErr == nil {
		t.Fatal("probe succeeded against an untrusted certificate")
	}
	if probeErr.Stage != ResponsesWebsocketProbeFailureStageTLS {
		t.Fatalf("stage = %q, want tls (err = %v)", probeErr.Stage, probeErr.Err)
	}
	if probeErr.Err == nil {
		t.Fatal("the underlying TLS error was not preserved")
	}
}

// An empty close frame is recorded as code 1005.
func TestProbeResponsesWebsocketReportsEmptyCloseAs1005LikeRust(t *testing.T) {
	server := newRawProbeServer(t, func(_ net.Conn, buffered *bufio.ReadWriter) {
		_, _ = buffered.Write([]byte{0x88, 0x00})
		_ = buffered.Flush()
	})
	endpoint := strings.Replace(server.URL, "http://", "ws://", 1) + "/v1/responses"
	probe, probeErr := ProbeResponsesWebsocket(t.Context(), endpoint, &ResponsesWebsocketProbeOptions{ImmediateCloseTimeout: time.Second})
	if probeErr != nil {
		t.Fatalf("probe error = %v", probeErr)
	}
	if probe.ImmediateClose == nil || probe.ImmediateClose.Code != "1005" || probe.ImmediateClose.Reason != "" {
		t.Fatalf("immediate close = %#v, want 1005", probe.ImmediateClose)
	}
}

func newRawProbeServer(t *testing.T, afterUpgrade func(net.Conn, *bufio.ReadWriter)) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "hijacking unsupported", http.StatusInternalServerError)
			return
		}
		conn, buffered, err := hijacker.Hijack()
		if err != nil {
			return
		}
		sum := sha1.Sum([]byte(r.Header.Get("Sec-WebSocket-Key") + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
		accept := base64.StdEncoding.EncodeToString(sum[:])
		_, _ = fmt.Fprintf(buffered, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\nx-request-id: req-read\r\n\r\n", accept)
		if err := buffered.Flush(); err != nil {
			_ = conn.Close()
			return
		}
		afterUpgrade(conn, buffered)
		time.Sleep(100 * time.Millisecond)
		_ = conn.Close()
	}))
	t.Cleanup(server.Close)
	return server
}
