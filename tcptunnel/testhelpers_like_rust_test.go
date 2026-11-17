package tcptunnel

import (
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
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
)

// selfSignedCertificate builds the local TLS identities used by both transport
// fixtures. The certificate doubles as its own root so tests can trust it.
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

type gatewayFixture struct {
	address string
	roots   *x509.CertPool
	close   func()
}

// startHTTP3Gateway serves CONNECT over QUIC/HTTP/3.
func startHTTP3Gateway(t *testing.T, handler http.Handler) *gatewayFixture {
	t.Helper()
	certificate, roots := selfSignedCertificate(t)
	packet, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
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
	return &gatewayFixture{
		address: packet.LocalAddr().String(),
		roots:   roots,
		close: func() {
			_ = listener.Close()
			_ = packet.Close()
		},
	}
}

// startHTTP2Gateway serves CONNECT over TLS/HTTP/2.
func startHTTP2Gateway(t *testing.T, handler http.Handler) *gatewayFixture {
	t.Helper()
	certificate, roots := selfSignedCertificate(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	server := &http.Server{
		Handler: handler,
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{certificate},
			NextProtos:   []string{"h2"},
		},
	}
	go func() { _ = server.ServeTLS(listener, "", "") }()
	return &gatewayFixture{
		address: listener.Addr().String(),
		roots:   roots,
		close: func() {
			_ = server.Close()
		},
	}
}

func targetForProvider(t *testing.T, address string) *ProxyTarget {
	t.Helper()
	host, rawPort, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatalf("SplitHostPort(%q) error = %v", address, err)
	}
	port, err := strconv.Atoi(rawPort)
	if err != nil {
		t.Fatalf("Atoi(%q) error = %v", rawPort, err)
	}
	return &ProxyTarget{Host: host, Port: port, Authority: "127.0.0.1:22"}
}

func clientRoots(roots *x509.CertPool) *tls.Config {
	return &tls.Config{RootCAs: roots}
}

// connectEchoHandler records the CONNECT and echoes tunnel bytes. A `reject`
// status answers with that status and stops reading the upload.
type connectRecorder struct {
	mu     sync.Mutex
	method string
	host   string
	header http.Header
	reads  int
}

func (r *connectRecorder) record(request *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.method = request.Method
	r.host = request.Host
	r.header = request.Header.Clone()
}

func (r *connectRecorder) snapshot() (string, string, http.Header, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.method, r.host, r.header.Clone(), r.reads
}

func connectEchoHandler(recorder *connectRecorder, reject int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if recorder != nil {
			recorder.record(r)
		}
		if r.Method != http.MethodConnect {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if reject != 0 {
			w.WriteHeader(reject)
			return
		}
		controller := http.NewResponseController(w)
		w.WriteHeader(http.StatusOK)
		_ = controller.Flush()
		buffer := make([]byte, 32*1024)
		for {
			count, err := r.Body.Read(buffer)
			if count > 0 {
				if recorder != nil {
					recorder.mu.Lock()
					recorder.reads += count
					recorder.mu.Unlock()
				}
				if _, writeErr := w.Write(buffer[:count]); writeErr != nil {
					return
				}
				_ = controller.Flush()
			}
			if err != nil {
				if err != io.EOF {
					return
				}
				return
			}
		}
	})
}
