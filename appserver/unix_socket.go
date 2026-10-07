package appserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"

	"codex_go/session"
)

var ErrUnixSocketUnsupported = errors.New("app-server unix socket transport is not supported on this platform")

type UnixSocketOptions struct {
	CodexHome      string
	StoreRoot      string
	Listen         string
	RuntimeOptions *RuntimeRouterOptions
	// ShutdownAccess accepts the managed daemon's `/daemon/shutdown` request
	// (Rust DaemonShutdownAccess). A server nobody manages refuses it.
	ShutdownAccess DaemonShutdownAccess
	// OnDaemonShutdown is invoked after a successful shutdown handshake.
	OnDaemonShutdown func()
}

func UnixSocketPath(listen string, codexHome string) (string, error) {
	listen = strings.TrimSpace(listen)
	if listen == "" || listen == "unix://" {
		return AppServerControlSocketPath(codexHome), nil
	}
	if strings.HasPrefix(listen, "unix://") {
		rest := strings.TrimPrefix(listen, "unix://")
		if rest == "" {
			return AppServerControlSocketPath(codexHome), nil
		}
		if strings.HasPrefix(rest, "/") {
			return filepath.Clean(rest), nil
		}
		parsed, err := url.Parse(listen)
		if err == nil && parsed.Host != "" {
			if parsed.Path != "" {
				return absoluteUnixSocketPath(parsed.Host + parsed.Path)
			}
			return absoluteUnixSocketPath(parsed.Host)
		}
		return absoluteUnixSocketPath(rest)
	}
	return "", fmt.Errorf("unsupported app-server unix listen address %s", listen)
}

func absoluteUnixSocketPath(path string) (string, error) {
	path = filepath.Clean(path)
	if filepath.IsAbs(path) {
		return path, nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.Clean(abs), nil
}

func NewUnixSocketRouter(codexHome string) *RuntimeRouter {
	return NewUnixSocketRouterWithOptions(codexHome, nil)
}

func NewUnixSocketRouterWithOptions(codexHome string, options *RuntimeRouterOptions) *RuntimeRouter {
	codexHome = strings.TrimSpace(codexHome)
	if codexHome == "" {
		codexHome = ".gcode"
	}
	store := session.NewStore(filepath.Join(codexHome, "sessions"))
	router := NewDefaultRuntimeRouterWithOptions(store, codexHome, options)
	// Rust stamps `rpc.transport = "unix_socket"` on the request span.
	router.SetRequestTransport("unix_socket")
	return router
}

func ServeUnixSocket(ctx context.Context, options *UnixSocketOptions) error {
	if options == nil {
		options = &UnixSocketOptions{}
	}
	// Rust #51470: a managed app-server daemon raises its soft file descriptor
	// limit before serving the control socket. The adjustment is best-effort and
	// preserves an already higher soft limit.
	if options.ShutdownAccess == DaemonShutdownManaged {
		if err := raiseManagedDaemonNoFileLimit(); err != nil {
			slog.Warn("failed to raise managed app-server file descriptor limit", "error", err)
		}
	}
	codexHome := strings.TrimSpace(options.CodexHome)
	if codexHome == "" {
		codexHome = ".gcode"
	}
	preparedRuntimeOptions, ownedStateRuntime, err := prepareSharedStateRuntime(ctx, codexHome, options.RuntimeOptions)
	if err != nil {
		return err
	}
	if ownedStateRuntime != nil {
		defer ownedStateRuntime.Close()
	}
	if preparedRuntimeOptions.logDBInstallation != nil {
		defer preparedRuntimeOptions.logDBInstallation.Close(context.Background())
	}
	socketPath, err := UnixSocketPath(options.Listen, codexHome)
	if err != nil {
		return err
	}
	// A managed daemon takes one recovery snapshot for the whole process: every
	// connection's router contributes its loaded threads, and the file is written
	// once after the server loop returns (Rust app-server/src/lib.rs:1037-1042
	// keeps the snapshot at daemon scope).
	recoverySink := daemonRecoverySinkForAccess(options.ShutdownAccess, codexHome)
	newRouter := func() *RuntimeRouter {
		if strings.TrimSpace(options.StoreRoot) != "" {
			router := NewDefaultRuntimeRouterWithOptions(session.NewStore(options.StoreRoot), codexHome, preparedRuntimeOptions)
			router.SetRequestTransport("unix_socket")
			return router
		}
		return NewUnixSocketRouterWithOptions(codexHome, preparedRuntimeOptions)
	}
	// A managed daemon also consumes the previous generation's handoff before it
	// serves and restores those threads in the background (Rust
	// daemon_thread_recovery.rs:32). Its daemon-lifetime router shares the
	// recovery sink, so the threads it restored are handed to the next generation.
	recoveryRestore := startDaemonRecoveryRestoreForAccess(options.ShutdownAccess, codexHome, newRouter)
	if recoveryRestore != nil && recoveryRestore.router != nil {
		recoveryRestore.router.SetDaemonRecoverySink(recoverySink)
	}
	serveErr := serveUnixSocket(ctx, socketPath, func() *RuntimeRouter {
		router := newRouter()
		router.SetDaemonRecoverySink(recoverySink)
		return router
	}, options.ShutdownAccess, options.OnDaemonShutdown)
	// Release the daemon-lifetime router before the snapshot is written so its
	// restored threads contribute to the next generation.
	if recoveryRestore != nil {
		recoveryRestore.close()
	}
	if recoverySink != nil {
		// Best-effort: a failed save must never block the shutdown.
		if err := recoverySink.WriteSnapshot(); err != nil {
			slog.Warn("failed to save daemon recovery snapshot", "error", err)
		}
	}
	return serveErr
}

func ensureUnixSocketParent(socketPath string) error {
	dir := filepath.Dir(socketPath)
	if dir == "." || dir == "" {
		return nil
	}
	return os.MkdirAll(dir, 0o700)
}

// DaemonShutdownPath is the WebSocket path a managed daemon's owner uses to ask
// the server to stop (Rust app-server-transport::run_daemon_shutdown).
const DaemonShutdownPath = "/daemon/shutdown"

// maxUnfragmentedMessageBytesHeader advertises the single-frame message limit so
// a client can reject an oversized request before the socket closes (Rust
// MAX_UNFRAGMENTED_MESSAGE_BYTES_HEADER).
const maxUnfragmentedMessageBytesHeader = "x-codex-websocket-max-unfragmented-message-bytes"

// controlSocketResponseTimeout bounds the shutdown handshake (Rust
// client::CONTROL_SOCKET_RESPONSE_TIMEOUT).
const controlSocketResponseTimeout = 2 * time.Second

// DaemonShutdownAccess mirrors Rust transport::DaemonShutdownAccess: only a
// server that a managed daemon owns accepts the shutdown request.
type DaemonShutdownAccess int

const (
	// DaemonShutdownDisabled refuses the shutdown path.
	DaemonShutdownDisabled DaemonShutdownAccess = iota
	// DaemonShutdownManaged accepts the shutdown path.
	DaemonShutdownManaged
)

// serveUnixSocketHTTP serves the control socket as the WebSocket transport Rust
// uses (app-server-transport::start_control_socket_acceptor): every accepted
// connection upgrades, the managed shutdown path is gated by access, and any
// other path is the JSON-RPC control channel.
func serveUnixSocketHTTP(ctx context.Context, listener net.Listener, routerFactory func() *RuntimeRouter, access DaemonShutdownAccess, shutdown func()) error {
	if ctx == nil {
		ctx = context.Background()
	}
	handler := &unixSocketHandler{routerFactory: routerFactory, access: access, shutdown: shutdown}
	server := &http.Server{Handler: handler}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), controlSocketResponseTimeout)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
		_ = listener.Close()
	}()
	err := server.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) || (ctx.Err() != nil && errors.Is(err, net.ErrClosed)) {
		return nil
	}
	return err
}

type unixSocketHandler struct {
	routerFactory func() *RuntimeRouter
	access        DaemonShutdownAccess
	shutdown      func()
}

func (h *unixSocketHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == DaemonShutdownPath {
		if h == nil || h.access != DaemonShutdownManaged {
			http.Error(w, "unmanaged server", http.StatusForbidden)
			return
		}
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{})
		if err != nil {
			return
		}
		runDaemonShutdownHandshake(r.Context(), conn, h.shutdown)
		return
	}
	if h == nil || h.routerFactory == nil {
		http.Error(w, "app-server control socket router is not configured", http.StatusInternalServerError)
		return
	}
	// Rust routes every other path to the WebSocket upgrade handler.
	if max := DefaultWebSocketMaxMessageSize; max > 0 {
		w.Header().Set(maxUnfragmentedMessageBytesHeader, strconv.FormatInt(max, 10))
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{})
	if err != nil {
		return
	}
	if DefaultWebSocketMaxMessageSize > 0 {
		conn.SetReadLimit(DefaultWebSocketMaxMessageSize)
	}
	_ = serveWebSocketConnection(r.Context(), conn, h.routerFactory())
}

// runDaemonShutdownHandshake reads the owner's PID, echoes it, and asks the
// manager to stop (Rust run_daemon_shutdown).
func runDaemonShutdownHandshake(ctx context.Context, conn *websocket.Conn, shutdown func()) {
	if conn == nil {
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	if ctx == nil {
		ctx = context.Background()
	}
	pid := strconv.Itoa(os.Getpid())
	readCtx, cancelRead := context.WithTimeout(ctx, controlSocketResponseTimeout)
	messageType, data, err := conn.Read(readCtx)
	cancelRead()
	if err != nil || messageType != websocket.MessageText || string(data) != pid {
		return
	}
	writeCtx, cancelWrite := context.WithTimeout(context.Background(), controlSocketResponseTimeout)
	err = conn.Write(writeCtx, websocket.MessageText, []byte(pid))
	cancelWrite()
	if err != nil {
		return
	}
	// Let the manager receive the acknowledgment before the main loop closes
	// connections.
	ackCtx, cancelAck := context.WithTimeout(context.Background(), controlSocketResponseTimeout)
	_, _, _ = conn.Read(ackCtx)
	cancelAck()
	if shutdown != nil {
		shutdown()
	}
}
