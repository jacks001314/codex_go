package tcptunnel

// Go parity for Rust's tcp-tunnel ssh_tests.rs and the SSH half of lib_tests.rs
// (Rust #60568): route selection, credential boundaries, binary stdio relay,
// half-close propagation, gateway EOF, rejection by status, and ERS v1/v2 tunnel
// JWTs for Portals listeners.

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"

	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
)

// sshTestProxy is a local HTTP/3 endpoint with a scripted CONNECT handler.
type sshTestProxy struct {
	addr  string
	roots *x509.CertPool
}

func newSSHTestProxy(t *testing.T, handler http.HandlerFunc) *sshTestProxy {
	t.Helper()
	certificate, roots := selfSignedCertificate(t)
	packet, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP() error = %v", err)
	}
	listener, err := quic.Listen(packet, &tls.Config{
		Certificates: []tls.Certificate{certificate},
		NextProtos:   []string{"h3"},
	}, &quic.Config{})
	if err != nil {
		t.Fatalf("quic.Listen() error = %v", err)
	}
	server := &http3.Server{Handler: handler}
	go func() {
		for {
			conn, err := listener.Accept(context.Background())
			if err != nil {
				return
			}
			go func() { _ = server.ServeQUICConn(conn) }()
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		_ = packet.Close()
	})
	return &sshTestProxy{addr: packet.LocalAddr().String(), roots: roots}
}

// target is the CONNECT authority pair for this proxy, and session is the
// transport a test drives directly (RunSSHProxy only trusts the built-in
// gateway, so relay coverage goes through bridgeStreams like Rust's tests).
func (p *sshTestProxy) target(t *testing.T) *ProxyTarget {
	t.Helper()
	proxyURL, err := url.Parse("https://" + p.addr)
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}
	target, err := ParseProxyTarget(proxyURL, []string{"https://" + p.addr}, "127.0.0.1:22")
	if err != nil {
		t.Fatalf("ParseProxyTarget() error = %v", err)
	}
	return target
}

func (p *sshTestProxy) session(t *testing.T) *session {
	t.Helper()
	created := newSession(p.target(t))
	created.tlsConfig = &tls.Config{NextProtos: []string{"h3"}, RootCAs: p.roots}
	created.transport.TLSClientConfig = created.tlsConfig
	// Exercise the segmentation-offload-free dial path everywhere.
	created.noGSO = true
	return created
}

// TestSSHDestinationRoutesByGatewayAuthorityAndPublishedPort mirrors Rust's
// connect_routes_by_gateway_authority_and_published_port.
func TestSSHDestinationRoutesByGatewayAuthorityAndPublishedPort(t *testing.T) {
	named, err := ParseArgs([]string{"--environment-id", "ccarenv_test-1", "--port", "2222"})
	if err != nil {
		t.Fatalf("ParseArgs(named) error = %v", err)
	}
	explicit, err := ParseArgs([]string{"--proxy-url", PublicGateway, "--target", "ccarenv_test-1:2222"})
	if err != nil {
		t.Fatalf("ParseArgs(explicit) error = %v", err)
	}
	namedDestination, err := named.SSHDestination()
	if err != nil {
		t.Fatalf("SSHDestination(named) error = %v", err)
	}
	explicitDestination, err := explicit.SSHDestination()
	if err != nil {
		t.Fatalf("SSHDestination(explicit) error = %v", err)
	}
	if *namedDestination.Target != *explicitDestination.Target {
		t.Fatalf("named target = %#v, explicit = %#v", *namedDestination.Target, *explicitDestination.Target)
	}
	for name, want := range map[string]string{
		"x-portals-auth-mode":      "oauth",
		"x-portals-environment-id": "ccarenv_test-1",
		"x-portals-port":           "2222",
	} {
		if got := namedDestination.Headers.Get(name); got != want {
			t.Fatalf("%s = %q, want %q", name, got, want)
		}
		if got := explicitDestination.Headers.Get(name); got != want {
			t.Fatalf("explicit %s = %q, want %q", name, got, want)
		}
	}
	if namedDestination.Target.Authority != "codex-portals.api.openai.com:443" {
		t.Fatalf("authority = %q", namedDestination.Target.Authority)
	}
	bearer, err := oauthBearer("local-fixture==")
	if err != nil {
		t.Fatalf("oauthBearer() error = %v", err)
	}
	if bearer != "Bearer local-fixture==" {
		t.Fatalf("oauthBearer() = %q", bearer)
	}
}

// TestSSHEnvironmentIDDefaultsToPort22 mirrors Rust's
// environment_id_defaults_to_ssh_and_rejects_ambiguous_routes.
func TestSSHEnvironmentIDDefaultsToPort22(t *testing.T) {
	named, err := ParseArgs([]string{"--environment-id", "ccarenv_test"})
	if err != nil {
		t.Fatalf("ParseArgs() error = %v", err)
	}
	destination, err := named.SSHDestination()
	if err != nil {
		t.Fatalf("SSHDestination() error = %v", err)
	}
	if got := destination.Headers.Get("x-portals-port"); got != "22" {
		t.Fatalf("x-portals-port = %q, want 22", got)
	}
	for _, argv := range [][]string{
		{"--environment-id", "env", "--port", "0"},
		{"--environment-id", "env", "--port", "65536"},
		{"--environment-id", "env", "--target", "other:22"},
		{"--environment-id", "env", "--auth-token-stdin", "--proxy-origins-file", "origins"},
		{"--target", "env:22"},
	} {
		if _, err := ParseArgs(argv); err == nil {
			t.Fatalf("ParseArgs(%v) succeeded, want error", argv)
		}
	}
}

// TestSSHRejectsInvalidRoutesBeforeReadingOAuth mirrors Rust's
// invalid_routes_fail_before_reading_oauth_or_writing_stdout.
func TestSSHRejectsInvalidRoutesBeforeReadingOAuth(t *testing.T) {
	for _, testCase := range []struct{ proxyURL, target string }{
		{PublicGateway, ":22"},
		{PublicGateway, "env"},
		{PublicGateway, "env:0"},
		{PublicGateway, "env:65536"},
		{PublicGateway, "id\r\nother: value:22"},
		{"https://codex-portals.api.openai.com.bad", "valid:22"},
		{"https://codex-portals.api.openai.com:4443", "valid:22"},
	} {
		args, err := ParseArgs([]string{"--proxy-url", testCase.proxyURL, "--target", testCase.target})
		if err != nil {
			t.Fatalf("ParseArgs(%q, %q) error = %v", testCase.proxyURL, testCase.target, err)
		}
		var output bytes.Buffer
		err = runSSHProxy(context.Background(), args, func(context.Context) (string, error) {
			t.Fatalf("unexpected credential lookup for %q/%q", testCase.proxyURL, testCase.target)
			return "", nil
		}, strings.NewReader(""), &output, newSession)
		if err == nil {
			t.Fatalf("runSSHProxy(%q, %q) error = nil", testCase.proxyURL, testCase.target)
		}
		if output.Len() != 0 {
			t.Fatalf("runSSHProxy(%q, %q) wrote %q", testCase.proxyURL, testCase.target, output.String())
		}
	}
}

// TestSSHRejectsInvalidOAuthCredentialsWithoutEchoingThem mirrors Rust's
// rejects_invalid_oauth_credentials_without_echoing_them.
func TestSSHRejectsInvalidOAuthCredentialsWithoutEchoingThem(t *testing.T) {
	for _, token := range []string{"", "=", "private=secret", "private\r\nvalue", "private secret"} {
		_, err := oauthBearer(token)
		if err == nil || err.Error() != "invalid OAuth access token" {
			t.Fatalf("oauthBearer(%q) error = %v", token, err)
		}
		if strings.Contains(err.Error(), "private") {
			t.Fatalf("oauthBearer(%q) echoed the credential: %v", token, err)
		}
	}
}

// TestSSHStdioRelaysBinaryAndPropagatesHalfClose mirrors the first half of
// Rust's stdio_propagates_half_close_and_exits_on_gateway_eof_or_rejection.
func TestSSHStdioRelaysBinaryAndPropagatesHalfClose(t *testing.T) {
	received := make(chan string, 1)
	proxy := newSSHTestProxy(t, func(w http.ResponseWriter, r *http.Request) {
		flusher, _ := w.(http.Flusher)
		w.WriteHeader(http.StatusOK)
		if flusher != nil {
			flusher.Flush()
		}
		_, _ = w.Write([]byte("\x00\xff"))
		if flusher != nil {
			flusher.Flush()
		}
		body, _ := io.ReadAll(r.Body)
		received <- string(body)
		_, _ = w.Write([]byte("after FIN"))
	})
	session := proxy.session(t)
	defer session.close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := session.connect(ctx); err != nil {
		t.Fatalf("connect() error = %v", err)
	}
	var output bytes.Buffer
	if err := bridgeStreams(ctx, session, strings.NewReader("request"), &output, session.target, http.Header{}, "Bearer fixture", bridgeProxyCommand); err != nil {
		t.Fatalf("bridgeStreams() error = %v", err)
	}
	if got := <-received; got != "request" {
		t.Fatalf("gateway read %q, want request", got)
	}
	if want := "\x00\xffafter FIN"; output.String() != want {
		t.Fatalf("output = %q, want %q", output.String(), want)
	}
}

// TestSSHExitsOnGatewayEOFWhileStdinStaysOpen mirrors Rust's remote-EOF case:
// OpenSSH still holds stdin open, yet the relay must finish and report the
// bytes the gateway sent.
func TestSSHExitsOnGatewayEOFWhileStdinStaysOpen(t *testing.T) {
	proxy := newSSHTestProxy(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("bye"))
	})
	session := proxy.session(t)
	defer session.close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := session.connect(ctx); err != nil {
		t.Fatalf("connect() error = %v", err)
	}
	stdinReader, stdinWriter := io.Pipe()
	defer stdinWriter.Close()
	var output bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- bridgeStreams(ctx, session, stdinReader, &output, session.target, http.Header{}, "Bearer fixture", bridgeProxyCommand)
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("bridgeStreams() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("bridgeStreams() did not exit on gateway EOF while stdin stayed open")
	}
	if output.String() != "bye" {
		t.Fatalf("output = %q, want bye", output.String())
	}
}

// TestSSHReportsRejectedConnectByStatusOnly mirrors Rust's rejection case: the
// peer's error text never reaches the caller.
func TestSSHReportsRejectedConnectByStatusOnly(t *testing.T) {
	proxy := newSSHTestProxy(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("peer secret text"))
	})
	session := proxy.session(t)
	defer session.close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := session.connect(ctx); err != nil {
		t.Fatalf("connect() error = %v", err)
	}
	var output bytes.Buffer
	err := bridgeStreams(ctx, session, strings.NewReader(""), &output, session.target, http.Header{}, "Bearer fixture", bridgeProxyCommand)
	if err == nil {
		t.Fatal("bridgeStreams() error = nil, want rejection")
	}
	if err.Error() != "MASQUE CONNECT rejected with status 401" {
		t.Fatalf("error = %q, want the HTTP status only", err.Error())
	}
	if strings.Contains(err.Error(), "peer secret") || output.Len() != 0 {
		t.Fatalf("rejection leaked peer text: %v / %q", err, output.String())
	}
	if mapped := sshStreamError(err); mapped == nil || mapped.Error() != err.Error() {
		t.Fatalf("sshStreamError() = %v, want the rejection", mapped)
	}
}

const (
	ersJWTHeader = "eyJ0eXAiOiJjY2EtdHVubmVsK2p3dCJ9"
	ersJWTv1     = ersJWTHeader + ".eyJ2ZXIiOjF9.fixture"
	ersJWTv2     = ersJWTHeader + ".eyJ2ZXIiOjJ9.fixture"
)

// TestPortalsListenerTunnelVersionsLikeRust covers startup and renewal (#61260).
func TestPortalsListenerTunnelVersionsLikeRust(t *testing.T) {
	for _, first := range []string{ersJWTv1, ersJWTv2} {
		for _, renewed := range []string{ersJWTv1, ersJWTv2} {
			input := newControlInput(bufioReader(first+"\n"+renewed+"\n"), false, PublicGateway)
			if metadata := <-input.metadata; metadata.err != nil {
				t.Fatal(metadata.err)
			}
			for _, want := range []string{first, renewed} {
				got := <-input.tokens
				if got.err != nil || got.auth != "Bearer "+want {
					t.Fatalf("token = %#v, want fixture %q", got, want)
				}
			}
		}
	}
}

// TestTunnelCredentialRejectsMalformedVersionsLikeRust keeps invalid claims rejected.
func TestTunnelCredentialRejectsMalformedVersionsLikeRust(t *testing.T) {
	for _, claims := range []string{`{"ver":3}`, `{"ver":"1"}`, `{"ver":"2"}`, `{"ver":1.0}`, `{"ver":2e0}`, `{}`, `null`, `invalid-json`} {
		token := ersJWTHeader + "." + base64.RawURLEncoding.EncodeToString([]byte(claims)) + ".fixture"
		for _, mode := range []TunnelCredential{CredentialBearer, CredentialErsTunnelJWT} {
			if _, err := mode.Header(token); err == nil {
				t.Errorf("accepted claims %s in mode %v", claims, mode)
			}
		}
	}
	for _, token := range []string{ersJWTv1, ersJWTv2} {
		parts := strings.Split(token, ".")
		for _, invalid := range []string{parts[0] + "." + parts[1], parts[0] + "." + parts[1] + ".", token + ".extra"} {
			if _, err := CredentialErsTunnelJWT.Header(invalid); err == nil {
				t.Errorf("accepted malformed fixture %q", invalid)
			}
		}
	}
	for _, invalid := range []string{"opaque", "malformed.parts", "eyJ0eXAiOiJKV1QifQ.eyJ2ZXIiOjF9.fixture"} {
		if _, err := CredentialErsTunnelJWT.Header(invalid); err == nil {
			t.Errorf("accepted non-ERS fixture %q", invalid)
		}
	}
}

func TestOtherBearerModesAcceptErsVersionsLikeRust(t *testing.T) {
	unrelated := "eyJ0eXAiOiJKV1QifQ.eyJ2ZXIiOjF9.fixture"
	for _, tc := range []struct{ origin, metadata string }{
		{PublicGateway, `[["x-portals-auth-mode","oauth"]]`},
		{"https://proxy.example", "[]"},
	} {
		input := newControlInput(bufioReader(tc.metadata+"\n"+unrelated+"\n"+ersJWTv1+"\n"+ersJWTv2+"\n"), true, tc.origin)
		if metadata := <-input.metadata; metadata.err != nil {
			t.Fatal(metadata.err)
		}
		for _, want := range []string{unrelated, ersJWTv1, ersJWTv2} {
			got := <-input.tokens
			if got.err != nil || got.auth != "Bearer "+want {
				t.Fatalf("token = %#v, want fixture %q", got, want)
			}
		}
	}
}

// bufioReader builds the buffered control stream the tunnel reads.
func bufioReader(value string) *bufio.Reader { return bufio.NewReader(strings.NewReader(value)) }
