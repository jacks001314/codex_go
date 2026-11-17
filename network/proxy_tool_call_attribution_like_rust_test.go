package network

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/elazarl/goproxy"
)

func newToolCallAttributionTestProxy(t *testing.T, baseEnv map[string]string, toolCallID string) *ProxyServer {
	t.Helper()
	settings := DefaultProxySettings()
	settings.Enabled = true
	// The Rust #61860 tests run the designated upstream on loopback, which the
	// target-checked connector only allows with allow_local_binding.
	settings.AllowLocalBinding = true
	settings.ProxyURL = "http://127.0.0.1:0"
	settings.SetAllowedDomains([]string{"example.com", "127.0.0.1"})
	server, err := StartStandaloneProxy(context.Background(), ProxyConfig{
		Network:       settings,
		AuditMetadata: ProxyAuditMetadata{ProcessLog: ProxyProcessLogMetadata{ToolCallID: toolCallID}},
	}, baseEnv)
	if err != nil {
		t.Fatalf("start standalone proxy: %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })
	return server
}

// TestToolCallAttributionEndpointMatchingLikeRust covers Rust #61860's
// endpoint comparison: only the designated upstream (same host and port) gets
// the header, an unset setting stays disabled, and an empty call id disables it.
func TestToolCallAttributionEndpointMatchingLikeRust(t *testing.T) {
	attribution := newToolCallAttribution(map[string]string{
		ToolCallAttributionProxyEnv: "http://attribution.local:8080",
	}, "call-1")
	if attribution == nil {
		t.Fatal("attribution disabled with both inputs set")
	}
	if got := attribution.headerValueFor(&ProxyAddress{Host: "attribution.local", Port: "8080", Scheme: "http"}); got != "call-1" {
		t.Fatalf("matching upstream header = %q, want call-1", got)
	}
	for name, upstream := range map[string]ProxyAddress{
		"different host": {Host: "other.local", Port: "8080", Scheme: "http"},
		"different port": {Host: "attribution.local", Port: "8081", Scheme: "http"},
	} {
		if got := attribution.headerValueFor(&upstream); got != "" {
			t.Fatalf("%s received attribution %q", name, got)
		}
	}
	if got := attribution.headerValueFor(nil); got != "" {
		t.Fatalf("direct destination received attribution %q", got)
	}
	if newToolCallAttribution(map[string]string{ToolCallAttributionProxyEnv: "http://attribution.local:8080"}, "  ") != nil {
		t.Fatal("blank tool-call id enabled attribution")
	}
	if newToolCallAttribution(map[string]string{}, "call-1") != nil {
		t.Fatal("unset attribution proxy enabled attribution")
	}
	if newToolCallAttribution(map[string]string{ToolCallAttributionProxyEnv: "http://attribution.local:8080"}, "bad\nid") != nil {
		t.Fatal("control character in the tool-call id enabled attribution")
	}
	defaultPort := newToolCallAttribution(map[string]string{
		ToolCallAttributionProxyEnv: "attribution.local",
	}, "call-2")
	if got := defaultPort.headerValueFor(&ProxyAddress{Host: "attribution.local", Port: "", Scheme: "http"}); got != "call-2" {
		t.Fatalf("default-port match = %q, want call-2", got)
	}
	if got := defaultPort.headerValueFor(&ProxyAddress{Host: "attribution.local", Port: "443", Scheme: "https"}); got != "" {
		t.Fatalf("different protocol received attribution %q", got)
	}
}

// TestProxyHTTPAttributionLikeRust covers Rust #61860's plaintext HTTP path:
// the designated upstream receives the controller's tool-call id, a
// caller-supplied header never survives, and other upstreams or direct routes
// receive nothing.
func TestProxyHTTPAttributionLikeRust(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	upstreamURL, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name    string
		baseEnv map[string]string
		want    string
	}{
		{
			name: "designated upstream",
			baseEnv: map[string]string{
				"HTTP_PROXY":                upstream.URL,
				ToolCallAttributionProxyEnv: upstream.URL,
			},
			want: "call-http",
		},
		{
			name: "other upstream",
			baseEnv: map[string]string{
				"HTTP_PROXY":                upstream.URL,
				ToolCallAttributionProxyEnv: "http://other.local:8080",
			},
			want: "",
		},
		{
			name:    "direct route",
			baseEnv: map[string]string{ToolCallAttributionProxyEnv: upstream.URL},
			want:    "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := newToolCallAttributionTestProxy(t, tc.baseEnv, "call-http")
			request := httptest.NewRequest(http.MethodGet, "http://example.com/v1/models", nil)
			request.Header.Set(ToolCallAttributionHeader, "caller-supplied")
			request, response := server.handleHTTPRequest(request, &goproxy.ProxyCtx{})
			if response != nil {
				t.Fatalf("handleHTTPRequest rejected the request: %d", response.StatusCode)
			}
			if got := request.Header.Get(ToolCallAttributionHeader); got != tc.want {
				t.Fatalf("%s attribution = %q, want %q", upstreamURL.Host, got, tc.want)
			}
		})
	}
}

// TestProxyConnectAttributionLikeRust covers Rust #61860's HTTPS path: the
// attribution rides on the outer CONNECT this proxy sends to its upstream.
func TestProxyConnectAttributionLikeRust(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	captured := make(chan http.Header, 1)
	done := make(chan struct{})
	defer close(done)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		request, readErr := http.ReadRequest(bufio.NewReader(conn))
		if readErr != nil {
			return
		}
		captured <- request.Header.Clone()
		_, _ = io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n")
		<-done
	}()

	upstream := "http://" + listener.Addr().String()
	server := newToolCallAttributionTestProxy(t, map[string]string{
		"HTTPS_PROXY":               upstream,
		ToolCallAttributionProxyEnv: upstream,
	}, "call-connect")
	connectRequest := httptest.NewRequest(http.MethodConnect, "http://example.com:443", nil)
	conn, err := server.httpProxy.ConnectDialWithReq(connectRequest, "tcp", "example.com:443")
	if err != nil {
		t.Fatalf("upstream CONNECT dial: %v", err)
	}
	if conn != nil {
		_ = conn.Close()
	}
	select {
	case header := <-captured:
		if got := header.Get(ToolCallAttributionHeader); got != "call-connect" {
			t.Fatalf("outer CONNECT attribution = %q, want call-connect", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("upstream proxy did not receive a CONNECT")
	}

	other := mustParseProxyURL(t, upstream)
	if got := server.proxyConnectAttributionHeader(other); got != "call-connect" {
		t.Fatalf("matching CONNECT attribution = %q", got)
	}
	if got := server.proxyConnectAttributionHeader(mustParseProxyURL(t, "http://other.local:8080")); got != "" {
		t.Fatalf("other upstream CONNECT attribution = %q", got)
	}
}

func mustParseProxyURL(t *testing.T, value string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
