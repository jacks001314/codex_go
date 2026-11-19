// One SSH-owned, authenticated tunnel to a verified Cloud Portals gateway.

package tcptunnel

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
)

// PublicGateway is the built-in Portals gateway. Only gateways shipped with the
// CLI may receive the signed-in account's credential.
const PublicGateway = "https://codex-portals.api.openai.com"

// GatewayOrigins lists the gateways allowed to receive OAuth credentials.
var GatewayOrigins = []string{PublicGateway}

const (
	// proxyConnectTimeout is one deadline for transport setup, credential
	// retrieval and CONNECT admission.
	proxyConnectTimeout = 10 * time.Second
	// quicHandshakeTimeout bounds each resolved QUIC peer.
	quicHandshakeTimeout = 5 * time.Second
	// quicHeadStart is how long QUIC may run before TLS/HTTP/2 starts.
	quicHeadStart = 250 * time.Millisecond
	// maxFieldSectionSize bounds decoded response header sections.
	maxFieldSectionSize = 64 * 1024
	// MaxTokenBytes bounds bearer files and access tokens.
	MaxTokenBytes = 64 * 1024
	// copyBufferSize is the transport buffer for both directions.
	copyBufferSize   = 64 * 1024
	proxyIdleTimeout = 30 * time.Second
	proxyKeepAlive   = 10 * time.Second
)

// EnableConnectProtocol is the HTTP/3 setting identifier for RFC 9220 extended
// CONNECT (SETTINGS_ENABLE_CONNECT_PROTOCOL).
const enableConnectProtocol = 0x8

// setupError mirrors Rust's `SetupError`: a setup timeout or a CONNECT rejected
// by the gateway. It is the only peer detail allowed to reach diagnostics.
type setupError struct {
	status *int
}

func (e *setupError) Error() string {
	if e.status == nil {
		return "SSH tunnel setup timed out"
	}
	return fmt.Sprintf("CONNECT rejected with status %d", *e.status)
}

func tunnelTimeout() error { return &setupError{} }

func rejection(status int) error { return &setupError{status: &status} }

func isRejection(err error) bool {
	var setup *setupError
	return errors.As(err, &setup) && setup.status != nil
}

// streamError hides every peer-supplied detail, which may contain arbitrary
// text (for example a GOAWAY debug payload).
func streamError() error { return errors.New("SSH tunnel stream failed") }

// requestSpec is the CONNECT request content shared by both transports: the
// gateways headers and the credential. Each attempt builds its own request so
// it can carry its own body stream.
type requestSpec struct {
	headers http.Header
	bearer  string
}

// request renders one CONNECT request for the spec. The caller assigns the
// transport-specific URL and body.
func (t *ProxyTarget) request(spec requestSpec) *http.Request {
	headers := spec.headers.Clone()
	if headers == nil {
		headers = http.Header{}
	}
	headers.Set("Authorization", spec.bearer)
	return &http.Request{
		Method: http.MethodConnect,
		Host:   t.Authority,
		Header: headers,
	}
}

// streamTunnel is one admitted CONNECT stream: uploaded bytes become request
// DATA, downloaded bytes become the response body.
type streamTunnel struct {
	body    io.ReadCloser
	upload  *io.PipeWriter
	cleanup func()
	once    sync.Once
}

func (t *streamTunnel) close() {
	t.once.Do(func() {
		_ = t.upload.Close()
		_ = t.body.Close()
		if t.cleanup != nil {
			t.cleanup()
		}
	})
}

// relay copies bytes until the peer finishes. OpenSSH needs EOF as soon as the
// peer closes, even while stdin is still open, so a completed download returns
// immediately and lets the process exit; the upload goroutine is then dropped
// with it.
func (t *streamTunnel) relay(in io.Reader, out io.Writer) error {
	go func() {
		_, err := io.CopyBuffer(t.upload, in, make([]byte, copyBufferSize))
		_ = t.upload.CloseWithError(err)
	}()
	_, err := copyAndFlush(out, t.body)
	t.close()
	if err != nil {
		return streamError()
	}
	return nil
}

// copyAndFlush forwards each response chunk and flushes a buffered local
// writer before reading more. Rust's HTTP/2 tunnel flushes after every DATA
// frame so interactive responses reach the process while the stream is open
// (Rust #62186).
func copyAndFlush(out io.Writer, in io.Reader) (int64, error) {
	buffer := make([]byte, copyBufferSize)
	var written int64
	for {
		count, readErr := in.Read(buffer)
		if count > 0 {
			n, writeErr := out.Write(buffer[:count])
			written += int64(n)
			if writeErr != nil {
				return written, writeErr
			}
			if n != count {
				return written, io.ErrShortWrite
			}
			if flusher, ok := out.(interface{ Flush() error }); ok {
				if flushErr := flusher.Flush(); flushErr != nil {
					return written, flushErr
				}
			}
		}
		if readErr == io.EOF {
			return written, nil
		}
		if readErr != nil {
			return written, readErr
		}
	}
}

// h3Connection owns one QUIC/HTTP/3 connection to the gateway.
type h3Connection struct {
	target    *ProxyTarget
	tlsConfig *tls.Config

	mu         sync.Mutex
	conn       *quic.Conn
	transport  *http3.Transport
	transports []*quic.Transport
}

// connectH3 establishes the QUIC transport before any credential is read.
func connectH3(ctx context.Context, target *ProxyTarget, tlsConfig *tls.Config) (*h3Connection, error) {
	quicTLS := tlsConfig.Clone()
	quicTLS.NextProtos = []string{"h3"}
	c := &h3Connection{target: target, tlsConfig: quicTLS}
	conn, transports, err := dialProxy(ctx, target, quicTLS)
	if err != nil {
		return nil, err
	}
	c.conn = conn
	c.transports = transports
	c.transport = &http3.Transport{
		TLSClientConfig: quicTLS,
		QUICConfig: &quic.Config{
			MaxIdleTimeout:  proxyIdleTimeout,
			KeepAlivePeriod: proxyKeepAlive,
		},
		// Advertise extended CONNECT (RFC 9220) like Rust's h3 client builder.
		AdditionalSettings: map[uint64]uint64{enableConnectProtocol: 1},
		// h3 advertises and enforces the maximum decoded field-section size.
		MaxResponseHeaderBytes: maxFieldSectionSize,
		Dial: func(context.Context, string, *tls.Config, *quic.Config) (*quic.Conn, error) {
			return conn, nil
		},
	}
	return c, nil
}

func (c *h3Connection) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.transport != nil {
		_ = c.transport.Close()
		c.transport = nil
	}
	if c.conn != nil {
		_ = c.conn.CloseWithError(0, "")
		c.conn = nil
	}
	for _, transport := range c.transports {
		_ = transport.Close()
	}
	c.transports = nil
}

// admit sends the one authenticated CONNECT and waits for a successful
// response before any SSH byte moves.
func (c *h3Connection) admit(ctx context.Context, spec requestSpec) (*streamTunnel, error) {
	bodyReader, bodyWriter := io.Pipe()
	request := c.target.request(spec)
	request.URL = &url.URL{Scheme: "https", Host: c.target.Addr()}
	request.Body = bodyReader
	request = request.WithContext(ctx)
	response, err := c.transport.RoundTrip(request)
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
		body:   response.Body,
		upload: bodyWriter,
		cleanup: func() {
			c.close()
		},
	}, nil
}

// dialProxy resolves the proxy host and tries every peer, mirroring Rust's
// per-address QUIC handshake loop.
func dialProxy(ctx context.Context, target *ProxyTarget, tlsConfig *tls.Config) (*quic.Conn, []*quic.Transport, error) {
	peers, err := net.DefaultResolver.LookupHost(ctx, target.Host)
	if err != nil {
		return nil, nil, fmt.Errorf("connecting to MASQUE proxy: %w", err)
	}
	var lastErr error
	for _, peer := range peers {
		config := tlsConfig.Clone()
		if config.ServerName == "" {
			config.ServerName = target.Host
		}
		remote, err := net.ResolveUDPAddr("udp", net.JoinHostPort(peer, strconv.Itoa(target.Port)))
		if err != nil {
			lastErr = err
			continue
		}
		local := &net.UDPAddr{}
		if ip := net.ParseIP(peer); ip != nil && ip.To4() == nil {
			local = &net.UDPAddr{IP: net.IPv6unspecified}
		}
		udp, err := net.ListenUDP("udp", local)
		if err != nil {
			lastErr = err
			continue
		}
		var conn net.PacketConn = udp
		if runtime.GOOS == "linux" {
			// Linux can advertise UDP segmentation on paths where batched
			// packets are lost; single datagrams keep bulk SSH transfers
			// moving there. Rust disables segmentation offload on Linux only.
			conn = packetConnWithoutBatch{udp}
		}
		transport := &quic.Transport{Conn: conn}
		attemptCtx, cancel := context.WithTimeout(ctx, quicHandshakeTimeout)
		quicConn, err := transport.Dial(attemptCtx, remote, config, &quic.Config{
			MaxIdleTimeout:  proxyIdleTimeout,
			KeepAlivePeriod: proxyKeepAlive,
			Allow0RTT:       false,
		})
		cancel()
		if err != nil {
			_ = transport.Close()
			lastErr = err
			continue
		}
		return quicConn, []*quic.Transport{transport}, nil
	}
	if lastErr == nil {
		lastErr = errors.New("MASQUE proxy resolved to no addresses")
	}
	return nil, nil, fmt.Errorf("HTTP/3 proxy connection failed: %w", lastErr)
}

// packetConnWithoutBatch hides quic-go's batched send path from the UDP socket,
// which disables UDP segmentation offload for SSH tunnels.
type packetConnWithoutBatch struct {
	net.PacketConn
}
