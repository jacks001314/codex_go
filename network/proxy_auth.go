package network

// Authenticated CONNECT tunnels for outbound HTTP proxies.
//
// Rust #55994: when a Windows proxy rejects a CONNECT with 407, libcurl is
// pointed at a local socket and negotiates Negotiate/NTLM with the signed-in
// user before handing the authenticated connection back to the caller. Go's
// net/http transport has no hook for replacing a rejected CONNECT tunnel, so
// this file owns the CONNECT handshake and exposes the accepted connection
// through an http.RoundTripper.
//
// The helper is only used for HTTPS targets routed through an HTTP or HTTPS
// proxy (the same route shape as Rust's can_authenticate_route). Plain HTTP
// requests and proxies with URL credentials keep using net/http's built-in
// proxy handling.

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	proxyAuthMaxRounds      = 5
	proxyAuthMaxErrorBody   = 1 << 20
	proxyAuthRouteCacheSize = 32
)

// ProxyConnectError reports a proxy CONNECT failure. StatusCode is the status
// returned by the proxy for a non-200 CONNECT (0 when no response was read).
type ProxyConnectError struct {
	StatusCode int
	Cause      error
}

func (e *ProxyConnectError) Error() string {
	if e == nil {
		return "proxyconnect tcp: proxy CONNECT failed"
	}
	status := "proxy CONNECT failed"
	if e.StatusCode != 0 {
		status = http.StatusText(e.StatusCode)
		if status == "" {
			status = fmt.Sprintf("status %d", e.StatusCode)
		}
	}
	message := "proxyconnect tcp: " + status
	if e.Cause != nil {
		message += ": " + e.Cause.Error()
	}
	return message
}

func (e *ProxyConnectError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// IsProxyAuthenticationRequired reports whether err is a proxy CONNECT
// challenge. It mirrors Rust's RouteFailureClass::ProxyAuthenticationRequired:
// only a failed CONNECT identifies a proxy challenge; a 407 returned by the
// origin over an established tunnel is an ordinary response.
func IsProxyAuthenticationRequired(err error) bool {
	var connectErr *ProxyConnectError
	return errors.As(err, &connectErr) && connectErr.StatusCode == http.StatusProxyAuthRequired
}

// proxyAuthSession is one client-side Negotiate/NTLM conversation. initial is
// the first token to send after the proxy advertises a scheme; update consumes
// the proxy's next challenge token.
type proxyAuthSession interface {
	scheme() string
	initialToken() []byte
	update(challenge []byte) ([]byte, error)
	release()
}

type proxyAuthFactory func(scheme string, proxyHost string) (proxyAuthSession, error)

type proxyChallenge struct {
	scheme string
	token  []byte
}

// proxyAuthTransport routes HTTPS requests through an authenticated CONNECT
// tunnel when net/http would otherwise only fail with a 407. It keeps one
// transport per (proxy, target) pair so pooled connections never cross
// tunnels, matching Rust's route-aware client pool.
type proxyAuthTransport struct {
	base        *http.Transport
	authFactory proxyAuthFactory

	mu     sync.Mutex
	routes map[string]*proxyAuthRouteTransport
	order  []string
}

type proxyAuthRouteTransport struct {
	transport *http.Transport
	lastUsed  time.Time
}

func newProxyAuthTransport(base *http.Transport) *proxyAuthTransport {
	return &proxyAuthTransport{
		base:        base,
		authFactory: newProxyAuthSession,
		routes:      make(map[string]*proxyAuthRouteTransport),
	}
}

func (t *proxyAuthTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if t == nil || t.base == nil {
		return nil, errors.New("proxy auth transport has no base transport")
	}
	if request == nil || request.URL == nil || request.URL.Scheme != "https" {
		return t.base.RoundTrip(request)
	}
	proxyURL, err := t.proxyFor(request)
	if err != nil {
		return nil, err
	}
	if !canAuthenticateProxyRoute(proxyURL) {
		return t.base.RoundTrip(request)
	}
	target, err := connectTarget(request.URL)
	if err != nil {
		return nil, err
	}
	return t.transportFor(proxyURL, target).RoundTrip(request)
}

// CloseIdleConnections releases every per-route pool. http.Client calls this
// through the CloseIdleConnections interface.
func (t *proxyAuthTransport) CloseIdleConnections() {
	t.mu.Lock()
	routes := t.routes
	t.routes = make(map[string]*proxyAuthRouteTransport)
	t.order = nil
	t.mu.Unlock()
	for _, route := range routes {
		route.transport.CloseIdleConnections()
	}
}

func (t *proxyAuthTransport) proxyFor(request *http.Request) (*url.URL, error) {
	if t.base.Proxy == nil {
		return nil, nil
	}
	return t.base.Proxy(request)
}

func (t *proxyAuthTransport) transportFor(proxyURL *url.URL, target string) *http.Transport {
	key := proxyURL.String() + "|" + target
	now := time.Now()
	t.mu.Lock()
	if route, ok := t.routes[key]; ok {
		route.lastUsed = now
		transport := route.transport
		t.mu.Unlock()
		return transport
	}
	transport := t.newRouteTransport(proxyURL, target)
	t.routes[key] = &proxyAuthRouteTransport{transport: transport, lastUsed: now}
	t.order = append(t.order, key)
	var evicted *http.Transport
	for len(t.order) > proxyAuthRouteCacheSize {
		oldest := t.order[0]
		t.order = t.order[1:]
		if route, ok := t.routes[oldest]; ok {
			evicted = route.transport
			delete(t.routes, oldest)
		}
	}
	t.mu.Unlock()
	if evicted != nil {
		evicted.CloseIdleConnections()
	}
	return transport
}

func (t *proxyAuthTransport) newRouteTransport(proxyURL *url.URL, target string) *http.Transport {
	transport := t.base.Clone()
	transport.Proxy = nil
	transport.DialTLSContext = nil
	dialContext := transport.DialContext
	transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
		return dialProxyTunnel(ctx, proxyURL, target, t.proxyTLSConfig(), t.authFactory, dialContext)
	}
	return transport
}

func (t *proxyAuthTransport) proxyTLSConfig() *tls.Config {
	if t.base.TLSClientConfig == nil {
		return &tls.Config{}
	}
	return t.base.TLSClientConfig.Clone()
}

func canAuthenticateProxyRoute(proxyURL *url.URL) bool {
	if proxyURL == nil || proxyURL.Hostname() == "" || proxyURL.User != nil {
		return false
	}
	return proxyURL.Scheme == "http" || proxyURL.Scheme == "https"
}

func connectTarget(target *url.URL) (string, error) {
	host := target.Hostname()
	if host == "" {
		return "", errors.New("proxy CONNECT target has no host")
	}
	port := target.Port()
	if port == "" {
		port = "443"
	}
	return net.JoinHostPort(host, port), nil
}

// dialProxyTunnel opens a connection to proxyURL, issues CONNECT for target,
// and answers 407 Negotiate/NTLM challenges with authFactory. The returned
// connection is positioned at the start of the tunneled byte stream.
func dialProxyTunnel(ctx context.Context, proxyURL *url.URL, target string, tlsConfig *tls.Config, authFactory proxyAuthFactory, dialContext func(context.Context, string, string) (net.Conn, error)) (net.Conn, error) {
	if proxyURL == nil {
		return nil, errors.New("proxy CONNECT requires a proxy URL")
	}
	address := proxyURL.Host
	if proxyURL.Port() == "" {
		switch proxyURL.Scheme {
		case "http":
			address = net.JoinHostPort(proxyURL.Hostname(), "80")
		case "https":
			address = net.JoinHostPort(proxyURL.Hostname(), "443")
		}
	}
	if dialContext == nil {
		dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
		dialContext = dialer.DialContext
	}
	conn, err := dialContext(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}
	stopCancel := context.AfterFunc(ctx, func() {
		_ = conn.Close()
	})
	defer stopCancel()

	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
		defer func() { _ = conn.SetDeadline(time.Time{}) }()
	}

	if proxyURL.Scheme == "https" {
		config := &tls.Config{}
		if tlsConfig != nil {
			config = tlsConfig.Clone()
		}
		config.ServerName = proxyURL.Hostname()
		// Proxy CONNECT authentication is an HTTP/1.1 exchange.
		config.NextProtos = []string{"http/1.1"}
		tunnel := tls.Client(conn, config)
		if err := tunnel.HandshakeContext(ctx); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("proxy TLS handshake: %w", err)
		}
		conn = tunnel
	}

	tunnel, err := authenticateProxyTunnel(ctx, conn, target, proxyURL.Hostname(), authFactory)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	return tunnel, nil
}

func authenticateProxyTunnel(ctx context.Context, conn net.Conn, target string, proxyHost string, authFactory proxyAuthFactory) (net.Conn, error) {
	reader := bufio.NewReader(conn)
	if err := writeProxyConnect(conn, target, nil); err != nil {
		return nil, err
	}
	response, err := readProxyConnectResponse(reader)
	if err != nil {
		return nil, err
	}
	if response.StatusCode == http.StatusOK {
		return &proxyTunnelConn{Conn: conn, reader: reader}, nil
	}
	if response.StatusCode != http.StatusProxyAuthRequired {
		drainConnectResponse(response)
		return nil, &ProxyConnectError{StatusCode: response.StatusCode}
	}

	challenges := parseProxyAuthenticate(response.Header)
	challenge, ok := selectProxyChallenge(challenges)
	drainConnectResponse(response)
	if !ok {
		return nil, &ProxyConnectError{StatusCode: http.StatusProxyAuthRequired}
	}
	if authFactory == nil {
		authFactory = newProxyAuthSession
	}
	session, err := authFactory(challenge.scheme, proxyHost)
	if err != nil {
		return nil, &ProxyConnectError{StatusCode: http.StatusProxyAuthRequired, Cause: err}
	}
	defer session.release()

	token := session.initialToken()
	for round := 0; round < proxyAuthMaxRounds; round++ {
		if err := writeProxyConnect(conn, target, &proxyChallenge{scheme: session.scheme(), token: token}); err != nil {
			return nil, err
		}
		response, err = readProxyConnectResponse(reader)
		if err != nil {
			return nil, err
		}
		if response.StatusCode == http.StatusOK {
			return &proxyTunnelConn{Conn: conn, reader: reader}, nil
		}
		if response.StatusCode != http.StatusProxyAuthRequired {
			drainConnectResponse(response)
			return nil, &ProxyConnectError{StatusCode: response.StatusCode}
		}
		next, ok := proxyChallengeToken(parseProxyAuthenticate(response.Header), session.scheme())
		drainConnectResponse(response)
		if !ok {
			return nil, &ProxyConnectError{StatusCode: http.StatusProxyAuthRequired}
		}
		token, err = session.update(next)
		if err != nil {
			return nil, &ProxyConnectError{StatusCode: http.StatusProxyAuthRequired, Cause: err}
		}
	}
	return nil, &ProxyConnectError{StatusCode: http.StatusProxyAuthRequired, Cause: errors.New("proxy authentication did not complete")}
}

func writeProxyConnect(writer io.Writer, target string, challenge *proxyChallenge) error {
	var request strings.Builder
	fmt.Fprintf(&request, "CONNECT %s HTTP/1.1\r\nHost: %s\r\nUser-Agent: Go-http-client/1.1\r\nProxy-Connection: Keep-Alive\r\n", target, target)
	if challenge != nil && challenge.scheme != "" {
		fmt.Fprintf(&request, "Proxy-Authorization: %s", challenge.scheme)
		if len(challenge.token) > 0 {
			fmt.Fprintf(&request, " %s", base64.StdEncoding.EncodeToString(challenge.token))
		}
		request.WriteString("\r\n")
	}
	request.WriteString("\r\n")
	_, err := io.WriteString(writer, request.String())
	return err
}

func readProxyConnectResponse(reader *bufio.Reader) (*http.Response, error) {
	request := &http.Request{Method: http.MethodConnect, URL: &url.URL{}}
	return http.ReadResponse(reader, request)
}

func drainConnectResponse(response *http.Response) {
	if response == nil || response.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, proxyAuthMaxErrorBody))
	_ = response.Body.Close()
}

// parseProxyAuthenticate returns every scheme/token pair advertised by the
// proxy. A single header may carry several comma-separated challenges.
func parseProxyAuthenticate(header http.Header) []proxyChallenge {
	var challenges []proxyChallenge
	for _, value := range header.Values("Proxy-Authenticate") {
		for _, part := range splitAuthenticateChallenges(value) {
			fields := strings.Fields(part)
			if len(fields) == 0 {
				continue
			}
			challenge := proxyChallenge{scheme: canonicalAuthScheme(fields[0])}
			if len(fields) > 1 {
				if token, err := base64.StdEncoding.DecodeString(fields[1]); err == nil {
					challenge.token = token
				}
			}
			challenges = append(challenges, challenge)
		}
	}
	return challenges
}

func splitAuthenticateChallenges(value string) []string {
	var parts []string
	start := 0
	quoted := false
	escaped := false
	for index, char := range value {
		switch {
		case escaped:
			escaped = false
		case char == '\\' && quoted:
			escaped = true
		case char == '"':
			quoted = !quoted
		case char == ',' && !quoted:
			parts = append(parts, strings.TrimSpace(value[start:index]))
			start = index + 1
		}
	}
	parts = append(parts, strings.TrimSpace(value[start:]))
	return parts
}

func canonicalAuthScheme(scheme string) string {
	switch strings.ToLower(strings.TrimSpace(scheme)) {
	case "negotiate":
		return "Negotiate"
	case "ntlm":
		return "NTLM"
	default:
		return scheme
	}
}

func selectProxyChallenge(challenges []proxyChallenge) (proxyChallenge, bool) {
	for _, wanted := range []string{"Negotiate", "NTLM"} {
		for _, challenge := range challenges {
			if strings.EqualFold(challenge.scheme, wanted) {
				return challenge, true
			}
		}
	}
	return proxyChallenge{}, false
}

func proxyChallengeToken(challenges []proxyChallenge, scheme string) ([]byte, bool) {
	for _, challenge := range challenges {
		if strings.EqualFold(challenge.scheme, scheme) {
			return challenge.token, true
		}
	}
	return nil, false
}

// proxyTunnelConn preserves bytes already read into the CONNECT response
// parser. It is a no-op wrapper when the proxy sent no lookahead.
type proxyTunnelConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *proxyTunnelConn) Read(p []byte) (int, error) {
	return c.reader.Read(p)
}

var _ http.RoundTripper = (*proxyAuthTransport)(nil)
