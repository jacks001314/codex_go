package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
}

type WebSocketServer struct {
	policy         *WebSocketAuthPolicy
	routerFactory  func() *RuntimeRouter
	maxMessageSize int64
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
		codexHome = ".codex"
	}
	storeRoot = strings.TrimSpace(storeRoot)
	if storeRoot == "" {
		storeRoot = filepath.Join(codexHome, "sessions")
	}
	return func() *RuntimeRouter {
		return NewDefaultRuntimeRouterWithOptions(session.NewStore(storeRoot), codexHome, options)
	}
}

func NewWebSocketServer(policy *WebSocketAuthPolicy, routerFactory func() *RuntimeRouter) *WebSocketServer {
	if routerFactory == nil {
		routerFactory = NewWebSocketRouterFactory(".codex", "")
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
		codexHome = ".codex"
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
	server := &http.Server{
		Handler: NewWebSocketServer(policy, NewWebSocketRouterFactoryWithOptions(codexHome, options.StoreRoot, preparedRuntimeOptions)),
	}
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
	if prepared.StateRuntime != nil {
		if prepared.EnableLogDB && prepared.logDBInstallation == nil {
			prepared.logDBInstallation = state.InstallLogDBHandler(prepared.StateRuntime)
		}
		return prepared, nil, nil
	}
	sqliteHomeOverride := ""
	if cfg, err := config.LoadWithOptions(codexHome, nil); err == nil {
		sqliteHomeOverride = cfg.SQLiteHome()
	}
	runtime, _, err := resolveDefaultStateRuntime(ctx, codexHome, prepared, sqliteHomeOverride)
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
	switch r.URL.Path {
	case "/readyz", "/healthz":
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
		return
	case "", "/":
	default:
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
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
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{})
	if err != nil {
		return
	}
	if s.maxMessageSize > 0 {
		conn.SetReadLimit(s.maxMessageSize)
	}
	_ = serveWebSocketConnection(r.Context(), conn, s.routerFactory())
}

func serveWebSocketConnection(ctx context.Context, conn *websocket.Conn, router *RuntimeRouter) error {
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
	connectionID := fmt.Sprintf("websocket-%d", atomic.AddUint64(&websocketConnectionCounter, 1))
	var cleanupOnce sync.Once
	cleanupConnection := func() {
		cleanupOnce.Do(func() {
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
		return conn.Write(writeCtx, websocket.MessageText, data)
	}

	router.SetNotificationSink(connectionNotificationSink{
		connectionID: connectionID,
		send: func(notification *Notification) {
			setWriteErr(writeJSON(notification))
		},
	})
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
		if messageType != websocket.MessageText {
			_ = conn.Close(websocket.StatusUnsupportedData, "app-server websocket expects text JSON messages")
			cleanupConnection()
			requests.Wait()
			return fmt.Errorf("%w: app-server websocket expects text JSON messages", ErrInvalidRequest)
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
				}
			}
			continue
		}
		requests.Add(1)
		go func(request *Request) {
			defer requests.Done()
			response := router.Handle(request)
			if response != nil {
				setWriteErr(writeJSON(response))
			}
		}(request)
	}
}
