package execserver

import (
	"net/http"
	"strings"
	"testing"
)

// Rust parity: codex-exec-server RemoteEnvironmentOptions::into_transport_params
// (#47648): caller-supplied executor headers require wss:// or a loopback
// destination, may not override the connection's own upgrade headers, and are
// validated as header names and values.
func TestNormalizeExecServerHeadersLikeRust(t *testing.T) {
	headers := http.Header{"Authorization": []string{"Bearer private-token"}}
	for _, testCase := range []struct {
		name string
		url  string
		ok   bool
	}{
		{name: "wss", url: "wss://executor.example/exec", ok: true},
		{name: "ipv4 loopback", url: "ws://127.0.0.1:8080/exec", ok: true},
		{name: "ipv6 loopback", url: "ws://[::1]:8080/exec", ok: true},
		{name: "localhost", url: "ws://localhost:8080/exec", ok: true},
		{name: "plain remote ws", url: "ws://executor.example/exec"},
		{name: "http", url: "http://executor.example/exec"},
		{name: "loopback suffix is not a loopback host", url: "ws://localhost.example.test/exec"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			normalized, err := NormalizeHeaders(testCase.url, headers)
			if testCase.ok {
				if err != nil {
					t.Fatalf("NormalizeHeaders(%s) error = %v", testCase.url, err)
				}
				if got := normalized.Get("Authorization"); got != "Bearer private-token" {
					t.Fatalf("Authorization = %q", got)
				}
				return
			}
			if err == nil {
				t.Fatalf("NormalizeHeaders(%s) error = nil, want a rejection", testCase.url)
			}
			if !strings.Contains(err.Error(), "wss:// or a loopback destination") {
				t.Fatalf("error = %v, want Rust's secure-transport message", err)
			}
			if strings.Contains(err.Error(), "private-token") {
				t.Fatalf("error leaked a header value: %v", err)
			}
		})
	}

	// No headers stay nil so the unauthenticated path is unchanged.
	if normalized, err := NormalizeHeaders("ws://executor.example/exec", nil); err != nil || normalized != nil {
		t.Fatalf("NormalizeHeaders(nil) = %#v, %v", normalized, err)
	}
}

func TestNormalizeExecServerHeadersRejectsUnsafeHeadersLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		headers http.Header
		want    string
	}{
		{
			name:    "connection is controlled by the client",
			headers: http.Header{"Connection": []string{"keep-alive"}},
			want:    "controlled by the connection",
		},
		{
			name:    "host is controlled by the client",
			headers: http.Header{"Host": []string{"other.example"}},
			want:    "controlled by the connection",
		},
		{
			name:    "websocket handshake headers are controlled by the client",
			headers: http.Header{"Sec-WebSocket-Key": []string{"abc"}},
			want:    "controlled by the connection",
		},
		{
			name:    "invalid name",
			headers: http.Header{"Bad Header": []string{"value"}},
			want:    "invalid exec-server WebSocket header name",
		},
		{
			name:    "invalid value",
			headers: http.Header{"Authorization": []string{"Bearer \x01bad"}},
			want:    "invalid value for exec-server WebSocket header",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := NormalizeHeaders("wss://executor.example/exec", testCase.headers)
			if err == nil {
				t.Fatalf("NormalizeHeaders() error = nil, want a rejection")
			}
			if !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("error = %v, want %q", err, testCase.want)
			}
		})
	}
}

// TestRedactedExecServerHeadersLikeRust mirrors Rust marking header values
// sensitive: diagnostics name the headers without echoing their values.
func TestRedactedExecServerHeadersLikeRust(t *testing.T) {
	rendered := RedactedExecServerHeaders(http.Header{
		"Authorization": []string{"Bearer private-token"},
		"X-Tenant":      []string{"acme"},
	})
	if strings.Contains(rendered, "private-token") || strings.Contains(rendered, "acme") {
		t.Fatalf("redacted headers leaked a value: %s", rendered)
	}
	for _, want := range []string{"Authorization=[redacted]", "X-Tenant=[redacted]"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("redacted headers = %q, missing %q", rendered, want)
		}
	}
	if got := RedactedExecServerHeaders(nil); got != "{}" {
		t.Fatalf("RedactedExecServerHeaders(nil) = %q", got)
	}
}
