package tcptunnel

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
)

// Transport tuning mirrors Rust's `codex-tcp-tunnel`.
const (
	proxyConnectTimeout  = 10 * time.Second
	quicHandshakeTimeout = 5 * time.Second
	proxyIdleTimeout     = 30 * time.Second
	proxyKeepAlive       = 10 * time.Second
	initialRetryDelay    = time.Second
	maxRetryDelay        = 15 * time.Second
	bridgeCopyBufferSize = 64 * 1024
)

// EnableConnectProtocol is the HTTP/3 setting identifier for RFC 9220 extended
// CONNECT (SETTINGS_ENABLE_CONNECT_PROTOCOL).
const enableConnectProtocol = 0x8

// Run executes the hidden `codex tcp-tunnel` command.
func Run(ctx context.Context, args *Args, stdin io.Reader, stdout, stderr io.Writer) error {
	return run(ctx, args, stdin, stdout, stderr, newSession)
}

// sessionFactory builds the proxy transport so tests can supply local trust
// roots without changing the command surface.
type sessionFactory func(target *ProxyTarget) *session

func run(ctx context.Context, args *Args, stdin io.Reader, stdout, stderr io.Writer, newSessionFn sessionFactory) error {
	if !args.AuthTokenStdin {
		return errors.New("--auth-token-stdin is required")
	}
	listenAddr, err := net.ResolveTCPAddr("tcp", args.ListenAddr)
	if err != nil {
		return fmt.Errorf("invalid listen address: %w", err)
	}
	if !listenAddr.IP.IsLoopback() {
		return errors.New("listener must be loopback")
	}
	originsSource, err := os.ReadFile(args.ProxyOriginsFile)
	if err != nil {
		return fmt.Errorf("reading trusted proxy origins: %w", err)
	}
	trustedOrigins, err := ParseTrustedOrigins(string(originsSource))
	if err != nil {
		return err
	}
	proxyURL, err := url.Parse(args.ProxyURL)
	if err != nil {
		return fmt.Errorf("invalid proxy URL: %w", err)
	}
	target, err := ParseProxyTarget(proxyURL, trustedOrigins, args.Target)
	if err != nil {
		return err
	}

	control := newControlInput(bufio.NewReader(stdin), args.ConnectHeadersStdin)
	metadata := <-control.metadata
	if metadata.err != nil {
		return metadata.err
	}
	initial, ok := <-control.tokens
	if !ok {
		return errors.New("MASQUE credential input closed")
	}
	if initial.err != nil {
		return initial.err
	}

	auth := &authState{}
	auth.store(initial.auth)
	headers := metadata.headers

	listener, err := net.ListenTCP("tcp", listenAddr)
	if err != nil {
		return fmt.Errorf("binding loopback listener: %w", err)
	}
	defer listener.Close()

	session := newSessionFn(target)
	defer session.close()
	connectCtx, cancelConnect := context.WithTimeout(ctx, proxyConnectTimeout)
	err = session.connect(connectCtx)
	cancelConnect()
	if err != nil {
		return err
	}

	// The line is the caller readiness contract, including the assigned port.
	if _, err := fmt.Fprintf(stdout, "LISTENING %s\n", listener.Addr().String()); err != nil {
		return err
	}
	if flusher, ok := stdout.(interface{ Flush() error }); ok {
		_ = flusher.Flush()
	}

	ready := make(chan struct{})
	close(ready)
	if !args.AuthTokenUpdatesStdin {
		return serve(ctx, listener, target, session, headers, auth, stderr)
	}

	tunnelErr := make(chan error, 1)
	go func() {
		tunnelErr <- serve(ctx, listener, target, session, headers, auth, stderr)
	}()
	updatesErr := make(chan error, 1)
	go func() {
		updatesErr <- updateAuthTokens(control.tokens, auth, ready, stdout)
	}()
	select {
	case err := <-tunnelErr:
		return err
	case err := <-updatesErr:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// updateAuthTokens mirrors Rust's `update_auth_tokens`: each replacement bearer
// becomes the credential for new connections, the readiness barrier is
// acknowledged once, and `AUTH_UPDATED` is reported for every accepted update.
func updateAuthTokens(tokens <-chan controlToken, auth *authState, ready <-chan struct{}, output io.Writer) error {
	first := true
	for token := range tokens {
		if token.err != nil {
			return token.err
		}
		auth.store(token.auth)
		if first {
			first = false
			<-ready
		}
		if _, err := fmt.Fprintln(output, "AUTH_UPDATED"); err != nil {
			return err
		}
	}
	return nil
}

// authState carries the current bearer across the tunnel's connections.
type authState struct {
	value atomic.Value
}

func (a *authState) store(value string) { a.value.Store(value) }

func (a *authState) load() string {
	if stored, ok := a.value.Load().(string); ok {
		return stored
	}
	return ""
}

// session owns the HTTP/3 connection to the proxy and re-dials it when it goes
// away. Accepted TCP streams are never replayed.
type session struct {
	target    *ProxyTarget
	tlsConfig *tls.Config
	quicConf  *quic.Config
	transport *http3.Transport

	mu   sync.Mutex
	conn *quic.Conn
}

func newSession(target *ProxyTarget) *session {
	s := &session{
		target:    target,
		tlsConfig: &tls.Config{NextProtos: []string{"h3"}},
		quicConf: &quic.Config{
			MaxIdleTimeout:  proxyIdleTimeout,
			KeepAlivePeriod: proxyKeepAlive,
		},
	}
	s.transport = &http3.Transport{
		TLSClientConfig: s.tlsConfig,
		QUICConfig:      s.quicConf,
		// Advertise extended CONNECT (RFC 9220) like Rust's h3 client builder.
		AdditionalSettings: map[uint64]uint64{enableConnectProtocol: 1},
		Dial:               s.dial,
	}
	return s
}

// close releases the transport; used by tests and by Run's deferred cleanup.
func (s *session) close() {
	s.mu.Lock()
	conn := s.conn
	s.conn = nil
	s.mu.Unlock()
	if conn != nil {
		_ = conn.CloseWithError(0, "")
	}
	_ = s.transport.Close()
}

// dial is the http3.Transport connection hook: it returns the cached connection
// when it is alive and re-dials otherwise.
func (s *session) dial(ctx context.Context, _ string, tlsConfig *tls.Config, quicConf *quic.Config) (*quic.Conn, error) {
	return s.ensureConnected(ctx, tlsConfig, quicConf)
}

// connect establishes the transport before the listener reports readiness.
func (s *session) connect(ctx context.Context) error {
	_, err := s.ensureConnected(ctx, s.tlsConfig, s.quicConf)
	return err
}

func (s *session) ensureConnected(ctx context.Context, tlsConfig *tls.Config, quicConf *quic.Config) (*quic.Conn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn != nil {
		select {
		case <-s.conn.Context().Done():
			s.conn = nil
		default:
			return s.conn, nil
		}
	}
	conn, err := dialProxy(ctx, s.target, tlsConfig, quicConf)
	if err != nil {
		return nil, err
	}
	s.conn = conn
	return conn, nil
}

// dialProxy resolves the proxy host and tries every peer, mirroring Rust's
// per-address QUIC handshake loop.
func dialProxy(ctx context.Context, target *ProxyTarget, tlsConfig *tls.Config, quicConf *quic.Config) (*quic.Conn, error) {
	if tlsConfig == nil {
		tlsConfig = &tls.Config{NextProtos: []string{"h3"}}
	}
	if quicConf == nil {
		quicConf = &quic.Config{}
	}
	if quicConf.MaxIdleTimeout == 0 {
		quicConf.MaxIdleTimeout = proxyIdleTimeout
	}
	if quicConf.KeepAlivePeriod == 0 {
		quicConf.KeepAlivePeriod = proxyKeepAlive
	}
	peers, err := net.DefaultResolver.LookupHost(ctx, target.Host)
	if err != nil {
		return nil, fmt.Errorf("connecting to MASQUE proxy: %w", err)
	}
	lastErr := errors.New("MASQUE proxy resolved to no addresses")
	for _, peer := range peers {
		config := tlsConfig.Clone()
		if config.ServerName == "" {
			config.ServerName = target.Host
		}
		addr := net.JoinHostPort(peer, fmt.Sprintf("%d", target.Port))
		attemptCtx, cancel := context.WithTimeout(ctx, quicHandshakeTimeout)
		conn, err := quic.DialAddr(attemptCtx, addr, config, quicConf)
		cancel()
		if err != nil {
			lastErr = err
			continue
		}
		return conn, nil
	}
	return nil, fmt.Errorf("QUIC handshake failed: %w", lastErr)
}

type acceptResult struct {
	conn net.Conn
	err  error
}

// serve accepts loopback connections until the context ends, reconnecting the
// proxy transport when it is lost. While offline, newly accepted connections
// are closed rather than queued.
func serve(ctx context.Context, listener *net.TCPListener, target *ProxyTarget, session *session, headers http.Header, auth *authState, stderr io.Writer) error {
	accepts := make(chan acceptResult)
	go func() {
		for {
			conn, err := listener.Accept()
			select {
			case accepts <- acceptResult{conn: conn, err: err}:
			case <-ctx.Done():
				if conn != nil {
					_ = conn.Close()
				}
				return
			}
			if err != nil {
				return
			}
		}
	}()

	for {
		session.mu.Lock()
		conn := session.conn
		session.mu.Unlock()
		if conn == nil {
			if err := reconnect(ctx, session, accepts, stderr); err != nil {
				return err
			}
			continue
		}
		served, err := serveConnection(ctx, conn, accepts, target, session, headers, auth, stderr)
		if served {
			return nil
		}
		if err == nil {
			return nil
		}
		fmt.Fprintf(stderr, "MASQUE proxy connection lost; reconnecting: %v\n", err)
		if err := reconnect(ctx, session, accepts, stderr); err != nil {
			return err
		}
	}
}

// serveConnection accepts local connections while the transport is alive. It
// reports `served` when the context ended, and returns the transport error
// otherwise.
func serveConnection(ctx context.Context, conn *quic.Conn, accepts <-chan acceptResult, target *ProxyTarget, session *session, headers http.Header, auth *authState, stderr io.Writer) (bool, error) {
	transportDone := conn.Context().Done()
	for {
		select {
		case <-ctx.Done():
			return true, nil
		case <-transportDone:
			return false, errors.New("MASQUE HTTP/3 connection closed")
		case result := <-accepts:
			if result.err != nil {
				return true, result.err
			}
			socket := result.conn
			if socket == nil {
				continue
			}
			go func() {
				if err := bridge(ctx, session, socket, target, headers, auth); err != nil {
					fmt.Fprintf(stderr, "MASQUE TCP connection failed: %v\n", err)
				}
			}()
		}
	}
}

// reconnect retries the proxy transport with Rust's backoff while draining (and
// closing) local connections so nothing is queued or replayed.
func reconnect(ctx context.Context, session *session, accepts <-chan acceptResult, stderr io.Writer) error {
	delay := initialRetryDelay
	for {
		attemptCtx, cancel := context.WithTimeout(ctx, proxyConnectTimeout)
		err := session.connect(attemptCtx)
		cancel()
		if err == nil {
			return nil
		}
		fmt.Fprintf(stderr, "MASQUE proxy reconnect failed: %v\n", err)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case result := <-accepts:
			timer.Stop()
			if result.conn != nil {
				_ = result.conn.Close()
			}
		case <-timer.C:
		}
		if delay < maxRetryDelay {
			delay *= 2
			if delay > maxRetryDelay {
				delay = maxRetryDelay
			}
		}
	}
}

// bridge forwards one local connection through its own authenticated CONNECT.
func bridge(ctx context.Context, session *session, socket net.Conn, target *ProxyTarget, headers http.Header, auth *authState) error {
	defer socket.Close()
	pipeReader, pipeWriter := io.Pipe()
	request := (&http.Request{
		Method: http.MethodConnect,
		URL: &url.URL{
			Scheme: "https",
			Host:   session.target.Addr(),
		},
		Host:   target.Authority,
		Header: headers.Clone(),
		Body:   pipeReader,
	}).WithContext(ctx)
	request.Header.Set("Authorization", auth.load())

	response, err := session.transport.RoundTrip(request)
	if err != nil {
		_ = pipeWriter.CloseWithError(err)
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		_ = pipeWriter.CloseWithError(fmt.Errorf("CONNECT rejected"))
		return fmt.Errorf("MASQUE CONNECT rejected with status %d", response.StatusCode)
	}

	uploadErr := make(chan error, 1)
	go func() {
		buffer := make([]byte, bridgeCopyBufferSize)
		_, err := io.CopyBuffer(pipeWriter, socket, buffer)
		// Closing the pipe ends the request stream, mirroring Rust's
		// `send.finish()` on local EOF.
		_ = pipeWriter.CloseWithError(err)
		uploadErr <- err
	}()

	buffer := make([]byte, bridgeCopyBufferSize)
	_, downloadErr := io.CopyBuffer(socket, response.Body, buffer)
	if tcp, ok := socket.(*net.TCPConn); ok && downloadErr == nil {
		_ = tcp.CloseWrite()
	}
	if downloadErr != nil {
		return downloadErr
	}
	select {
	case err := <-uploadErr:
		if err != nil && !errors.Is(err, io.ErrClosedPipe) && !isClosedError(err) {
			return err
		}
	default:
	}
	return nil
}

func isClosedError(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	return strings.Contains(message, "use of closed network connection") ||
		strings.Contains(message, "closed pipe")
}
