package tcptunnel

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
)

type countingListener struct {
	net.Listener
	accepts *atomic.Int32
}

func (l *countingListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err == nil {
		l.accepts.Add(1)
	}
	return conn, err
}

// startDualGateway serves HTTP/3 and HTTP/2 on one port, like the Rust
// transport tests, and counts accepted TCP connections.
func startDualGateway(t *testing.T, h3Handler, h2Handler http.Handler) (string, *x509.CertPool, *atomic.Int32) {
	t.Helper()
	certificate, roots := selfSignedCertificate(t)
	packet, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("ListenUDP() error = %v", err)
	}
	port := packet.LocalAddr().(*net.UDPAddr).Port
	tcpListener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		_ = packet.Close()
		t.Fatalf("Listen(tcp:%d) error = %v", port, err)
	}
	quicListener, err := quic.Listen(packet, &tls.Config{
		Certificates: []tls.Certificate{certificate},
		NextProtos:   []string{"h3"},
	}, &quic.Config{})
	if err != nil {
		t.Fatalf("quic.Listen() error = %v", err)
	}
	h3Server := &http3.Server{Handler: h3Handler}
	go func() {
		for {
			conn, err := quicListener.Accept(context.Background())
			if err != nil {
				return
			}
			go func() { _ = h3Server.ServeQUICConn(conn) }()
		}
	}()
	accepts := &atomic.Int32{}
	h2Server := &http.Server{
		Handler: h2Handler,
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{certificate},
			NextProtos:   []string{"h2"},
		},
	}
	go func() { _ = h2Server.ServeTLS(&countingListener{Listener: tcpListener, accepts: accepts}, "", "") }()
	t.Cleanup(func() {
		_ = h2Server.Close()
		_ = quicListener.Close()
		_ = packet.Close()
	})
	return fmt.Sprintf("127.0.0.1:%d", port), roots, accepts
}

func requestFixture(context.Context) (requestSpec, error) {
	return requestSpec{headers: portalHeaders(), bearer: "Bearer fixture"}, nil
}

func relayEcho(t *testing.T, tunnel *streamTunnel, payload []byte) []byte {
	t.Helper()
	var echo bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- tunnel.relay(bytes.NewReader(payload), &echo) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("relay() error = %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatalf("relay() did not finish")
	}
	return echo.Bytes()
}

// TestTransportPrefersAdmittedQUICLikeRust keeps TCP untouched when QUIC
// admits the CONNECT.
func TestTransportPrefersAdmittedQUICLikeRust(t *testing.T) {
	h3Recorder := &connectRecorder{}
	address, roots, accepts := startDualGateway(t, connectEchoHandler(h3Recorder, 0), connectEchoHandler(&connectRecorder{}, 0))
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	tunnel, err := newTransportConfig(clientRoots(roots)).open(ctx, targetForProvider(t, address), requestFixture)
	if err != nil {
		t.Fatalf("open() error = %v", err)
	}
	payload := bytes.Repeat([]byte{9}, 64*1024)
	if echo := relayEcho(t, tunnel, payload); !bytes.Equal(echo, payload) {
		t.Fatalf("echo length = %d, want %d", len(echo), len(payload))
	}
	time.Sleep(300 * time.Millisecond)
	if got := accepts.Load(); got != 0 {
		t.Fatalf("TCP accepts = %d, want the QUIC preference to keep TCP closed", got)
	}
	if _, _, _, reads := h3Recorder.snapshot(); reads != len(payload) {
		t.Fatalf("HTTP/3 gateway read %d bytes, want %d", reads, len(payload))
	}
}

// TestTransportFallsBackToHTTP2WhenUDPBlockedLikeRust uses the 250 ms head
// start to admit TLS/HTTP/2 when no QUIC peer answers.
func TestTransportFallsBackToHTTP2WhenUDPBlockedLikeRust(t *testing.T) {
	gateway := startHTTP2Gateway(t, connectEchoHandler(&connectRecorder{}, 0))
	defer gateway.close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	started := time.Now()
	tunnel, err := newTransportConfig(clientRoots(gateway.roots)).open(ctx, targetForProvider(t, gateway.address), requestFixture)
	if err != nil {
		t.Fatalf("open() error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("open() took %v, want the HTTP/2 fallback well before the QUIC deadline", elapsed)
	}
	payload := bytes.Repeat([]byte{5}, 48*1024)
	if echo := relayEcho(t, tunnel, payload); !bytes.Equal(echo, payload) {
		t.Fatalf("echo length = %d, want %d", len(echo), len(payload))
	}
}

// TestTransportAdmissionRejectionIsTerminalLikeRust never falls back once the
// preferred transport received a status.
func TestTransportAdmissionRejectionIsTerminalLikeRust(t *testing.T) {
	address, roots, accepts := startDualGateway(t, connectEchoHandler(&connectRecorder{}, http.StatusForbidden), connectEchoHandler(&connectRecorder{}, 0))
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, err := newTransportConfig(clientRoots(roots)).open(ctx, targetForProvider(t, address), requestFixture)
	if err == nil || !isRejection(err) || err.Error() != "CONNECT rejected with status 403" {
		t.Fatalf("open() error = %v, want a terminal 403", err)
	}
	time.Sleep(300 * time.Millisecond)
	if got := accepts.Load(); got != 0 {
		t.Fatalf("TCP accepts = %d after a rejected admission", got)
	}
}

// TestOpenNeverLoadsCredentialsBeforeTLSLikeRust keeps the access-token lookup
// behind gateway verification.
func TestOpenNeverLoadsCredentialsBeforeTLSLikeRust(t *testing.T) {
	gateway := startHTTP2Gateway(t, connectEchoHandler(nil, 0))
	defer gateway.close()
	_, otherRoots := selfSignedCertificate(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	lookups := 0
	_, err := newTransportConfig(clientRoots(otherRoots)).open(ctx, targetForProvider(t, gateway.address), func(context.Context) (requestSpec, error) {
		lookups++
		return requestSpec{bearer: "Bearer fixture"}, nil
	})
	if err == nil {
		t.Fatalf("open() returned no error against an untrusted gateway")
	}
	if lookups != 0 {
		t.Fatalf("open() loaded credentials before TLS verification")
	}
}
