package tcptunnel

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
)

// localHTTP3Proxy is a minimal HTTP/3 CONNECT proxy used to exercise the
// tunnel transport end to end. It records the authorization and extension
// headers of every CONNECT and echoes the tunneled bytes back.
type localHTTP3Proxy struct {
	addr     string
	roots    *x509.CertPool
	server   *http3.Server
	packet   net.PacketConn
	listener *quic.Listener
	requests chan recordedConnect

	mu    sync.Mutex
	conns []*quic.Conn
}

type recordedConnect struct {
	authorization string
	route         string
}

func newLocalHTTP3Proxy(t *testing.T) *localHTTP3Proxy {
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
	requests := make(chan recordedConnect, 16)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		requests <- recordedConnect{
			authorization: r.Header.Get("Authorization"),
			route:         r.Header.Get("X-Test-Route"),
		}
		flusher, _ := w.(http.Flusher)
		w.WriteHeader(http.StatusOK)
		if flusher != nil {
			flusher.Flush()
		}
		if _, err := w.Write([]byte("ready")); err != nil {
			return
		}
		if flusher != nil {
			flusher.Flush()
		}
		buffer := make([]byte, 4096)
		for {
			count, err := r.Body.Read(buffer)
			if count > 0 {
				if _, writeErr := w.Write(buffer[:count]); writeErr != nil {
					return
				}
				if flusher != nil {
					flusher.Flush()
				}
			}
			if err != nil {
				return
			}
		}
	})
	proxy := &localHTTP3Proxy{
		addr:     packet.LocalAddr().String(),
		roots:    roots,
		server:   &http3.Server{Handler: handler},
		packet:   packet,
		listener: listener,
		requests: requests,
	}
	go func() {
		for {
			conn, err := listener.Accept(context.Background())
			if err != nil {
				return
			}
			proxy.mu.Lock()
			proxy.conns = append(proxy.conns, conn)
			proxy.mu.Unlock()
			go func() { _ = proxy.server.ServeQUICConn(conn) }()
		}
	}()
	t.Cleanup(proxy.close)
	return proxy
}

func (p *localHTTP3Proxy) close() {
	_ = p.listener.Close()
	_ = p.packet.Close()
}

// killConnections drops every live QUIC connection, simulating a proxy that
// went away.
func (p *localHTTP3Proxy) killConnections() {
	p.mu.Lock()
	conns := append([]*quic.Conn(nil), p.conns...)
	p.mu.Unlock()
	for _, conn := range conns {
		_ = conn.CloseWithError(0, "test interruption")
	}
}

func (p *localHTTP3Proxy) trustedOrigin() string {
	return "https://" + p.addr
}

func (p *localHTTP3Proxy) sessionFactory(t *testing.T) sessionFactory {
	return func(target *ProxyTarget) *session {
		if target.Host != "127.0.0.1" {
			t.Fatalf("session target host = %q", target.Host)
		}
		created := newSession(target)
		created.tlsConfig = &tls.Config{NextProtos: []string{"h3"}, RootCAs: p.roots}
		created.transport.TLSClientConfig = created.tlsConfig
		return created
	}
}

func proxyTargetForTest(t *testing.T, proxy *localHTTP3Proxy) *ProxyTarget {
	t.Helper()
	proxyURL, err := url.Parse(proxy.trustedOrigin())
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}
	target, err := ParseProxyTarget(proxyURL, []string{proxy.trustedOrigin()}, "127.0.0.1:22")
	if err != nil {
		t.Fatalf("ParseProxyTarget() error = %v", err)
	}
	return target
}

// TestTransportCarriesAuthenticatedConnectAndReconnectsWithoutReplay mirrors
// the essential half of Rust's
// `transport_reconnect_keeps_listener_and_does_not_replay_old_tcp_streams`:
// every local connection issues its own authenticated CONNECT, bearer updates
// apply to new connections, and a lost transport reconnects without replaying
// the interrupted TCP stream.
func TestTransportCarriesAuthenticatedConnectAndReconnectsWithoutReplay(t *testing.T) {
	proxy := newLocalHTTP3Proxy(t)
	target := proxyTargetForTest(t, proxy)

	session := proxy.sessionFactory(t)(target)
	defer session.close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	listener, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("ListenTCP() error = %v", err)
	}
	defer listener.Close()

	auth := &authState{}
	auth.store("Bearer local-test")
	headers, err := ReadConnectHeaders(bufio.NewReader(strings.NewReader("[[\"x-test-route\",\"custom-route\"]]\n")))
	if err != nil {
		t.Fatalf("ReadConnectHeaders() error = %v", err)
	}
	connectCtx, cancelConnect := context.WithTimeout(ctx, 10*time.Second)
	err = session.connect(connectCtx)
	cancelConnect()
	if err != nil {
		t.Fatalf("session.connect() error = %v", err)
	}

	served := make(chan error, 1)
	go func() { served <- serve(ctx, listener, target, session, headers, auth, io.Discard) }()

	dial := func() net.Conn {
		t.Helper()
		conn, err := net.DialTimeout("tcp", listener.Addr().String(), 5*time.Second)
		if err != nil {
			t.Fatalf("dial listener: %v", err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		return conn
	}
	readReady := func(conn net.Conn) {
		t.Helper()
		buffer := make([]byte, 5)
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		if _, err := io.ReadFull(conn, buffer); err != nil {
			t.Fatalf("read readiness: %v", err)
		}
		if string(buffer) != "ready" {
			t.Fatalf("readiness = %q, want %q", string(buffer), "ready")
		}
	}
	expectConnect := func(wantAuth string) {
		t.Helper()
		select {
		case recorded := <-proxy.requests:
			if recorded.authorization != wantAuth || recorded.route != "custom-route" {
				t.Fatalf("CONNECT headers = %#v, want auth %q", recorded, wantAuth)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("proxy did not receive the CONNECT for %q", wantAuth)
		}
	}

	first := dial()
	readReady(first)
	expectConnect("Bearer local-test")

	// The tunnel is bidirectional.
	if _, err := first.Write([]byte("ping")); err != nil {
		t.Fatalf("write tunneled bytes: %v", err)
	}
	echo := make([]byte, 4)
	_ = first.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.ReadFull(first, echo); err != nil || string(echo) != "ping" {
		t.Fatalf("echo = %q, err = %v", string(echo), err)
	}

	// A renewal applies to connections opened after it, never to the live one.
	auth.store("Bearer replacement-test")
	second := dial()
	readReady(second)
	expectConnect("Bearer replacement-test")

	// Losing the transport kills the live stream and reconnects; the
	// interrupted stream is never replayed.
	proxy.killConnections()
	_ = first.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := first.Read(make([]byte, 1)); err == nil {
		t.Fatal("interrupted stream stayed open")
	}

	deadline := time.Now().Add(30 * time.Second)
	var recovered net.Conn
	for time.Now().Before(deadline) {
		candidate, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
		if err != nil {
			time.Sleep(100 * time.Millisecond)
			continue
		}
		_ = candidate.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		buffer := make([]byte, 5)
		if _, err := io.ReadFull(candidate, buffer); err == nil && string(buffer) == "ready" {
			recovered = candidate
			break
		}
		_ = candidate.Close()
		time.Sleep(100 * time.Millisecond)
	}
	if recovered == nil {
		t.Fatal("listener did not recover after the transport was lost")
	}
	defer recovered.Close()

	cancel()
	select {
	case <-served:
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not stop after cancellation")
	}
}

// TestRunReportsReadinessAndForwardsControlInput drives the whole command
// surface against the local proxy: stdin supplies the bearer and extension
// headers, stdout reports the readiness line, and a renewal is acknowledged.
func TestRunReportsReadinessAndForwardsControlInput(t *testing.T) {
	proxy := newLocalHTTP3Proxy(t)
	originsFile := filepath.Join(t.TempDir(), "origins.txt")
	if err := os.WriteFile(originsFile, []byte(proxy.trustedOrigin()+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	args := &Args{
		ProxyURL:              proxy.trustedOrigin(),
		ProxyOriginsFile:      originsFile,
		Target:                "127.0.0.1:22",
		ListenAddr:            "127.0.0.1:0",
		AuthTokenStdin:        true,
		AuthTokenUpdatesStdin: true,
		ConnectHeadersStdin:   true,
	}
	stdinReader, stdinWriter := io.Pipe()
	stdoutReader, stdoutWriter := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErr := make(chan error, 1)
	go func() {
		runErr <- run(ctx, args, stdinReader, stdoutWriter, io.Discard, proxy.sessionFactory(t))
	}()
	if _, err := io.WriteString(stdinWriter, "[[\"x-test-route\",\"custom-route\"]]\nfirst-secret\n"); err != nil {
		t.Fatalf("writing control input: %v", err)
	}

	lines := bufio.NewReader(stdoutReader)
	line, err := lines.ReadString('\n')
	if err != nil {
		t.Fatalf("reading readiness: %v", err)
	}
	if !strings.HasPrefix(line, "LISTENING 127.0.0.1:") {
		t.Fatalf("readiness line = %q", line)
	}
	address := strings.TrimSpace(strings.TrimPrefix(line, "LISTENING "))
	conn, err := net.DialTimeout("tcp", address, 5*time.Second)
	if err != nil {
		t.Fatalf("dial listener: %v", err)
	}
	defer conn.Close()
	buffer := make([]byte, 5)
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.ReadFull(conn, buffer); err != nil || string(buffer) != "ready" {
		t.Fatalf("readiness payload = %q, err = %v", string(buffer), err)
	}
	select {
	case recorded := <-proxy.requests:
		if recorded.authorization != "Bearer first-secret" || recorded.route != "custom-route" {
			t.Fatalf("CONNECT headers = %#v", recorded)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("proxy did not receive the CONNECT")
	}

	// The second bearer line is acknowledged on stdout.
	if _, err := io.WriteString(stdinWriter, "second-secret\n"); err != nil {
		t.Fatalf("writing renewed bearer: %v", err)
	}
	ack, err := lines.ReadString('\n')
	if err != nil {
		t.Fatalf("reading auth acknowledgement: %v", err)
	}
	if strings.TrimSpace(ack) != "AUTH_UPDATED" {
		t.Fatalf("auth acknowledgement = %q", ack)
	}
	newConn, err := net.DialTimeout("tcp", address, 5*time.Second)
	if err != nil {
		t.Fatalf("dial listener: %v", err)
	}
	defer newConn.Close()
	_ = newConn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.ReadFull(newConn, buffer); err != nil {
		t.Fatalf("read renewed readiness: %v", err)
	}
	select {
	case recorded := <-proxy.requests:
		if recorded.authorization != "Bearer second-secret" {
			t.Fatalf("renewed CONNECT headers = %#v", recorded)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("proxy did not receive the renewed CONNECT")
	}

	cancel()
	select {
	case <-runErr:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancellation")
	}
}

// selfSignedCertificate returns a localhost certificate and the pool that
// trusts it.
func selfSignedCertificate(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:              []string{"127.0.0.1", "localhost"},
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1)},
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("CreateCertificate() error = %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("MarshalECPrivateKey() error = %v", err)
	}
	certificate, err := tls.X509KeyPair(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}),
	)
	if err != nil {
		t.Fatalf("X509KeyPair() error = %v", err)
	}
	roots := x509.NewCertPool()
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("ParseCertificate() error = %v", err)
	}
	roots.AddCert(parsed)
	return certificate, roots
}
