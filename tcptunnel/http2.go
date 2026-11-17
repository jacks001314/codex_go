// TLS HTTP/2 CONNECT transport for the same trusted proxy origin as HTTP/3.
// Each connection carries one CONNECT and preserves TCP half-close.

package tcptunnel

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
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/http/httpproxy"
	"golang.org/x/net/http2"
)

// proxyIntercept is the system HTTPS proxy for the gateway origin.
type proxyIntercept struct {
	uri  *url.URL
	auth string
}

// systemProxy mirrors hyper-util's `Matcher::from_system().intercept()`:
// HTTPS_PROXY/all_proxy with NO_PROXY, then the OS configuration when the
// environment is silent. A CGI environment never uses a proxy, and a proxy
// scheme this tunnel cannot speak is treated as no proxy at all.
func systemProxy(target *ProxyTarget) (*proxyIntercept, error) {
	if os.Getenv("REQUEST_METHOD") != "" {
		return nil, nil
	}
	gateway, err := gatewayURI(target)
	if err != nil {
		return nil, err
	}
	proxyText := firstEnv("HTTPS_PROXY", "https_proxy")
	if proxyText == "" {
		proxyText = firstEnv("ALL_PROXY", "all_proxy")
	}
	noProxy := firstEnv("NO_PROXY", "no_proxy")
	if proxyText == "" {
		proxyText, noProxy = systemProxySetting(noProxy)
	}
	if proxyText == "" {
		return nil, nil
	}
	intercept, err := parseProxyIntercept(proxyText)
	if err != nil {
		return nil, nil
	}
	if noProxy != "" {
		config := httpproxy.Config{HTTPSProxy: intercept.uri.String(), NoProxy: noProxy}
		selected, err := config.ProxyFunc()(gateway)
		if err != nil || selected == nil {
			return nil, nil
		}
	}
	return intercept, nil
}

func firstEnv(names ...string) string {
	for _, name := range names {
		if value := os.Getenv(name); value != "" {
			return value
		}
	}
	return ""
}

// parseProxyIntercept mirrors `parse_env_uri`: a missing scheme means HTTP,
// only http(s) proxies are usable here, and userinfo becomes a Basic
// Proxy-Authorization value.
func parseProxyIntercept(text string) (*proxyIntercept, error) {
	raw := strings.TrimSpace(text)
	if raw == "" {
		return nil, errors.New("empty proxy")
	}
	if !tcpTunnelSchemePrefix.MatchString(raw) {
		raw = "http://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return nil, errors.New("invalid proxy")
	}
	switch parsed.Scheme {
	case "http", "https":
	default:
		return nil, errors.New("unsupported proxy scheme")
	}
	auth := ""
	if parsed.User != nil {
		user := parsed.User.Username()
		password, _ := parsed.User.Password()
		auth = "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+password))
	}
	parsed.User = nil
	parsed.Path = "/"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return &proxyIntercept{uri: parsed, auth: auth}, nil
}

func gatewayURI(target *ProxyTarget) (*url.URL, error) {
	host := target.Host
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return url.Parse("https://" + host + ":" + strconv.Itoa(target.Port) + "/")
}

// h2Connection owns one TLS HTTP/2 connection to the gateway.
type h2Connection struct {
	target    *ProxyTarget
	transport *http2.Transport
	client    *http2.ClientConn
	conn      net.Conn
}

// connectH2 resolves every address, races TLS+HTTP/2 handshakes and returns the
// first usable connection. A certificate failure is reported as its own error
// when no address succeeds.
func connectH2(ctx context.Context, target *ProxyTarget, tlsConfig *tls.Config) (*h2Connection, error) {
	addresses, err := net.DefaultResolver.LookupHost(ctx, target.Host)
	if err != nil {
		return nil, err
	}
	type attempt struct {
		connection *h2Connection
		err        error
	}
	attempts := make(chan attempt, len(addresses))
	for _, address := range addresses {
		go func(ip string) {
			dialer := net.Dialer{}
			raw, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(ip, strconv.Itoa(target.Port)))
			if err != nil {
				attempts <- attempt{err: err}
				return
			}
			if tcp, ok := raw.(*net.TCPConn); ok {
				_ = tcp.SetNoDelay(true)
			}
			connection, err := handshakeH2(ctx, raw, target, tlsConfig)
			attempts <- attempt{connection: connection, err: err}
		}(address)
	}
	invalidCertificate := false
	for range addresses {
		result := <-attempts
		if result.err == nil {
			return result.connection, nil
		}
		var certificateError *tls.CertificateVerificationError
		if errors.As(result.err, &certificateError) {
			invalidCertificate = true
		}
	}
	if invalidCertificate {
		return nil, errors.New("HTTP/2 proxy certificate verification failed")
	}
	return nil, errors.New("HTTP/2 proxy connection failed")
}

// connectThroughProxy tunnels to the gateway through the system HTTPS proxy,
// then runs the same HTTP/2 handshake inside that tunnel. Only the CONNECT
// authority reaches the proxy; the gateway credential stays inside TLS to the
// gateway.
func connectThroughProxy(ctx context.Context, target *ProxyTarget, tlsConfig *tls.Config, intercept *proxyIntercept) (*h2Connection, error) {
	switch intercept.uri.Scheme {
	case "http", "https":
	default:
		return nil, errors.New("unsupported HTTPS proxy scheme")
	}
	proxyAddress := intercept.uri.Host
	if intercept.uri.Port() == "" {
		port := "80"
		if intercept.uri.Scheme == "https" {
			port = "443"
		}
		proxyAddress = net.JoinHostPort(intercept.uri.Hostname(), port)
	}
	dialer := net.Dialer{}
	raw, err := dialer.DialContext(ctx, "tcp", proxyAddress)
	if err != nil {
		return nil, err
	}
	if tcp, ok := raw.(*net.TCPConn); ok {
		_ = tcp.SetNoDelay(true)
	}
	var conn net.Conn = raw
	if intercept.uri.Scheme == "https" {
		proxyTLS := tlsConfig.Clone()
		proxyTLS.ServerName = intercept.uri.Hostname()
		// Rust clears ALPN for the proxy leg; the tunnel is an HTTP/1 CONNECT.
		proxyTLS.NextProtos = nil
		tlsConn := tls.Client(raw, proxyTLS)
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			_ = raw.Close()
			return nil, err
		}
		conn = tlsConn
	}
	authority := target.Addr()
	request := "CONNECT " + authority + " HTTP/1.1\r\nHost: " + authority + "\r\n"
	if intercept.auth != "" {
		request += "Proxy-Authorization: " + intercept.auth + "\r\n"
	}
	request += "\r\n"
	if _, err := io.WriteString(conn, request); err != nil {
		_ = conn.Close()
		return nil, err
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodConnect})
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if response.Body != nil {
		_ = response.Body.Close()
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		_ = conn.Close()
		return nil, fmt.Errorf("proxy CONNECT rejected with status %d", response.StatusCode)
	}
	return handshakeH2(ctx, &bufferedConn{Conn: conn, reader: reader}, target, tlsConfig)
}

// bufferedConn preserves bytes the HTTP/1 proxy response reader may have
// buffered past the CONNECT headers.
type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.reader.Read(p) }

// handshakeH2 requires ALPN `h2` and waits for the peer's SETTINGS to admit a
// stream before any credential is sent.
func handshakeH2(ctx context.Context, raw net.Conn, target *ProxyTarget, tlsConfig *tls.Config) (*h2Connection, error) {
	config := tlsConfig.Clone()
	config.ServerName = target.Host
	config.NextProtos = []string{"h2"}
	tlsConn := tls.Client(raw, config)
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		_ = raw.Close()
		return nil, err
	}
	if tlsConn.ConnectionState().NegotiatedProtocol != "h2" {
		_ = tlsConn.Close()
		return nil, errors.New("proxy did not negotiate HTTP/2")
	}
	transport := &http2.Transport{
		MaxHeaderListSize: maxFieldSectionSize,
	}
	client, err := transport.NewClientConn(tlsConn)
	if err != nil {
		_ = tlsConn.Close()
		return nil, err
	}
	for {
		state := client.State()
		if state.Closed {
			_ = client.Close()
			return nil, errors.New("HTTP/2 proxy closed during setup")
		}
		if state.MaxConcurrentStreams > 0 {
			break
		}
		select {
		case <-ctx.Done():
			_ = client.Close()
			return nil, ctx.Err()
		case <-time.After(2 * time.Millisecond):
		}
	}
	return &h2Connection{target: target, transport: transport, client: client, conn: tlsConn}, nil
}

func (c *h2Connection) close() {
	if c.client != nil {
		_ = c.client.Close()
	}
	if c.conn != nil {
		_ = c.conn.Close()
	}
}

// admit sends the one authenticated CONNECT and waits for a successful
// response. A rejection resets the stream before returning.
func (c *h2Connection) admit(ctx context.Context, spec requestSpec) (*streamTunnel, error) {
	bodyReader, bodyWriter := io.Pipe()
	request := c.target.request(spec)
	request.URL = &url.URL{Scheme: "https", Host: c.target.Addr()}
	request.Body = bodyReader
	request = request.WithContext(ctx)
	response, err := c.client.RoundTrip(request)
	if err != nil {
		_ = bodyWriter.CloseWithError(err)
		return nil, streamError()
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		_ = bodyWriter.CloseWithError(errors.New("CONNECT rejected"))
		_ = response.Body.Close()
		return nil, rejection(response.StatusCode)
	}
	return &streamTunnel{
		body:    response.Body,
		upload:  bodyWriter,
		cleanup: c.close,
	}, nil
}
