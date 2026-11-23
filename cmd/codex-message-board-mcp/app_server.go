package main

// Rust parity: codex-rs/message-board-mcp/src/app_server.rs (#63354).
//
// Optional loopback app-server connection for best-effort board notifications.
// Requests share one reply routing without holding the connection cache lock.
// Disconnected or cancelled transports are replaced for new notices, never
// replayed.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"codex_go/appserver"
)

const (
	// messageBoardNoticeLimit matches the remote board limit, counting the
	// content plus both routing paths.
	messageBoardNoticeLimit = 1024
	messageBoardClientName  = "codex_message_board"
)

// messageBoardAppServerRequestTimeout bounds one connection attempt or notice.
var messageBoardAppServerRequestTimeout = 10 * time.Second

type messageBoardAppServer struct {
	url string
	mu  sync.Mutex
	// conn is the cached connection. A closed connection is replaced on the next
	// notice instead of being reused.
	conn *messageBoardAppServerConnection
}

type messageBoardAppServerConnection struct {
	conn     *websocket.Conn
	writeMu  sync.Mutex
	nextID   atomic.Int64
	mu       sync.Mutex
	pending  map[string]chan messageBoardRPCResponse
	closed   chan struct{}
	closeOne sync.Once
	cancel   context.CancelFunc
}

type messageBoardRPCResponse struct {
	ID     json.RawMessage       `json:"id"`
	Result json.RawMessage       `json:"result"`
	Error  *messageBoardRPCError `json:"error"`
}

type messageBoardRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// messageBoardAppServerRejection reports a matched JSON-RPC error reply.
type messageBoardAppServerRejection struct {
	code int
}

func (e *messageBoardAppServerRejection) Error() string {
	return fmt.Sprintf("app-server rejected request (code %d)", e.code)
}

// newMessageBoardAppServer validates the endpoint and opens the first
// connection so a bad URL fails at startup rather than on the first post.
func newMessageBoardAppServer(ctx context.Context, raw string) (*messageBoardAppServer, error) {
	endpoint, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("--app-server requires a loopback ws:// URL without credentials: %w", err)
	}
	if !isMessageBoardLoopbackWebSocketURL(endpoint) {
		return nil, errors.New("--app-server requires a loopback ws:// URL without credentials")
	}
	server := &messageBoardAppServer{url: endpoint.String()}
	connectCtx, cancel := context.WithTimeout(ctx, messageBoardAppServerRequestTimeout)
	defer cancel()
	connection, err := dialMessageBoardAppServer(connectCtx, server.url)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return nil, errors.New("app-server initialization timed out")
		}
		return nil, err
	}
	server.conn = connection
	return server, nil
}

// isMessageBoardLoopbackWebSocketURL accepts a plain ws:// endpoint on
// localhost or a loopback address with no credentials, query or fragment.
func isMessageBoardLoopbackWebSocketURL(endpoint *url.URL) bool {
	if endpoint == nil || endpoint.Scheme != "ws" || endpoint.User != nil ||
		endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return false
	}
	if endpoint.Path != "" && endpoint.Path != "/" {
		return false
	}
	host := strings.TrimSpace(endpoint.Hostname())
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// inject queues one notice in the recipient's mailbox through thread/injectItems
// without starting a turn.
func (s *messageBoardAppServer) inject(ctx context.Context, recipient string, notice messageBoardNotice) error {
	if s == nil {
		return errors.New("app-server notifications are not configured")
	}
	if notice.overLimit() {
		return errors.New("remote board notice exceeds the context budget")
	}
	item, err := notice.item()
	if err != nil {
		return err
	}
	_, err = s.request(ctx, string(appserver.MethodThreadInjectItems), appserver.ThreadInjectItemsParams{
		ThreadID: recipient,
		Items:    []json.RawMessage{item},
	})
	return err
}

// request sends one JSON-RPC request on the cached connection. A timed out or
// broken transport is dropped so the next notice reconnects instead of reusing
// it; a matched rejection leaves the transport usable.
func (s *messageBoardAppServer) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	callCtx, cancel := context.WithTimeout(ctx, messageBoardAppServerRequestTimeout)
	defer cancel()
	connection, err := s.connection(callCtx)
	if err != nil {
		return nil, err
	}
	result, err := connection.request(callCtx, method, params)
	if err == nil {
		return result, nil
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		s.invalidate(connection)
		return nil, errors.New("app-server notification timed out")
	}
	var rejection *messageBoardAppServerRejection
	if errors.As(err, &rejection) {
		return nil, err
	}
	s.invalidate(connection)
	return nil, err
}

// invalidate drops a connection that can no longer carry requests so the next
// notice reconnects instead of reusing it. The failed notice is never replayed.
func (s *messageBoardAppServer) invalidate(connection *messageBoardAppServerConnection) {
	s.mu.Lock()
	if s.conn == connection {
		s.conn = nil
	}
	s.mu.Unlock()
	connection.close(errors.New("connection discarded"))
}

// connection returns the cached transport, reconnecting once for new notices
// when the previous one closed.
func (s *messageBoardAppServer) connection(ctx context.Context) (*messageBoardAppServerConnection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn != nil && !s.conn.isClosed() {
		return s.conn, nil
	}
	connection, err := dialMessageBoardAppServer(ctx, s.url)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return nil, errors.New("app-server notification timed out")
		}
		return nil, err
	}
	s.conn = connection
	return connection, nil
}

func dialMessageBoardAppServer(ctx context.Context, endpoint string) (*messageBoardAppServerConnection, error) {
	conn, _, err := websocket.Dial(ctx, endpoint, nil)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return nil, err
		}
		return nil, errors.New("failed to connect to the local app-server")
	}
	readCtx, cancelRead := context.WithCancel(context.Background())
	connection := &messageBoardAppServerConnection{
		conn:    conn,
		pending: map[string]chan messageBoardRPCResponse{},
		closed:  make(chan struct{}),
		cancel:  cancelRead,
	}
	go connection.readLoop(readCtx)
	// Keep the shared client's event queue drained even between notices; the
	// connection owns the reader and drops it when it closes.
	if err := connection.initialize(ctx); err != nil {
		connection.close(err)
		return nil, err
	}
	return connection, nil
}

func (c *messageBoardAppServerConnection) initialize(ctx context.Context) error {
	params := appserver.InitializeParams{
		ClientInfo: appserver.ClientInfo{
			Name:    messageBoardClientName,
			Version: appserver.CodexVersion(),
		},
		Capabilities: &appserver.InitializeCapabilities{ExperimentalAPI: true},
	}
	if _, err := c.request(ctx, string(appserver.MethodInitialize), params); err != nil {
		return errors.New("failed to connect to the local app-server")
	}
	payload, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "initialized"})
	if err != nil {
		return err
	}
	return c.write(ctx, payload)
}

func (c *messageBoardAppServerConnection) isClosed() bool {
	if c == nil {
		return true
	}
	select {
	case <-c.closed:
		return true
	default:
		return false
	}
}

func (c *messageBoardAppServerConnection) close(cause error) {
	if c == nil {
		return
	}
	c.closeOne.Do(func() {
		close(c.closed)
		if c.cancel != nil {
			c.cancel()
		}
		_ = c.conn.Close(websocket.StatusNormalClosure, "")
		c.mu.Lock()
		for key, pending := range c.pending {
			delete(c.pending, key)
			close(pending)
		}
		c.mu.Unlock()
	})
	_ = cause
}

func (c *messageBoardAppServerConnection) readLoop(ctx context.Context) {
	for {
		_, data, err := c.conn.Read(ctx)
		if err != nil {
			c.close(err)
			return
		}
		var message messageBoardRPCResponse
		if err := json.Unmarshal(data, &message); err != nil {
			continue
		}
		// Notifications and server requests are ignored: the board only waits
		// for replies to its own injections.
		var envelope struct {
			Method string `json:"method"`
		}
		_ = json.Unmarshal(data, &envelope)
		if strings.TrimSpace(envelope.Method) != "" || len(message.ID) == 0 {
			continue
		}
		key := messageBoardRequestKey(message.ID)
		c.mu.Lock()
		pending := c.pending[key]
		c.mu.Unlock()
		if pending == nil {
			continue
		}
		select {
		case pending <- message:
		default:
		}
	}
}

func (c *messageBoardAppServerConnection) write(ctx context.Context, payload []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.isClosed() {
		return errors.New("app-server notification connection failed")
	}
	if err := c.conn.Write(ctx, websocket.MessageText, payload); err != nil {
		return errors.New("app-server notification connection failed")
	}
	return nil
}

func (c *messageBoardAppServerConnection) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := fmt.Sprintf("board-%d-%d", time.Now().UnixNano(), c.nextID.Add(1))
	pending := make(chan messageBoardRPCResponse, 1)
	c.mu.Lock()
	c.pending[id] = pending
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()
	payload, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	})
	if err != nil {
		return nil, err
	}
	if err := c.write(ctx, payload); err != nil {
		return nil, err
	}
	select {
	case response, ok := <-pending:
		if !ok {
			return nil, errors.New("app-server notification connection failed")
		}
		if response.Error != nil {
			return nil, &messageBoardAppServerRejection{code: response.Error.Code}
		}
		return response.Result, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.closed:
		return nil, errors.New("app-server notification connection failed")
	}
}

func messageBoardRequestKey(id json.RawMessage) string {
	trimmed := strings.TrimSpace(string(id))
	if unquoted := strings.Trim(trimmed, "\""); unquoted != trimmed {
		return unquoted
	}
	return trimmed
}

// messageBoardNotice is one board post prepared for delivery to a native agent.
type messageBoardNotice struct {
	author    string
	recipient string
	content   string
}

func (n messageBoardNotice) overLimit() bool {
	return len(n.content)+len(n.author)+len(n.recipient) > messageBoardNoticeLimit
}

// item renders the inter-agent communication as the agent_message response
// item thread/injectItems accepts (Rust InterAgentCommunication::to_model_input_item).
func (n messageBoardNotice) item() (json.RawMessage, error) {
	return json.Marshal(map[string]any{
		"type":      "agent_message",
		"author":    n.author,
		"recipient": n.recipient,
		"content": []any{
			map[string]any{"type": "input_text", "text": n.content},
		},
	})
}
