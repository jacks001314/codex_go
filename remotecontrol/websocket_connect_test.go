package remotecontrol

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestBuildRemoteControlWebsocketRequestUsesRustHeaders(t *testing.T) {
	token := "server-token"
	cursor := "cursor-123"
	enrollment := &Enrollment{
		ServerID:           "srv_e_test",
		ServerName:         "test-server",
		RemoteControlToken: &token,
	}

	request, err := BuildRemoteControlWebsocketRequest("wss://chatgpt.com/backend-api/wham/remote/control/server", enrollment, "install-1", &cursor)
	if err != nil {
		t.Fatalf("BuildRemoteControlWebsocketRequest() error = %v", err)
	}
	if request.Method != http.MethodGet || request.URL.Scheme != "wss" {
		t.Fatalf("request method/url = %s %s", request.Method, request.URL.String())
	}
	assertHeader(t, request.Header, RemoteControlServerIDHeader, "srv_e_test")
	assertHeader(t, request.Header, RemoteControlServerNameHeader, base64.StdEncoding.EncodeToString([]byte("test-server")))
	assertHeader(t, request.Header, RemoteControlProtocolVersionHeader, RemoteControlProtocolVersion)
	assertHeader(t, request.Header, "Authorization", "Bearer server-token")
	assertHeader(t, request.Header, RemoteControlInstallationIDHeader, "install-1")
	assertHeader(t, request.Header, RemoteControlSubscribeCursorHeader, cursor)
}

func TestBuildRemoteControlWebsocketRequestRejectsMissingTokenAndInvalidHeader(t *testing.T) {
	if _, err := BuildRemoteControlWebsocketRequest("wss://chatgpt.com/backend-api/wham/remote/control/server", &Enrollment{ServerID: "srv", ServerName: "name"}, "install", nil); err == nil || !strings.Contains(err.Error(), "missing remote control server token") {
		t.Fatalf("missing token error = %v", err)
	}
	token := "token"
	_, err := BuildRemoteControlWebsocketRequest("wss://chatgpt.com/backend-api/wham/remote/control/server", &Enrollment{ServerID: "bad\nserver", ServerName: "name", RemoteControlToken: &token}, "install", nil)
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("invalid header error = %v, want ErrInvalidRequest", err)
	}
}

func TestNextReconnectDelayStaysCappedLikeRust(t *testing.T) {
	var attempt uint64
	delay := nextReconnectDelayWithJitter(&attempt, 1)
	if delay != 200*time.Millisecond || attempt != 1 {
		t.Fatalf("first delay/attempt = %v/%d", delay, attempt)
	}
	delay = nextReconnectDelayWithJitter(&attempt, 1)
	if delay != 200*time.Millisecond || attempt != 2 {
		t.Fatalf("second delay/attempt = %v/%d", delay, attempt)
	}
	delay = nextReconnectDelayWithJitter(&attempt, 1)
	if delay != 400*time.Millisecond || attempt != 3 {
		t.Fatalf("third delay/attempt = %v/%d", delay, attempt)
	}
	// A sustained failure streak stays at the 30s cap and never resets the
	// attempt counter back to fast retries (Rust #49330).
	attempt = 25
	for i := 0; i < 5; i++ {
		delay = nextReconnectDelayWithJitter(&attempt, 1)
		if delay != RemoteControlReconnectBackoffCap {
			t.Fatalf("capped delay[%d] = %v, want %v", i, delay, RemoteControlReconnectBackoffCap)
		}
	}
	if attempt != 25 {
		t.Fatalf("attempt advanced while capped: %d, want 25", attempt)
	}
}

func TestWebsocketResponseReportsMissingRemoteAppServer(t *testing.T) {
	response := &http.Response{StatusCode: http.StatusNotFound}
	if !WebsocketResponseReportsMissingRemoteAppServer(response, []byte(`{"detail":"Remote app server not found"}`)) {
		t.Fatal("missing remote app server detail should be detected")
	}
	if WebsocketResponseReportsMissingRemoteAppServer(response, []byte(`{"detail":"other"}`)) {
		t.Fatal("other detail should not be detected")
	}
	if WebsocketResponseReportsMissingRemoteAppServer(&http.Response{StatusCode: http.StatusBadRequest}, []byte(`{"detail":"Remote app server not found"}`)) {
		t.Fatal("non-404 should not be detected")
	}
}

func TestFormatRemoteControlWebsocketConnectErrorIncludesHeadersAndBodyPreview(t *testing.T) {
	response := &http.Response{Header: http.Header{}}
	response.Header.Set(remoteControlRequestIDHeader, "req-1")
	response.Header.Set(remoteControlCFRayHeader, "cf-1")
	got := FormatRemoteControlWebsocketConnectError("wss://example.test/ws", response, []byte(`{"remote_control_token":"secret","detail":"nope"}`), errors.New("HTTP 401"))
	if !strings.Contains(got, "failed to connect app-server remote control websocket `wss://example.test/ws`: HTTP 401") ||
		!strings.Contains(got, "request-id: req-1, cf-ray: cf-1") ||
		strings.Contains(got, "secret") ||
		!strings.Contains(got, "redacted") {
		t.Fatalf("formatted error = %s", got)
	}
}

func assertHeader(t *testing.T, headers http.Header, name string, want string) {
	t.Helper()
	if got := headers.Get(name); got != want {
		t.Fatalf("header %s = %q, want %q", name, got, want)
	}
}
