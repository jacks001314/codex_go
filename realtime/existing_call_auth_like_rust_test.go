package realtime

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// existingCallHeaderRecorder is a fake observer endpoint that records the
// handshake headers of every connection it accepts.
type existingCallHeaderRecorder struct {
	mu      sync.Mutex
	headers []http.Header
}

func (r *existingCallHeaderRecorder) server(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		r.mu.Lock()
		r.headers = append(r.headers, request.Header.Clone())
		r.mu.Unlock()
		conn, err := websocket.Accept(writer, request, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		<-request.Context().Done()
	}))
	t.Cleanup(server.Close)
	return server
}

func (r *existingCallHeaderRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.headers)
}

func (r *existingCallHeaderRecorder) first(t *testing.T) http.Header {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		r.mu.Lock()
		var header http.Header
		if len(r.headers) > 0 {
			header = r.headers[0]
		}
		r.mu.Unlock()
		if header != nil || time.Now().After(deadline) {
			return header
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// observerBackend mirrors Rust #62114's private observer: the model provider
// snapshot carries inference credentials while the runtime hook resolves the
// host's own external headers on each handshake.
func observerBackend(sidebandBaseURL string) *TransportBackendConfig {
	return &TransportBackendConfig{
		SidebandBaseURL: sidebandBaseURL,
		Headers: http.Header{
			"Authorization":          []string{"Bearer inference-only-key"},
			"X-Model-Provider-Trace": []string{"model-provider"},
		},
		RuntimeAuthHeaders: func(context.Context, string) (http.Header, error) {
			return http.Header{"X-Runtime-Auth": []string{"observer"}}, nil
		},
	}
}

func startObserverSideband(t *testing.T, backend *TransportBackendConfig, threadID string, transport *StartTransport, auth ExistingCallAuth) error {
	t.Helper()
	manager := NewManager()
	manager.SetTransportBackend(backend)
	manager.SetNotificationSink(func(Notification) {})
	version := VersionV3
	includeStartupContext := false
	realtimeSessionID := "observer-session"
	_, _, err := manager.StartWithOptions(&StartParams{
		ThreadID:              threadID,
		OutputModality:        OutputAudio,
		Version:               &version,
		Transport:             transport,
		IncludeStartupContext: &includeStartupContext,
		RealtimeSessionID:     &realtimeSessionID,
	}, &StartOptions{Attachment: &AttachmentOptions{ExistingCallAuth: auth}})
	if err == nil {
		t.Cleanup(func() { _, _, _ = manager.Stop(&StopParams{ThreadID: threadID}, "requested") })
	}
	return err
}

// TestExistingCallAuthModelProviderKeepsModelProviderCredentialsLikeRust pins
// Rust #62114's default: a host that does not select external headers keeps the
// model provider's snapshot, so its inference credentials still travel.
func TestExistingCallAuthModelProviderKeepsModelProviderCredentialsLikeRust(t *testing.T) {
	recorder := &existingCallHeaderRecorder{}
	server := recorder.server(t)

	err := startObserverSideband(t, observerBackend(server.URL), "thread-auth-model-provider",
		ExistingCallTransport("rtc_model_provider"), ExistingCallAuthModelProvider)
	if err != nil {
		t.Fatalf("start existing call: %v", err)
	}

	header := recorder.first(t)
	if header == nil {
		t.Fatal("the sideband never connected")
	}
	if got := header.Get("X-Model-Provider-Trace"); got != "model-provider" {
		t.Fatalf("X-Model-Provider-Trace = %q, want the model provider snapshot", got)
	}
	if got := header.Get("X-Runtime-Auth"); got != "observer" {
		t.Fatalf("X-Runtime-Auth = %q, want the host's external headers", got)
	}
}

// TestExistingCallAuthExternalHeadersDropsModelProviderCredentialsLikeRust
// pins Rust #62114: a host-selected observer authenticates with its own
// external headers, so the model provider's snapshot never reaches it while the
// realtime session headers still do.
func TestExistingCallAuthExternalHeadersDropsModelProviderCredentialsLikeRust(t *testing.T) {
	recorder := &existingCallHeaderRecorder{}
	server := recorder.server(t)

	err := startObserverSideband(t, observerBackend(server.URL), "thread-auth-external-headers",
		ExistingCallTransportWithSideband("rtc_existing", server.URL), ExistingCallAuthExternalHeaders)
	if err != nil {
		t.Fatalf("start existing call: %v", err)
	}

	header := recorder.first(t)
	if header == nil {
		t.Fatal("the sideband never connected")
	}
	for name, values := range header {
		switch http.CanonicalHeaderKey(name) {
		case "Authorization", "X-Model-Provider-Trace":
			t.Fatalf("model provider credential %q reached the observer: %v", name, values)
		}
	}
	if got := header.Get("X-Runtime-Auth"); got != "observer" {
		t.Fatalf("X-Runtime-Auth = %q, want the host's external headers", got)
	}
	if got := header.Get("x-session-id"); got != "observer-session" {
		t.Fatalf("x-session-id = %q, want the realtime session header", got)
	}
}

// TestExistingCallAuthExternalHeadersRequiresExplicitSidebandURLLikeRust pins
// Rust #62114's admission check: external headers only suit a call whose
// sideband endpoint the host named itself, and are rejected before a handshake.
func TestExistingCallAuthExternalHeadersRequiresExplicitSidebandURLLikeRust(t *testing.T) {
	cases := map[string]*StartTransport{
		"configured sideband": ExistingCallTransport("rtc_no_override"),
		"websocket transport": {Type: "websocket"},
	}
	for name, transport := range cases {
		t.Run(name, func(t *testing.T) {
			recorder := &existingCallHeaderRecorder{}
			server := recorder.server(t)

			err := startObserverSideband(t, observerBackend(server.URL), "thread-auth-no-url", transport, ExistingCallAuthExternalHeaders)
			if !errors.Is(err, ErrInvalidRealtimeRequest) {
				t.Fatalf("error = %v, want ErrInvalidRealtimeRequest", err)
			}
			if !strings.Contains(err.Error(), "requires an existing call with an explicit sideband URL") {
				t.Fatalf("error = %v, want the explicit sideband URL requirement", err)
			}
			if recorder.count() != 0 {
				t.Fatalf("the sideband dialed %d times, want 0", recorder.count())
			}
		})
	}
}

// TestExistingCallAuthExternalHeadersRequiresHeaderAuthenticationLikeRust pins
// Rust #62114: without header-based authentication there is no external
// credential to present, and the model provider's headers must not substitute
// for it.
func TestExistingCallAuthExternalHeadersRequiresHeaderAuthenticationLikeRust(t *testing.T) {
	recorder := &existingCallHeaderRecorder{}
	server := recorder.server(t)

	backend := observerBackend(server.URL)
	backend.RuntimeAuthHeaders = nil
	err := startObserverSideband(t, backend, "thread-auth-no-headers",
		ExistingCallTransportWithSideband("rtc_no_header_auth", server.URL), ExistingCallAuthExternalHeaders)
	if !errors.Is(err, ErrInvalidRealtimeRequest) {
		t.Fatalf("error = %v, want ErrInvalidRealtimeRequest", err)
	}
	if !strings.Contains(err.Error(), "requires header-based authentication") {
		t.Fatalf("error = %v, want the header-based authentication requirement", err)
	}
	if recorder.count() != 0 {
		t.Fatalf("the sideband dialed %d times, want 0", recorder.count())
	}
}
