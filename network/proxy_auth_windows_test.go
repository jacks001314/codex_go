//go:build windows

package network

import (
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestNewHTTPClientUsesProxyAuthTransportForSystemProxy(t *testing.T) {
	client := NewHTTPClient(true, 0)
	if _, ok := client.Transport.(*proxyAuthTransport); !ok {
		t.Fatalf("Transport = %T, want *proxyAuthTransport", client.Transport)
	}
	direct := NewHTTPClient(false, 0)
	if _, ok := direct.Transport.(*http.Transport); !ok {
		t.Fatalf("direct Transport = %T, want *http.Transport", direct.Transport)
	}
}

func TestWindowsProxyAuthSessionProducesInitialTokens(t *testing.T) {
	for _, scheme := range []string{"Negotiate", "NTLM"} {
		t.Run(scheme, func(t *testing.T) {
			session, err := newProxyAuthSession(scheme, "proxy.example.com")
			if err != nil {
				t.Fatalf("newProxyAuthSession: %v", err)
			}
			defer session.release()
			if session.scheme() != scheme {
				t.Fatalf("scheme = %q, want %q", session.scheme(), scheme)
			}
			if len(session.initialToken()) == 0 {
				t.Fatal("initial SSPI token is empty")
			}
		})
	}
}

func TestNewHTTPClientSystemProxyAuthenticatesLikeRust(t *testing.T) {
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "proxied")
	}))
	defer origin.Close()
	proxy := startConnectProxyServer(t, false, true, func(round int, authorization string) (int, string) {
		switch round {
		case 0:
			return http.StatusProxyAuthRequired, "Negotiate"
		case 1:
			return http.StatusOK, ""
		default:
			t.Errorf("unexpected CONNECT round %d", round)
			return http.StatusBadRequest, ""
		}
	})
	defer proxy.Close()

	client := NewHTTPClient(true, 5*time.Second)
	base := BaseTransport(client.Transport)
	if base == nil {
		t.Fatalf("Transport = %T, want a configurable transport", client.Transport)
	}
	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatalf("parse proxy URL: %v", err)
	}
	base.Proxy = http.ProxyURL(proxyURL)
	base.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	client.Transport = ReplaceBaseTransport(client.Transport, base)
	roundTripper, ok := client.Transport.(*proxyAuthTransport)
	if !ok {
		t.Fatalf("Transport = %T, want *proxyAuthTransport", client.Transport)
	}
	roundTripper.authFactory = func(scheme string, proxyHost string) (proxyAuthSession, error) {
		return &fakeProxyAuthSession{schemeName: "Negotiate", initial: []byte("token")}, nil
	}

	response, err := client.Get(origin.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if response.StatusCode != http.StatusOK || string(body) != "proxied" {
		t.Fatalf("response = %d %q, want 200 %q", response.StatusCode, body, "proxied")
	}
}
