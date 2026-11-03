package agentboard

// Standalone Streamable HTTP transport for the message-board MCP tools.
//
// Rust parity: codex-rs/message-board-mcp/src/http.rs (#58904). The host binds
// board access, caller identity, and membership before serving a connection;
// this transport owns JSON-RPC framing, bearer authentication, host checks,
// MCP sessions, and shutdown.

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	// MessageBoardMCPHTTPPath is the single endpoint the server exposes.
	MessageBoardMCPHTTPPath = "/mcp"
	// MessageBoardMCPSessionHeader carries the MCP session id.
	MessageBoardMCPSessionHeader = "Mcp-Session-Id"
	// DefaultMessageBoardMCPMaxBodyBytes bounds one JSON-RPC request body.
	DefaultMessageBoardMCPMaxBodyBytes = 4 << 20
	// messageBoardMCPProtocolVersion is the version returned when the client
	// does not request a specific one.
	messageBoardMCPProtocolVersion = "2025-06-18"
)

var (
	// ErrUnknownBoardCaller reports caller metadata that is not a member of the
	// session's spawn tree.
	ErrUnknownBoardCaller = errors.New("caller is not a member of this session")
	// ErrMessageBoardMCPNoToken reports an empty bearer token, which would let
	// any caller assert thread identity.
	ErrMessageBoardMCPNoToken = errors.New("MCP bearer token must not be empty")
	// ErrMessageBoardMCPInvalidHost reports a Host header outside the allowed
	// set; the transport refuses to trust it.
	ErrMessageBoardMCPInvalidHost = errors.New("MCP request host is not allowed")
)

// MessageBoardMCPBind turns the caller metadata asserted by a trusted host into
// an adapter bound to the caller's board, identity, and path. Membership
// validation belongs to the binder, so an invalid thread/session pair fails
// here before any board operation.
type MessageBoardMCPBind func(ctx context.Context, threadID string, sessionID string) (*MessageBoardMCPServer, error)

// MessageBoardMCPHTTPConfig configures the standalone HTTP MCP endpoint.
type MessageBoardMCPHTTPConfig struct {
	// Token is the bearer credential a trusted host presents. It is required.
	Token string
	// AllowedHosts lists extra Host header values accepted besides loopback.
	AllowedHosts []string
	// Bind resolves the caller for tools/call.
	Bind MessageBoardMCPBind
	// Tools defaults to the discussion tool set when empty.
	Tools []MessageBoardTool
	// MaxBodyBytes bounds one request body.
	MaxBodyBytes int64
}

// MessageBoardMCPHTTP serves the message-board tools over Streamable HTTP.
type MessageBoardMCPHTTP struct {
	config   MessageBoardMCPHTTPConfig
	server   *http.Server
	listener net.Listener
	ctx      context.Context
	cancel   context.CancelFunc
	done     chan struct{}
	sessions sync.Map
	handlers sync.WaitGroup
}

type messageBoardMCPSession struct {
	id       string
	mu       sync.Mutex
	adapters map[string]*MessageBoardMCPServer
}

type mcpRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type mcpRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type mcpRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *mcpRPCError    `json:"error,omitempty"`
}

func newMessageBoardMCPRPCError(code int, message string) *mcpRPCError {
	return &mcpRPCError{Code: code, Message: message}
}

// NewMessageBoardMCPHTTP validates the configuration without starting a server.
func NewMessageBoardMCPHTTP(config MessageBoardMCPHTTPConfig) (*MessageBoardMCPHTTP, error) {
	if strings.TrimSpace(config.Token) == "" {
		return nil, ErrMessageBoardMCPNoToken
	}
	if config.Bind == nil {
		return nil, errors.New("MCP caller binder is required")
	}
	if len(config.Tools) == 0 {
		config.Tools = append([]MessageBoardTool(nil), MessageBoardDiscussionTools...)
	}
	if config.MaxBodyBytes <= 0 {
		config.MaxBodyBytes = DefaultMessageBoardMCPMaxBodyBytes
	}
	config.AllowedHosts = normalizeMCPAllowedHosts(config.AllowedHosts)
	server := &MessageBoardMCPHTTP{config: config, done: make(chan struct{})}
	server.ctx, server.cancel = context.WithCancel(context.Background())
	return server, nil
}

// Serve starts serving on listener and returns the bound endpoint URL.
func (s *MessageBoardMCPHTTP) Serve(listener net.Listener) (string, error) {
	if s == nil || listener == nil {
		return "", errors.New("MCP HTTP server is unavailable")
	}
	s.listener = listener
	s.server = &http.Server{Handler: s}
	go func() {
		defer close(s.done)
		_ = s.server.Serve(listener)
	}()
	return "http://" + listener.Addr().String() + MessageBoardMCPHTTPPath, nil
}

// Close cancels pending bindings, stops accepting connections, drains
// in-flight handlers, and releases every MCP session before returning.
func (s *MessageBoardMCPHTTP) Close(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s.cancel()
	if s.server == nil {
		return nil
	}
	var err error
	err = s.server.Shutdown(ctx)
	<-s.done
	s.handlers.Wait()
	s.sessions.Range(func(key, _ any) bool {
		s.sessions.Delete(key)
		return true
	})
	return err
}

func (s *MessageBoardMCPHTTP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != MessageBoardMCPHTTPPath {
		http.NotFound(w, r)
		return
	}
	if err := s.authorize(r); err != nil {
		status := http.StatusUnauthorized
		if errors.Is(err, ErrMessageBoardMCPInvalidHost) {
			status = http.StatusForbidden
		}
		http.Error(w, err.Error(), status)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, s.config.MaxBodyBytes))
	if err != nil {
		http.Error(w, "request body is too large", http.StatusRequestEntityTooLarge)
		return
	}
	var request mcpRPCRequest
	if err := json.Unmarshal(body, &request); err != nil {
		s.writeRPCResponse(w, http.StatusBadRequest, mcpRPCResponse{JSONRPC: "2.0", Error: newMessageBoardMCPRPCError(-32700, "Parse error")}, "")
		return
	}
	if request.JSONRPC != "2.0" || strings.TrimSpace(request.Method) == "" {
		s.writeRPCResponse(w, http.StatusBadRequest, mcpRPCResponse{JSONRPC: "2.0", ID: request.ID, Error: newMessageBoardMCPRPCError(-32600, "Invalid Request")}, "")
		return
	}
	if len(request.ID) == 0 || string(request.ID) == "null" {
		// Notifications carry no response. Initialized is the only one that
		// matters to this server.
		w.WriteHeader(http.StatusAccepted)
		return
	}
	s.handlers.Add(1)
	defer s.handlers.Done()
	response, sessionHeader, status := s.dispatch(r.Context(), request, r.Header.Get(MessageBoardMCPSessionHeader))
	s.writeRPCResponse(w, status, response, sessionHeader)
}

func (s *MessageBoardMCPHTTP) writeRPCResponse(w http.ResponseWriter, status int, response mcpRPCResponse, sessionID string) {
	if sessionID != "" {
		w.Header().Set(MessageBoardMCPSessionHeader, sessionID)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(response)
}

func (s *MessageBoardMCPHTTP) authorize(r *http.Request) error {
	authorization := r.Header.Get("Authorization")
	expected := "Bearer " + s.config.Token
	if subtle.ConstantTimeCompare([]byte(authorization), []byte(expected)) != 1 {
		return errors.New("invalid MCP bearer token")
	}
	host := mcpRequestHost(r)
	if host == "" {
		return ErrMessageBoardMCPInvalidHost
	}
	if isLoopbackMCPHost(host) {
		return nil
	}
	for _, allowed := range s.config.AllowedHosts {
		if strings.EqualFold(allowed, host) {
			return nil
		}
	}
	return ErrMessageBoardMCPInvalidHost
}

func (s *MessageBoardMCPHTTP) dispatch(ctx context.Context, request mcpRPCRequest, sessionID string) (mcpRPCResponse, string, int) {
	response := mcpRPCResponse{JSONRPC: "2.0", ID: request.ID}
	switch request.Method {
	case "initialize":
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(request.Params, &params)
		version := strings.TrimSpace(params.ProtocolVersion)
		if version == "" {
			version = messageBoardMCPProtocolVersion
		}
		session := &messageBoardMCPSession{id: uuid.NewString(), adapters: map[string]*MessageBoardMCPServer{}}
		s.sessions.Store(session.id, session)
		response.Result = map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "codex-message-board", "version": "1"},
		}
		return response, session.id, http.StatusOK
	case "ping":
		response.Result = map[string]any{}
		return response, "", http.StatusOK
	case "tools/list", "tools/call":
		if sessionID == "" {
			response.Error = newMessageBoardMCPRPCError(-32600, "MCP session id is required")
			return response, "", http.StatusBadRequest
		}
		value, ok := s.sessions.Load(sessionID)
		if !ok {
			response.Error = newMessageBoardMCPRPCError(-32600, "unknown MCP session")
			return response, "", http.StatusNotFound
		}
		session := value.(*messageBoardMCPSession)
		if request.Method == "tools/list" {
			cursor, err := mcpListCursor(request.Params)
			if err != nil {
				response.Error = newMessageBoardMCPRPCError(MessageBoardMCPInvalidParamsCode, err.Error())
				return response, "", http.StatusOK
			}
			tools, err := s.listTools(cursor)
			if err != nil {
				response.Error = mcpErrorFromAdapter(err)
				return response, "", http.StatusOK
			}
			response.Result = map[string]any{"tools": tools}
			return response, "", http.StatusOK
		}
		var params struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
			Meta      struct {
				ThreadID  string `json:"threadId"`
				SessionID string `json:"sessionId"`
			} `json:"_meta"`
		}
		if err := json.Unmarshal(request.Params, &params); err != nil {
			response.Error = newMessageBoardMCPRPCError(MessageBoardMCPInvalidParamsCode, "Invalid message-board arguments")
			return response, "", http.StatusOK
		}
		threadID := strings.TrimSpace(params.Meta.ThreadID)
		callerSession := strings.TrimSpace(params.Meta.SessionID)
		if threadID == "" || callerSession == "" {
			response.Error = newMessageBoardMCPRPCError(MessageBoardMCPInvalidParamsCode, "Calls require threadId and sessionId metadata")
			return response, "", http.StatusOK
		}
		adapter, err := session.adapter(s.ctx, threadID, callerSession, s.config.Bind)
		if err != nil {
			response.Error = newMessageBoardMCPRPCError(MessageBoardMCPInvalidParamsCode, err.Error())
			return response, "", http.StatusOK
		}
		result, err := adapter.CallTool(ctx, params.Name, params.Arguments, mcpRawRequestID(request.ID))
		if err != nil {
			response.Error = mcpErrorFromAdapter(err)
			return response, "", http.StatusOK
		}
		response.Result = result
		return response, "", http.StatusOK
	default:
		response.Error = newMessageBoardMCPRPCError(-32601, "Method not found")
		return response, "", http.StatusOK
	}
}

func (s *MessageBoardMCPHTTP) listTools(cursor *string) ([]MessageBoardMCPTool, error) {
	adapter := NewMessageBoardMCPServer(s.config.Tools, nil, "", "")
	return adapter.ListTools(cursor)
}

func (s *messageBoardMCPSession) adapter(ctx context.Context, threadID, callerSession string, bind MessageBoardMCPBind) (*MessageBoardMCPServer, error) {
	if s == nil || bind == nil {
		return nil, errors.New("message-board MCP session is unavailable")
	}
	key := threadID + "\x00" + callerSession
	s.mu.Lock()
	defer s.mu.Unlock()
	if adapter := s.adapters[key]; adapter != nil {
		return adapter, nil
	}
	adapter, err := bind(ctx, threadID, callerSession)
	if err != nil {
		return nil, err
	}
	if adapter == nil {
		return nil, errors.New("message-board binder returned no server")
	}
	s.adapters[key] = adapter
	return adapter, nil
}

func mcpListCursor(params json.RawMessage) (*string, error) {
	if len(params) == 0 {
		return nil, nil
	}
	var decoded struct {
		Cursor *string `json:"cursor"`
	}
	if err := json.Unmarshal(params, &decoded); err != nil {
		return nil, errors.New("Invalid message-board pagination")
	}
	return decoded.Cursor, nil
}

func mcpRawRequestID(id json.RawMessage) any {
	if len(id) == 0 {
		return nil
	}
	var value any
	if err := json.Unmarshal(id, &value); err != nil {
		return nil
	}
	return value
}

func mcpErrorFromAdapter(err error) *mcpRPCError {
	var protocolError *MessageBoardMCPError
	if errors.As(err, &protocolError) {
		return newMessageBoardMCPRPCError(protocolError.Code, protocolError.Message)
	}
	return newMessageBoardMCPRPCError(MessageBoardMCPInternalErrorCode, "Message-board tool failed")
}

func normalizeMCPAllowedHosts(hosts []string) []string {
	normalized := make([]string, 0, len(hosts))
	for _, host := range hosts {
		host = strings.TrimSpace(host)
		if host == "" {
			continue
		}
		normalized = append(normalized, host)
	}
	return normalized
}

func mcpRequestHost(r *http.Request) string {
	if r == nil {
		return ""
	}
	host := strings.TrimSpace(r.Host)
	if host == "" {
		return ""
	}
	if parsed, err := url.Parse("http://" + host); err == nil && parsed.Hostname() != "" {
		return parsed.Hostname()
	}
	return strings.Trim(host, "[]")
}

func isLoopbackMCPHost(host string) bool {
	host = strings.Trim(strings.ToLower(strings.TrimSpace(host)), "[]")
	if host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// MessageBoardMCPHTTPServe is a convenience wrapper that binds a loopback
// listener and serves until ctx is cancelled.
func MessageBoardMCPHTTPServe(ctx context.Context, address string, config MessageBoardMCPHTTPConfig) (*MessageBoardMCPHTTP, string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	server, err := NewMessageBoardMCPHTTP(config)
	if err != nil {
		return nil, "", err
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, "", err
	}
	endpoint, err := server.Serve(listener)
	if err != nil {
		_ = listener.Close()
		return nil, "", err
	}
	go func() {
		<-ctx.Done()
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Close(closeCtx)
	}()
	return server, endpoint, nil
}

// MessageBoardMCPAuthorizationHeader returns the bearer header for a token.
func MessageBoardMCPAuthorizationHeader(token string) string {
	return "Bearer " + strings.TrimSpace(token)
}
