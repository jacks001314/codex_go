package doctor

import (
	"bufio"
	"crypto/sha1"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"codex_go/model"
)

// Rust parity: codex-rs/cli/src/doctor.rs (#57597). Probe failure details keep
// the failure category but never echo wire values such as URLs, server bodies
// or TLS error text that may contain credentials.
func TestWebsocketProbeErrorDetailsRedactWireValuesLikeRust(t *testing.T) {
	const secret = "private-wire-value"
	response := &http.Response{
		StatusCode: http.StatusUnauthorized,
		Status:     "401 Unauthorized",
		Body:       io.NopCloser(strings.NewReader(secret)),
	}
	apiDetail := websocketHandshakeErrorDetail(response, errors.New("server said "+secret))
	if apiDetail != "handshake API error: 401 Unauthorized [redacted]" {
		t.Fatalf("api detail = %q", apiDetail)
	}
	transportDetail := websocketHandshakeErrorDetail(nil, &url.Error{
		Op:  "Get",
		URL: "https://example.com/?token=" + secret,
		Err: errors.New(secret),
	})
	if transportDetail != "handshake transport error: network error: [redacted]" {
		t.Fatalf("transport detail = %q", transportDetail)
	}
	tlsDetail := websocketHandshakeErrorDetail(nil, &url.Error{
		Op:  "Get",
		URL: "wss://example.com/responses",
		Err: &tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}},
	})
	if tlsDetail != "handshake transport error: TLS handshake failed: [redacted]" {
		t.Fatalf("tls detail = %q", tlsDetail)
	}
	for _, detail := range []string{apiDetail, transportDetail, tlsDetail} {
		if strings.Contains(detail, secret) || strings.Contains(detail, "example.com") {
			t.Fatalf("detail leaked a wire value: %q", detail)
		}
	}
	if got := closeReasonDetail(""); got != "empty" {
		t.Fatalf("empty close reason detail = %q", got)
	}
	if got := closeReasonDetail(secret); got != "present (redacted)" {
		t.Fatalf("close reason detail = %q", got)
	}
}

// TestWebsocketProbeReportsEmptyCloseAs1005LikeRust pins the empty close
// frame arm: the probe reports code 1005 with an empty reason.
func TestWebsocketProbeReportsEmptyCloseAs1005LikeRust(t *testing.T) {
	server := newRawWebsocketServer(t, func(_ net.Conn, buffered *bufio.ReadWriter) {
		_, _ = buffered.Write([]byte{0x88, 0x00})
		_ = buffered.Flush()
	})
	check := websocketReachabilityCheckFromProvider("custom", rawWebsocketProbeProvider(server.URL), nil)
	if check.Status != CheckStatusWarning || check.Summary != "Responses WebSocket closed immediately after handshake" {
		t.Fatalf("check = %+v", check)
	}
	for _, want := range []string{
		"immediate close code: 1005",
		"immediate close reason: empty",
	} {
		if !containsDetail(check, want) {
			t.Fatalf("missing detail %q in %#v", want, check.Details)
		}
	}
}

// TestWebsocketProbeKeepsUpgradeEvidenceOnReadFailureLikeRust pins the evidence
// retention arm: a malformed frame after a successful upgrade keeps the
// handshake result while redacting the read error.
func TestWebsocketProbeKeepsUpgradeEvidenceOnReadFailureLikeRust(t *testing.T) {
	server := newRawWebsocketServer(t, func(_ net.Conn, buffered *bufio.ReadWriter) {
		// Reserved-bit frame from the server is a protocol error the client
		// surfaces as a read failure.
		_, _ = buffered.Write([]byte{0xC1, 0x00})
		_ = buffered.Flush()
	})
	check := websocketReachabilityCheckFromProvider("custom", rawWebsocketProbeProvider(server.URL), nil)
	if check.Status != CheckStatusWarning || check.Summary != "Responses WebSocket failed; HTTPS fallback may still work" {
		t.Fatalf("check = %+v", check)
	}
	if !containsDetail(check, "handshake result: HTTP 101") {
		t.Fatalf("handshake evidence lost: %#v", check.Details)
	}
	if !containsDetail(check, "handshake stream error: [redacted]") {
		t.Fatalf("stream error was not redacted: %#v", check.Details)
	}
	joined := strings.Join(check.Details, "\n")
	if strings.Contains(joined, "rsv") {
		t.Fatalf("check leaked the raw read error:\n%s", joined)
	}
}

func rawWebsocketProbeProvider(serverURL string) *model.ProviderInfo {
	timeout := uint64(500)
	return &model.ProviderInfo{
		Name:                      "Custom",
		BaseURL:                   serverURL + "/v1",
		WireAPI:                   model.WireAPIResponses,
		SupportsWebsockets:        true,
		WebsocketConnectTimeoutMS: &timeout,
	}
}

// newRawWebsocketServer accepts a WebSocket upgrade and hands the raw
// connection to afterUpgrade so tests can send frames coder/websocket's server
// helper would refuse to produce.
func newRawWebsocketServer(t *testing.T, afterUpgrade func(net.Conn, *bufio.ReadWriter)) *httptest.Server {
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
		key := r.Header.Get("Sec-WebSocket-Key")
		sum := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
		accept := base64.StdEncoding.EncodeToString(sum[:])
		_, _ = fmt.Fprintf(buffered, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", accept)
		if err := buffered.Flush(); err != nil {
			_ = conn.Close()
			return
		}
		afterUpgrade(conn, buffered)
		// Give the client time to consume the frame before the socket closes.
		time.Sleep(100 * time.Millisecond)
		_ = conn.Close()
	}))
	t.Cleanup(server.Close)
	return server
}
