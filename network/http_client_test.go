package network

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestNewHTTPClientCanDisableSystemProxy(t *testing.T) {
	client := NewHTTPClient(false, 0)
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("Transport = %T", client.Transport)
	}
	if transport.Proxy != nil {
		t.Fatal("Proxy is set, want nil")
	}
}

func TestNewHTTPClientCanRespectSystemProxy(t *testing.T) {
	client := NewHTTPClient(true, 0)
	transport := underlyingHTTPTransport(t, client.Transport)
	if transport.Proxy == nil {
		t.Fatal("Proxy is nil, want ProxyFromEnvironment")
	}
}

func underlyingHTTPTransport(t *testing.T, roundTripper http.RoundTripper) *http.Transport {
	t.Helper()
	switch transport := roundTripper.(type) {
	case *http.Transport:
		return transport
	case *proxyAuthTransport:
		return transport.base
	default:
		t.Fatalf("Transport = %T", roundTripper)
		return nil
	}
}

// Rust #56000: only a failed CONNECT identifies a proxy challenge. A 407 that
// comes back from the origin is an ordinary response and must reach the caller
// untouched, with its Proxy-Authenticate header and body intact.
func TestHTTPClientTreatsOriginProxyAuthStatusAsResponseLikeRust(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Proxy-Authenticate", "Negotiate")
		w.WriteHeader(http.StatusProxyAuthRequired)
		_, _ = w.Write([]byte("origin denied"))
	}))
	defer server.Close()

	client := NewHTTPClient(false, 5*time.Second)
	resp, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusProxyAuthRequired {
		t.Fatalf("StatusCode = %d, want %d", resp.StatusCode, http.StatusProxyAuthRequired)
	}
	if want := "Negotiate"; resp.Header.Get("Proxy-Authenticate") != want {
		t.Fatalf("Proxy-Authenticate = %q, want %q", resp.Header.Get("Proxy-Authenticate"), want)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != "origin denied" {
		t.Fatalf("body = %q, want %q", body, "origin denied")
	}
}

// Rust #56000: an unauthenticated CONNECT rejected with 407 stays an error on
// the request path so the caller can classify it as a proxy authentication
// failure instead of proxying through the challenge.
func TestHTTPClientReportsProxyConnectChallengeAsErrorLikeRust(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				reader := bufio.NewReader(conn)
				for {
					line, err := reader.ReadString('\n')
					if err != nil || line == "\r\n" {
						break
					}
				}
				_, _ = conn.Write([]byte("HTTP/1.1 407 Proxy Authentication Required\r\nProxy-Authenticate: Negotiate\r\nContent-Length: 0\r\n\r\n"))
			}(conn)
		}
	}()

	proxyURL := &url.URL{Scheme: "http", Host: listener.Addr().String()}
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
		Timeout:   5 * time.Second,
	}
	_, err = client.Get("https://example.com/")
	if err == nil {
		t.Fatal("Get: want an error for a rejected proxy CONNECT")
	}
	// net/http reports the challenge without the status code
	// ("Proxy Authentication Required"); that is the text the doctor probe
	// classifier keys on for Rust's ProxyAuthenticationRequired class.
	if !strings.Contains(strings.ToLower(err.Error()), "proxy authentication required") {
		t.Fatalf("error = %v, want the proxy authentication challenge", err)
	}
}
