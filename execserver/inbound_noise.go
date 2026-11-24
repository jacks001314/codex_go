package execserver

// Rust parity: codex-rs/exec-server/src/server/transport.rs
// `run_noise_websocket_listener` plus noise_relay/admission.rs (#62325). An
// executor that registered a Noise environment can accept inbound WebSockets
// instead of dialing a rendezvous relay: the connection carries the same relay
// frame protocol, each stream completes a Noise handshake authorized by the
// environment registry, and the listener bounds connections, handshake attempts
// and the time a socket may wait for its first authorization.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

const (
	// inboundNoiseMaxConnections mirrors Rust's listener semaphore of 64.
	inboundNoiseMaxConnections = 64
	// inboundNoiseFirstAuthorizationTimeout mirrors Rust
	// FIRST_NOISE_AUTHORIZATION_TIMEOUT: a socket that never authorizes is
	// closed so unauthenticated peers cannot hold connection permits.
	inboundNoiseFirstAuthorizationTimeout = 20 * time.Second
	// inboundNoiseMaxHandshakes mirrors MAX_CONCURRENT_HANDSHAKES.
	inboundNoiseMaxHandshakes = 16
	// inboundNoiseHandshakesPerSecond mirrors MAX_HANDSHAKES_PER_SECOND.
	inboundNoiseHandshakesPerSecond = 32
	// inboundNoiseHandshakeWindow mirrors HANDSHAKE_WINDOW.
	inboundNoiseHandshakeWindow = time.Second
)

// noiseHandshakeBudget is Rust's HandshakeBudget: at most 16 handshakes may be
// parsed at once, and at most 32 may start in any one-second window. The
// permit is held through registry authorization so a burst of handshakes
// cannot stall the registry client.
type noiseHandshakeBudget struct {
	permits  chan struct{}
	mu       sync.Mutex
	attempts []time.Time
	now      func() time.Time
}

func newNoiseHandshakeBudget() *noiseHandshakeBudget {
	budget := &noiseHandshakeBudget{
		permits: make(chan struct{}, inboundNoiseMaxHandshakes),
		now:     time.Now,
	}
	for index := 0; index < inboundNoiseMaxHandshakes; index++ {
		budget.permits <- struct{}{}
	}
	return budget
}

// tryAdmit takes one concurrent-handshake permit and charges one attempt
// against the rate window. The returned release must be called once validation
// finishes; repeated calls are safe.
func (b *noiseHandshakeBudget) tryAdmit() (func(), bool) {
	if b == nil {
		return func() {}, true
	}
	select {
	case <-b.permits:
	default:
		return nil, false
	}
	now := b.now()
	b.mu.Lock()
	kept := b.attempts[:0]
	for _, attempt := range b.attempts {
		if now.Sub(attempt) < inboundNoiseHandshakeWindow {
			kept = append(kept, attempt)
		}
	}
	b.attempts = kept
	if len(b.attempts) >= inboundNoiseHandshakesPerSecond {
		b.mu.Unlock()
		b.permits <- struct{}{}
		return nil, false
	}
	b.attempts = append(b.attempts, now)
	b.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() { b.permits <- struct{}{} })
	}, true
}

// noiseInboundAdmission reports the first authorized stream of one inbound
// connection and carries the listener's shared handshake budget (Rust
// InboundAdmission).
type noiseInboundAdmission struct {
	budget     *noiseHandshakeBudget
	authorized chan struct{}
	once       sync.Once
}

// markAuthorized records that the peer completed registry authorization, so
// the listener stops counting against the first-authorization deadline.
func (a *noiseInboundAdmission) markAuthorized() {
	if a == nil || a.authorized == nil {
		return
	}
	a.once.Do(func() { close(a.authorized) })
}

// InboundNoiseListenerControl bounds and drains one inbound Noise listener
// (Rust NoiseListenerControl). Stop cancels every live connection and releases
// the listener; a host waits for the listener call to return before closing
// its history.
type InboundNoiseListenerControl struct {
	permits  chan struct{}
	stop     chan struct{}
	stopOnce sync.Once

	mu                        sync.Mutex
	conns                     map[*websocket.Conn]struct{}
	budget                    *noiseHandshakeBudget
	firstAuthorizationTimeout time.Duration
}

// NewInboundNoiseListenerControl creates a control with the production limits.
func NewInboundNoiseListenerControl() *InboundNoiseListenerControl {
	control := &InboundNoiseListenerControl{
		permits:                   make(chan struct{}, inboundNoiseMaxConnections),
		stop:                      make(chan struct{}),
		conns:                     map[*websocket.Conn]struct{}{},
		budget:                    newNoiseHandshakeBudget(),
		firstAuthorizationTimeout: inboundNoiseFirstAuthorizationTimeout,
	}
	for index := 0; index < inboundNoiseMaxConnections; index++ {
		control.permits <- struct{}{}
	}
	return control
}

// Stop closes the listener's live connections. It is safe to call twice.
func (c *InboundNoiseListenerControl) Stop() {
	if c == nil {
		return
	}
	c.stopOnce.Do(func() {
		close(c.stop)
		c.mu.Lock()
		conns := make([]*websocket.Conn, 0, len(c.conns))
		for conn := range c.conns {
			conns = append(conns, conn)
		}
		c.mu.Unlock()
		for _, conn := range conns {
			_ = conn.CloseNow()
		}
	})
}

// Stopped reports the channel closed by Stop.
func (c *InboundNoiseListenerControl) Stopped() <-chan struct{} {
	if c == nil {
		return nil
	}
	return c.stop
}

// ActiveConnections is the number of open inbound sockets.
func (c *InboundNoiseListenerControl) ActiveConnections() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.conns)
}

// HandshakeBudget exposes the shared budget for diagnostics and tests.
func (c *InboundNoiseListenerControl) HandshakeBudget() *noiseHandshakeBudget {
	if c == nil {
		return nil
	}
	return c.budget
}

func (c *InboundNoiseListenerControl) tryAcquire() bool {
	if c == nil {
		return true
	}
	select {
	case <-c.permits:
		return true
	default:
		return false
	}
}

func (c *InboundNoiseListenerControl) release() {
	if c == nil {
		return
	}
	select {
	case c.permits <- struct{}{}:
	default:
	}
}

func (c *InboundNoiseListenerControl) track(conn *websocket.Conn) {
	if c == nil || conn == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.conns[conn] = struct{}{}
}

func (c *InboundNoiseListenerControl) untrack(conn *websocket.Conn) {
	if c == nil || conn == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.conns, conn)
}

// ServeInboundNoiseWebSocket accepts inbound Noise WebSockets on address,
// printing the bound `ws://` URL to stdout like the stdio and plaintext
// listeners do.
func (s *Server) ServeInboundNoiseWebSocket(ctx context.Context, address string, cfg RemoteEnvironmentConfig, registration remoteRegistrationResponse, identity *remoteNoiseIdentity, control *InboundNoiseListenerControl, stdout io.Writer) error {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	if stdout != nil {
		if _, err := fmt.Fprintf(stdout, "ws://%s\n", listener.Addr().String()); err != nil {
			_ = listener.Close()
			return err
		}
	}
	return s.ServeInboundNoiseWebSocketListener(ctx, listener, cfg, registration, identity, control)
}

// ServeInboundNoiseWebSocketListener serves the inbound Noise listener on an
// existing listener until ctx ends or Stop is called, then drains the open
// connections.
func (s *Server) ServeInboundNoiseWebSocketListener(ctx context.Context, listener net.Listener, cfg RemoteEnvironmentConfig, registration remoteRegistrationResponse, identity *remoteNoiseIdentity, control *InboundNoiseListenerControl) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if listener == nil {
		return errors.New("inbound Noise listener is required")
	}
	if identity == nil {
		return errors.New("inbound Noise listener requires the executor identity")
	}
	if strings.TrimSpace(registration.EnvironmentID) == "" || strings.TrimSpace(registration.ExecutorRegistrationID) == "" {
		return errors.New("inbound Noise listener requires the environment registration")
	}
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return errors.New("inbound Noise listener requires the environment registry")
	}
	if control == nil {
		control = NewInboundNoiseListenerControl()
	}
	defer s.shutdownSessions()

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Rust rejects origin-bearing upgrades; the inbound listener keeps the
		// plaintext listener's rule so a browser page cannot dial it.
		if r.Header.Get("Origin") != "" {
			http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
			return
		}
		if r.URL.Path == "/readyz" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		if !control.tryAcquire() {
			http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
			return
		}
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{})
		if err != nil {
			control.release()
			return
		}
		conn.SetReadLimit(int64(maxNoiseJSONRPCMessageLen))
		control.track(conn)
		defer func() {
			control.untrack(conn)
			control.release()
			_ = conn.CloseNow()
		}()
		admission := &noiseInboundAdmission{budget: control.budget, authorized: make(chan struct{})}
		connCtx, cancel := context.WithCancel(r.Context())
		defer cancel()
		done := make(chan error, 1)
		go func() {
			done <- s.serveNoiseRelayConnectionWithAdmission(connCtx, conn, cfg, &registration, identity, admission)
		}()
		deadline := control.firstAuthorizationTimeout
		if deadline <= 0 {
			deadline = inboundNoiseFirstAuthorizationTimeout
		}
		timer := time.NewTimer(deadline)
		defer timer.Stop()
		select {
		case <-admission.authorized:
		case <-done:
			return
		case <-timer.C:
			cancel()
			_ = conn.CloseNow()
			<-done
			return
		case <-control.stop:
			cancel()
			_ = conn.CloseNow()
			<-done
			return
		case <-ctx.Done():
			cancel()
			_ = conn.CloseNow()
			<-done
			return
		}
		select {
		case <-done:
		case <-control.stop:
			cancel()
			_ = conn.CloseNow()
			<-done
		case <-ctx.Done():
			cancel()
			_ = conn.CloseNow()
			<-done
		}
	})
	httpServer := &http.Server{Handler: mux}
	served := make(chan error, 1)
	go func() { served <- httpServer.Serve(listener) }()
	select {
	case <-ctx.Done():
		control.Stop()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
		err := <-served
		if errors.Is(err, http.ErrServerClosed) || errors.Is(err, net.ErrClosed) {
			return nil
		}
		return err
	case <-control.stop:
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
		err := <-served
		if errors.Is(err, http.ErrServerClosed) || errors.Is(err, net.ErrClosed) {
			return nil
		}
		return err
	case err := <-served:
		if errors.Is(err, http.ErrServerClosed) || errors.Is(err, net.ErrClosed) {
			return nil
		}
		return err
	}
}

// ParseInboundNoiseListenURL accepts the `ws://IP:PORT` endpoint Rust requires
// for a remote Noise listener and reports a numeric address.
func ParseInboundNoiseListenURL(listenURL string) (string, error) {
	address, ok := strings.CutPrefix(strings.TrimSpace(listenURL), "ws://")
	if !ok {
		return "", fmt.Errorf("remote --listen requires ws://IP:PORT, got %q", listenURL)
	}
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return "", fmt.Errorf("remote --listen requires ws://IP:PORT, got %q", listenURL)
	}
	ip := net.ParseIP(host)
	port, err := strconv.Atoi(portText)
	if ip == nil || err != nil || port < 0 || port > 65535 {
		return "", fmt.Errorf("remote --listen requires ws://IP:PORT, got %q", listenURL)
	}
	return net.JoinHostPort(ip.String(), strconv.Itoa(port)), nil
}

// RunInboundNoiseEnvironment registers this executor's Noise identity and then
// accepts inbound harness connections on address (Rust
// run_inbound_noise_environment). The listener stops when ctx ends or control
// is stopped, and it drains live sockets before returning.
func RunInboundNoiseEnvironment(ctx context.Context, cfg RemoteEnvironmentConfig, address string, control *InboundNoiseListenerControl, stdout io.Writer) error {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	if stdout != nil {
		if _, err := fmt.Fprintf(stdout, "ws://%s\n", listener.Addr().String()); err != nil {
			_ = listener.Close()
			return err
		}
	}
	return RunInboundNoiseEnvironmentListener(ctx, listener, cfg, control)
}

// RunInboundNoiseEnvironmentListener serves the registered inbound environment
// on an existing listener.
func RunInboundNoiseEnvironmentListener(ctx context.Context, listener net.Listener, cfg RemoteEnvironmentConfig, control *InboundNoiseListenerControl) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if listener == nil {
		return errors.New("inbound Noise environment requires a listener")
	}
	cfg.BaseURL = strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if cfg.BaseURL == "" {
		return errors.New("environment registry base URL is required")
	}
	cfg.EnvironmentID = strings.TrimSpace(cfg.EnvironmentID)
	if cfg.EnvironmentID == "" {
		return errors.New("environment id is required for inbound exec-server registration")
	}
	if strings.TrimSpace(cfg.ForwardURL) != "" {
		// Rust rejects forwarding from a Noise listener: the forward bridge is
		// built for outbound relay streams.
		return errors.New("inbound Noise exec-server does not support forwarding")
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = remoteHTTPClient()
	}
	server := NewServerWithHTTPClient(cfg.HTTPClient)
	server.SetPreferMXC(cfg.PreferMXC)
	server.SetProxyPrivateIPsViaUpstream(cfg.ProxyPrivateIPsViaUpstream)
	server.SetLinuxSandboxTransparentTCP(cfg.LinuxSandboxTransparentTCP)
	server.SetLinuxSandboxPIDNamespace(cfg.LinuxSandboxPIDNamespace)
	server.SetTcpUpstreamProxy(cfg.TcpUpstreamProxy)
	server.SetEnvironmentConfigReader(cfg.EnvironmentConfigReader)
	server.SetAllowedSymlinkedCodexHome(cfg.AllowedSymlinkedCodexHome)
	identity, err := generateRemoteNoiseIdentity()
	if err != nil {
		return fmt.Errorf("failed to generate Noise listener identity: %w", err)
	}
	defer identity.Destroy()
	registration, err := registerRemoteEnvironment(ctx, cfg, identity.PublicKey())
	if err != nil {
		return err
	}
	server.SetExecutorRegistrationIdentity(registration.EnvironmentID, registration.ExecutorRegistrationID)
	return server.ServeInboundNoiseWebSocketListener(ctx, listener, cfg, *registration, identity, control)
}
