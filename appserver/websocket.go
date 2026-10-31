package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"codex_go/config"
	"codex_go/features"
	"codex_go/session"
	"codex_go/state"
)

const DefaultWebSocketMaxMessageSize int64 = 128 << 20

var websocketConnectionCounter uint64

type WebSocketOptions struct {
	CodexHome      string
	StoreRoot      string
	Listen         string
	Auth           *WebSocketAuthSettings
	Ready          io.Writer
	RuntimeOptions *RuntimeRouterOptions
	// CompressionEnabled overrides the app_server_websocket_compression feature
	// for tests and embedders. Nil derives the value from config.
	CompressionEnabled *bool
}

type WebSocketServer struct {
	policy         *WebSocketAuthPolicy
	routerFactory  func() *RuntimeRouter
	maxMessageSize int64
	compression    bool
}

// SetCompressionEnabled controls whether this server negotiates zstd WebSocket
// compression. It must be called before serving requests.
func (s *WebSocketServer) SetCompressionEnabled(enabled bool) {
	if s != nil {
		s.compression = enabled
	}
}

func webSocketCompressionFeatureEnabled(codexHome string) bool {
	cfg, err := config.LoadWithOptions(codexHome, nil)
	if err != nil {
		return false
	}
	return features.Enabled(cfg.FeatureSettings(), "app_server_websocket_compression")
}

func WebSocketListenAddress(listen string) (string, error) {
	listen = strings.TrimSpace(listen)
	if listen == "" {
		listen = "ws://127.0.0.1:0"
	}
	parsed, err := url.Parse(listen)
	if err != nil {
		return "", err
	}
	if parsed.Scheme != "ws" {
		return "", fmt.Errorf("unsupported app-server websocket listen address %s", listen)
	}
	if strings.TrimSpace(parsed.Host) == "" {
		return "", fmt.Errorf("%w: websocket listen host is required", ErrInvalidRequest)
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return "", fmt.Errorf("%w: websocket listen path is not supported", ErrInvalidRequest)
	}
	return parsed.Host, nil
}

func NewWebSocketRouterFactory(codexHome string, storeRoot string) func() *RuntimeRouter {
	return NewWebSocketRouterFactoryWithOptions(codexHome, storeRoot, nil)
}

func NewWebSocketRouterFactoryWithOptions(codexHome string, storeRoot string, options *RuntimeRouterOptions) func() *RuntimeRouter {
	codexHome = strings.TrimSpace(codexHome)
	if codexHome == "" {
		codexHome = ".gcode"
	}
	storeRoot = strings.TrimSpace(storeRoot)
	if storeRoot == "" {
		storeRoot = filepath.Join(codexHome, "sessions")
	}
	return func() *RuntimeRouter {
		router := NewDefaultRuntimeRouterWithOptions(session.NewStore(storeRoot), codexHome, options)
		// Rust stamps `rpc.transport = "websocket"` on the request span.
		router.SetRequestTransport("websocket")
		return router
	}
}

func NewWebSocketServer(policy *WebSocketAuthPolicy, routerFactory func() *RuntimeRouter) *WebSocketServer {
	if routerFactory == nil {
		routerFactory = NewWebSocketRouterFactory(".gcode", "")
	}
	return &WebSocketServer{
		policy:         policy,
		routerFactory:  routerFactory,
		maxMessageSize: DefaultWebSocketMaxMessageSize,
	}
}

func ServeWebSocket(ctx context.Context, options *WebSocketOptions) error {
	if options == nil {
		options = &WebSocketOptions{}
	}
	if ctx == nil {
		ctx = context.Background()
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
	address, err := WebSocketListenAddress(options.Listen)
	if err != nil {
		return err
	}
	policy, err := NewWebSocketAuthPolicy(options.Auth)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	if policy.IsUnauthenticatedNonLoopbackListener(listener.Addr()) {
		_ = listener.Close()
		return errors.New("refusing to start non-loopback websocket listener without authentication")
	}
	if options.Ready != nil {
		fmt.Fprintf(options.Ready, "app-server websocket listening on %s\n", WebSocketURLFromAddr(listener.Addr()))
	}
	websocketServer := NewWebSocketServer(
		policy,
		NewWebSocketRouterFactoryWithOptions(codexHome, options.StoreRoot, preparedRuntimeOptions),
	)
	compressionEnabled := webSocketCompressionFeatureEnabled(codexHome)
	if options.CompressionEnabled != nil {
		compressionEnabled = *options.CompressionEnabled
	}
	websocketServer.SetCompressionEnabled(compressionEnabled)
	server := &http.Server{Handler: websocketServer}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
		_ = listener.Close()
	}()
	err = server.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) || (ctx.Err() != nil && errors.Is(err, net.ErrClosed)) {
		return nil
	}
	return err
}

func prepareSharedStateRuntime(ctx context.Context, codexHome string, options *RuntimeRouterOptions) (*RuntimeRouterOptions, *state.StateRuntime, error) {
	prepared := &RuntimeRouterOptions{}
	if options != nil {
		*prepared = *options
	}
	// Corruption is detected inside the database opener, before the router is
	// constructed, so the metrics sink is chosen here and reused for every later
	// metric on this router (Rust #49701).
	if prepared.DBCorruptionMetrics == nil {
		prepared.DBCorruptionMetrics = state.NewTaskMetrics()
	}
	// Managed policy applies to the database home and journal mode here, exactly
	// like Rust's `config.sqlite_config()` (Rust #57225), and an explicitly
	// configured log_dir moves logs_2.sqlite unless a managed sqlite_home wins
	// (Rust #55978).
	sqliteHomeOverride := ""
	logsHomeOverride := ""
	enforceNFSDelete := false
	if cfg, err := config.LoadEffectiveWithOptions(codexHome, &config.EffectiveOptions{IncludeManagedConfig: true}); err == nil {
		sqliteHomeOverride = cfg.SQLiteHome()
		enforceNFSDelete = cfg.UnsafeEnforceSQLiteJournalMode()
		if logDir, configured := cfg.ConfiguredLogDir(); configured && !cfg.HasManagedSQLiteHomeRequirement() {
			logsHomeOverride = logDir
		}
	}
	prepared.ConfiguredLogDir = logsHomeOverride
	if prepared.StateRuntime != nil {
		if prepared.EnableLogDB && prepared.logDBInstallation == nil {
			prepared.logDBInstallation = state.InstallLogDBHandler(prepared.StateRuntime)
		}
		return prepared, nil, nil
	}
	runtime, _, err := resolveDefaultStateRuntime(ctx, codexHome, prepared, sqliteHomeOverride, logsHomeOverride, enforceNFSDelete)
	if err != nil {
		return nil, nil, err
	}
	prepared.StateRuntime = runtime
	if prepared.EnableLogDB {
		prepared.logDBInstallation = state.InstallLogDBHandler(runtime)
	}
	return prepared, runtime, nil
}

func WebSocketURLFromAddr(addr net.Addr) string {
	if addr == nil {
		return "ws://"
	}
	if tcp, ok := addr.(*net.TCPAddr); ok && tcp != nil {
		return "ws://" + tcp.String()
	}
	return "ws://" + addr.String()
}

func (s *WebSocketServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// A browser-attached Origin header never belongs on this listener; refusing
	// it outright keeps a page from driving a local server (Rust
	// transport::reject_requests_with_origin_header).
	if strings.TrimSpace(r.Header.Get("Origin")) != "" {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	switch r.URL.Path {
	case "/readyz", "/healthz":
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
		return
	}
	// Rust routes every other path (including the client's `/rpc`) to the
	// WebSocket upgrade handler (axum `fallback(any(...))`).
	if s == nil || s.routerFactory == nil {
		http.Error(w, "app-server websocket router is not configured", http.StatusInternalServerError)
		return
	}
	if s.policy != nil {
		if err := s.policy.Authorize(r.Header, time.Now()); err != nil {
			var authErr *WebSocketAuthError
			if errors.As(err, &authErr) && authErr.StatusCode != 0 {
				http.Error(w, authErr.Message, authErr.StatusCode)
				return
			}
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
	}
	compression := WebsocketCompressionPlain
	if s.compression && r.Header.Get(WebsocketCompressionHeader) == WebsocketZstdStreamProtocol {
		compression = WebsocketCompressionZstd
		w.Header().Set(WebsocketCompressionHeader, WebsocketZstdStreamProtocol)
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{})
	if err != nil {
		return
	}
	if s.maxMessageSize > 0 {
		conn.SetReadLimit(s.maxMessageSize)
	}
	_ = serveWebSocketConnectionWithCompression(r.Context(), conn, s.routerFactory(), compression)
}

func serveWebSocketConnection(ctx context.Context, conn *websocket.Conn, router *RuntimeRouter) error {
	return serveWebSocketConnectionWithCompression(ctx, conn, router, WebsocketCompressionPlain)
}

func serveWebSocketConnectionWithCompression(
	ctx context.Context,
	conn *websocket.Conn,
	router *RuntimeRouter,
	compression WebsocketCompression,
) error {
	if conn == nil {
		return errors.New("app-server websocket connection is nil")
	}
	if router == nil {
		_ = conn.Close(websocket.StatusInternalError, "router is not configured")
		return errors.New("app-server websocket router is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	defer router.Close()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer conn.Close(websocket.StatusNormalClosure, "")
	connectionNumber := atomic.AddUint64(&websocketConnectionCounter, 1)
	connectionID := fmt.Sprintf("websocket-%d", connectionNumber)
	var compressor *ZstdEncoder
	var decompressor *ZstdDecoder
	if compression == WebsocketCompressionZstd {
		var err error
		compressor, decompressor, err = NewWebsocketZstdCodecPair(
			MaxWebsocketDecompressedMessageSize,
			WebsocketCompressionConnection{ID: connectionNumber, Kind: WebsocketCompressionConnectionListener},
		)
		if err != nil {
			_ = conn.Close(websocket.StatusInternalError, "websocket compression initialization failed")
			return err
		}
		defer compressor.Close()
		defer decompressor.Close()
	}
	var cleanupOnce sync.Once
	cleanupConnection := func() {
		cleanupOnce.Do(func() {
			// Rust #43959: once the transport stops reading, turn admission
			// closes before the handler waits for in-flight requests. Active
			// turns stay owned by Router.Close.
			router.BeginDraining()
			// Verify ceremonies stop belonging to this peer as soon as its
			// transport closes (Rust #43715).
			router.releaseUserVerificationConnection(connectionID)
			router.ConnectionClosed(connectionID)
		})
	}
	defer cleanupConnection()

	var writeMu sync.Mutex
	var errMu sync.Mutex
	var writeErr error
	setWriteErr := func(err error) {
		if err == nil {
			return
		}
		errMu.Lock()
		defer errMu.Unlock()
		if writeErr == nil {
			writeErr = err
		}
	}
	writeJSON := func(value any) error {
		data, err := json.Marshal(value)
		if err != nil {
			return err
		}
		writeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		writeMu.Lock()
		defer writeMu.Unlock()
		if compressor != nil {
			payload, err := compressor.Encode(string(data))
			if err != nil {
				return err
			}
			return conn.Write(writeCtx, websocket.MessageBinary, payload)
		}
		return conn.Write(writeCtx, websocket.MessageText, data)
	}

	router.SetNotificationSink(connectionNotificationSink{
		connectionID: connectionID,
		send: func(notification *Notification) {
			setWriteErr(writeJSON(notification))
		},
	})
	// Rust sends a request-scoped notification after its response on the wire.
	// Buffer those notifications and flush them once the response was written.
	router.SetDeferredGoalNotifications(true)
	defer router.SetDeferredGoalNotifications(false)
	router.SetServerRequestSink(connectionServerRequestSink{
		connectionID: connectionID,
		send: func(request *ServerRequest) {
			setWriteErr(writeJSON(request))
		},
	})
	defer router.SetNotificationSink(nil)
	defer router.SetServerRequestSink(nil)

	var requests sync.WaitGroup
	initialized := false
	for {
		messageType, data, err := conn.Read(ctx)
		if err != nil {
			cleanupConnection()
			requests.Wait()
			if ctx.Err() != nil || websocket.CloseStatus(err) == websocket.StatusNormalClosure || websocket.CloseStatus(err) == websocket.StatusGoingAway {
				return nil
			}
			errMu.Lock()
			defer errMu.Unlock()
			if writeErr != nil {
				return writeErr
			}
			return err
		}
		switch {
		case compression == WebsocketCompressionZstd && messageType != websocket.MessageBinary:
			_ = conn.Close(websocket.StatusUnsupportedData, "app-server websocket expects compressed binary JSON messages")
			cleanupConnection()
			requests.Wait()
			return fmt.Errorf("%w: app-server websocket expects compressed binary JSON messages", ErrInvalidRequest)
		case compression == WebsocketCompressionPlain && messageType != websocket.MessageText:
			_ = conn.Close(websocket.StatusUnsupportedData, "app-server websocket expects text JSON messages")
			cleanupConnection()
			requests.Wait()
			return fmt.Errorf("%w: app-server websocket expects text JSON messages", ErrInvalidRequest)
		case decompressor != nil:
			decodeCtx, cancelDecode := context.WithTimeout(ctx, websocketCompressionDecodeTimeout)
			decoded, err := decompressor.DecodeContext(decodeCtx, data)
			cancelDecode()
			if err != nil {
				slog.Warn("failed to decode compressed websocket message", "error", err)
				_ = conn.Close(websocket.StatusUnsupportedData, "invalid compressed JSON message")
				cleanupConnection()
				requests.Wait()
				return fmt.Errorf("%w: invalid compressed JSON message: %v", ErrInvalidRequest, err)
			}
			data = []byte(decoded)
		}
		data = []byte(strings.TrimSpace(string(data)))
		if len(data) == 0 {
			continue
		}
		response, request := decodeJSONLine(router, data)
		if request == nil {
			if response != nil {
				setWriteErr(writeJSON(response))
			}
			continue
		}
		request.ConnectionID = connectionID
		if !initialized && request.Method != MethodInitialize {
			setWriteErr(writeJSON(ErrorResponse(request.ID, -32600, "Not initialized", nil)))
			continue
		}
		if admitErr := router.admitDispatchedRequest(request); admitErr != nil {
			setWriteErr(writeJSON(turnAdmissionErrorResponse(request, admitErr)))
			continue
		}
		if request.Method == MethodInitialize {
			response := router.Handle(request)
			if response != nil && response.Error == nil {
				initialized = true
			}
			if response != nil {
				setWriteErr(writeJSON(response))
				if response.Error == nil {
					if notification := router.initializeRemoteControlStatusNotification(); notification != nil {
						setWriteErr(writeJSON(notification))
					}
					router.notifyWorkspaceRoutingToConnection(connectionID)
				}
				for _, notification := range router.FlushDeferredGoalNotifications(connectionID) {
					setWriteErr(writeJSON(notification))
				}
			}
			continue
		}
		requests.Add(1)
		go func(request *Request) {
			defer requests.Done()
			defer request.releaseTurnAdmission()
			response := router.Handle(request)
			if response != nil {
				setWriteErr(writeJSON(response))
				for _, notification := range router.FlushDeferredGoalNotifications(connectionID) {
					setWriteErr(writeJSON(notification))
				}
			}
		}(request)
	}
}
