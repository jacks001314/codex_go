package network

import (
	"context"
	"crypto/tls"
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
)

type fakeProxyAuthSession struct {
	schemeName string
	initial    []byte
	challenges [][]byte
	responses  [][]byte
	index      int
	released   bool
}

func (s *fakeProxyAuthSession) scheme() string { return s.schemeName }

func (s *fakeProxyAuthSession) initialToken() []byte { return s.initial }

func (s *fakeProxyAuthSession) update(challenge []byte) ([]byte, error) {
	if s.index >= len(s.responses) {
		return nil, errors.New("unexpected proxy challenge")
	}
	if s.index < len(s.challenges) && string(challenge) != string(s.challenges[s.index]) {
		return nil, fmt.Errorf("challenge = %q, want %q", challenge, s.challenges[s.index])
	}
	token := s.responses[s.index]
	s.index++
	return token, nil
}

func (s *fakeProxyAuthSession) release() { s.released = true }

func TestParseProxyAuthenticateSchemesAndTokens(t *testing.T) {
	header := http.Header{}
	header.Add("Proxy-Authenticate", `Negotiate, NTLM `+base64.StdEncoding.EncodeToString([]byte("challenge")))
	header.Add("Proxy-Authenticate", `Basic realm="corp, proxy"`)

	challenges := parseProxyAuthenticate(header)
	if len(challenges) != 3 {
		t.Fatalf("challenges = %#v, want 3", challenges)
	}
	if challenges[0].scheme != "Negotiate" || len(challenges[0].token) != 0 {
		t.Fatalf("challenges[0] = %#v", challenges[0])
	}
	if challenges[1].scheme != "NTLM" || string(challenges[1].token) != "challenge" {
		t.Fatalf("challenges[1] = %#v", challenges[1])
	}
	if challenges[2].scheme != "Basic" {
		t.Fatalf("challenges[2] = %#v", challenges[2])
	}

	selected, ok := selectProxyChallenge(challenges)
	if !ok || selected.scheme != "Negotiate" {
		t.Fatalf("selected = %#v, ok = %v", selected, ok)
	}
}

func TestDialProxyTunnelAuthenticatesNegotiateChallengeLikeRust(t *testing.T) {
	proxy := startConnectProxyServer(t, false, false, func(round int, authorization string) (int, string) {
		switch round {
		case 0:
			if authorization != "" {
				t.Errorf("round 0 Authorization = %q, want empty", authorization)
			}
			return http.StatusProxyAuthRequired, "Negotiate"
		case 1:
			if want := "Negotiate " + base64.StdEncoding.EncodeToString([]byte("token-1")); authorization != want {
				t.Errorf("round 1 Authorization = %q, want %q", authorization, want)
			}
			return http.StatusProxyAuthRequired, "Negotiate " + base64.StdEncoding.EncodeToString([]byte("challenge-1"))
		case 2:
			if want := "Negotiate " + base64.StdEncoding.EncodeToString([]byte("token-2")); authorization != want {
				t.Errorf("round 2 Authorization = %q, want %q", authorization, want)
			}
			return http.StatusOK, ""
		default:
			t.Errorf("unexpected CONNECT round %d", round)
			return http.StatusBadRequest, ""
		}
	})
	defer proxy.Close()

	session := &fakeProxyAuthSession{
		schemeName: "Negotiate",
		initial:    []byte("token-1"),
		challenges: [][]byte{[]byte("challenge-1")},
		responses:  [][]byte{[]byte("token-2")},
	}
	factory := func(scheme string, proxyHost string) (proxyAuthSession, error) {
		if scheme != "Negotiate" {
			t.Fatalf("scheme = %q, want Negotiate", scheme)
		}
		if proxyHost != "127.0.0.1" {
			t.Fatalf("proxyHost = %q, want 127.0.0.1", proxyHost)
		}
		return session, nil
	}

	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatalf("parse proxy URL: %v", err)
	}
	conn, err := dialProxyTunnel(context.Background(), proxyURL, "example.com:443", nil, factory, nil)
	if err != nil {
		t.Fatalf("dialProxyTunnel: %v", err)
	}
	defer conn.Close()
	if !session.released {
		t.Fatal("proxy auth session was not released")
	}

	if _, err := io.WriteString(conn, "ping"); err != nil {
		t.Fatalf("write tunneled bytes: %v", err)
	}
	echo := make([]byte, 4)
	if _, err := io.ReadFull(conn, echo); err != nil {
		t.Fatalf("read tunneled bytes: %v", err)
	}
	if string(echo) != "ping" {
		t.Fatalf("echo = %q, want ping", echo)
	}
}

func TestDialProxyTunnelRejectsUnsupportedProxyAuthLikeRust(t *testing.T) {
	proxy := startConnectProxyServer(t, false, false, func(round int, authorization string) (int, string) {
		if round != 0 {
			t.Errorf("unexpected CONNECT round %d", round)
		}
		if authorization != "" {
			t.Errorf("Authorization = %q, want empty", authorization)
		}
		return http.StatusProxyAuthRequired, `Basic realm="corp"`
	})
	defer proxy.Close()

	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatalf("parse proxy URL: %v", err)
	}
	_, err = dialProxyTunnel(context.Background(), proxyURL, "example.com:443", nil, nil, nil)
	if err == nil {
		t.Fatal("dialProxyTunnel: want a proxy authentication error")
	}
	if !IsProxyAuthenticationRequired(err) {
		t.Fatalf("error = %v, want ProxyAuthenticationRequired", err)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "proxy authentication required") {
		t.Fatalf("error = %v, want the net/http-compatible challenge text", err)
	}
}

func TestProxyAuthTransportServesOriginOverAuthenticatedTunnel(t *testing.T) {
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "origin ok")
	}))
	defer origin.Close()

	proxy := startConnectProxyServer(t, false, true, func(round int, authorization string) (int, string) {
		switch round {
		case 0:
			return http.StatusProxyAuthRequired, "Negotiate"
		case 1:
			if want := "Negotiate " + base64.StdEncoding.EncodeToString([]byte("token-1")); authorization != want {
				t.Errorf("Authorization = %q, want %q", authorization, want)
			}
			return http.StatusOK, ""
		default:
			t.Errorf("unexpected CONNECT round %d", round)
			return http.StatusBadRequest, ""
		}
	})
	defer proxy.Close()

	client := newProxyAuthTestClient(t, origin, proxy, func(scheme string, proxyHost string) (proxyAuthSession, error) {
		return &fakeProxyAuthSession{schemeName: "Negotiate", initial: []byte("token-1")}, nil
	})
	response, err := client.Get(origin.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if response.StatusCode != http.StatusOK || string(body) != "origin ok" {
		t.Fatalf("response = %d %q, want 200 %q", response.StatusCode, body, "origin ok")
	}
}

func TestProxyAuthTransportKeepsOriginProxyAuthResponseLikeRust(t *testing.T) {
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Proxy-Authenticate", "Negotiate")
		w.WriteHeader(http.StatusProxyAuthRequired)
		_, _ = io.WriteString(w, "origin denied")
	}))
	defer origin.Close()

	proxy := startConnectProxyServer(t, false, true, func(round int, authorization string) (int, string) {
		if round != 0 {
			t.Errorf("unexpected CONNECT round %d", round)
		}
		return http.StatusOK, ""
	})
	defer proxy.Close()

	client := newProxyAuthTestClient(t, origin, proxy, nil)
	response, err := client.Get(origin.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusProxyAuthRequired {
		t.Fatalf("StatusCode = %d, want 407", response.StatusCode)
	}
	if got := response.Header.Get("Proxy-Authenticate"); got != "Negotiate" {
		t.Fatalf("Proxy-Authenticate = %q, want Negotiate", got)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != "origin denied" {
		t.Fatalf("body = %q, want origin denied", body)
	}
}

func TestProxyAuthTransportUsesTLSProxy(t *testing.T) {
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "tls proxy ok")
	}))
	defer origin.Close()

	proxy := startConnectProxyServer(t, true, true, func(round int, authorization string) (int, string) {
		if round != 0 {
			t.Errorf("unexpected CONNECT round %d", round)
		}
		return http.StatusOK, ""
	})
	defer proxy.Close()

	client := newProxyAuthTestClient(t, origin, proxy, nil)
	response, err := client.Get(origin.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if response.StatusCode != http.StatusOK || string(body) != "tls proxy ok" {
		t.Fatalf("response = %d %q, want 200 %q", response.StatusCode, body, "tls proxy ok")
	}
}

func newProxyAuthTestClient(t *testing.T, origin *httptest.Server, proxy *httptest.Server, factory proxyAuthFactory) *http.Client {
	t.Helper()
	base := http.DefaultTransport.(*http.Transport).Clone()
	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatalf("parse proxy URL: %v", err)
	}
	base.Proxy = http.ProxyURL(proxyURL)
	base.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	roundTripper := newProxyAuthTransport(base)
	if factory != nil {
		roundTripper.authFactory = factory
	}
	return &http.Client{Transport: roundTripper, Timeout: 5 * time.Second}
}

func startConnectProxyServer(t *testing.T, useTLS bool, relay bool, respond func(round int, authorization string) (int, string)) *httptest.Server {
	t.Helper()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			t.Errorf("method = %s, want CONNECT", r.Method)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			t.Errorf("response writer does not support hijacking")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		conn, buffered, err := hijacker.Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		defer conn.Close()
		request := r
		reader := buffered.Reader
		for round := 0; ; round++ {
			if round > 0 {
				request, err = http.ReadRequest(reader)
				if err != nil {
					return
				}
			}
			status, challenge := respond(round, request.Header.Get("Proxy-Authorization"))
			if status == http.StatusOK {
				_, _ = io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n")
				if !relay {
					_, _ = io.Copy(conn, reader)
					return
				}
				target, err := net.Dial("tcp", request.Host)
				if err != nil {
					t.Errorf("relay dial %s: %v", request.Host, err)
					return
				}
				defer target.Close()
				go func() {
					_, _ = io.Copy(target, reader)
					_ = target.(*net.TCPConn).CloseWrite()
				}()
				_, _ = io.Copy(conn, target)
				return
			}
			fmt.Fprintf(conn, "HTTP/1.1 %d %s\r\n", status, http.StatusText(status))
			if challenge != "" {
				fmt.Fprintf(conn, "Proxy-Authenticate: %s\r\n", challenge)
			}
			_, _ = io.WriteString(conn, "Content-Length: 0\r\n\r\n")
		}
	})
	server := httptest.NewUnstartedServer(handler)
	if useTLS {
		server.StartTLS()
	} else {
		server.Start()
	}
	return server
}

func TestProxyConnectErrorTextMatchesNetHTTP(t *testing.T) {
	err := &ProxyConnectError{StatusCode: http.StatusProxyAuthRequired, Cause: errors.New("sspi failed")}
	if got := err.Error(); !strings.Contains(got, "Proxy Authentication Required") || !strings.Contains(got, "sspi failed") {
		t.Fatalf("Error() = %q", got)
	}
	if !IsProxyAuthenticationRequired(fmt.Errorf("wrapped: %w", err)) {
		t.Fatal("wrapped ProxyConnectError was not recognized")
	}
}

func TestProxyAuthTransportReportsProxyChallengeAsError(t *testing.T) {
	proxy := startConnectProxyServer(t, false, false, func(round int, authorization string) (int, string) {
		if round != 0 {
			t.Errorf("unexpected CONNECT round %d", round)
		}
		return http.StatusProxyAuthRequired, `Basic realm="corp"`
	})
	defer proxy.Close()

	base := http.DefaultTransport.(*http.Transport).Clone()
	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatalf("parse proxy URL: %v", err)
	}
	base.Proxy = http.ProxyURL(proxyURL)
	client := &http.Client{
		Transport: newProxyAuthTransport(base),
		Timeout:   5 * time.Second,
	}
	_, err = client.Get("https://example.com/")
	if err == nil {
		t.Fatal("Get: want a proxy authentication error")
	}
	if !IsProxyAuthenticationRequired(err) {
		t.Fatalf("error = %v, want ProxyAuthenticationRequired", err)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "proxy authentication required") {
		t.Fatalf("error = %v, want the doctor classifier text", err)
	}
}

func TestCanAuthenticateProxyRoute(t *testing.T) {
	tests := []struct {
		name     string
		proxyURL string
		want     bool
	}{
		{name: "http proxy", proxyURL: "http://proxy.example.com:3128", want: true},
		{name: "https proxy", proxyURL: "https://proxy.example.com:3128", want: true},
		{name: "proxy credentials", proxyURL: "http://user:secret@proxy.example.com:3128", want: false},
		{name: "socks proxy", proxyURL: "socks5://proxy.example.com:1080", want: false},
		{name: "missing host", proxyURL: "http:///", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parsed, err := url.Parse(test.proxyURL)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if got := canAuthenticateProxyRoute(parsed); got != test.want {
				t.Fatalf("canAuthenticateProxyRoute(%q) = %v, want %v", test.proxyURL, got, test.want)
			}
		})
	}
}
