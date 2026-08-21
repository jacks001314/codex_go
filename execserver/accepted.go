package execserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/coder/websocket"
)

// Rust #39786: embedding hosts can construct a remote exec-server environment
// from an already accepted and authenticated WebSocket and supply replacement
// connections that resume the same session.

// ConnectAcceptedClient mirrors Rust ExecServerClient::connect_accepted_websocket:
// the host has already accepted and authenticated the WebSocket, so the client
// initializes the session directly without the registry/dial flow. The initial
// connection cannot resume a session.
func ConnectAcceptedClient(conn *websocket.Conn, clientName string) (*Client, error) {
	if conn == nil {
		return nil, errors.New("accepted exec-server connection is required")
	}
	clientName = strings.TrimSpace(clientName)
	if clientName == "" {
		clientName = "codex-go-unified-exec"
	}
	client := &Client{
		clientName:   clientName,
		nextID:       1,
		nextHTTPID:   1,
		pending:      map[int64]chan clientCallResult{},
		sessions:     map[string]*clientProcessSession{},
		httpStreams:  map[string]*HTTPBodyStream{},
		inboundIDs:   map[int64]struct{}{},
		inboundSlots: make(chan struct{}, MaxInFlightServerRequests),
		done:         make(chan struct{}),
		accepted:     true,
	}
	client.open = func(ctx context.Context, resumeSessionID string, handleNotification func(string, json.RawMessage) error) (clientConnection, *InitializeResponse, error) {
		if strings.TrimSpace(resumeSessionID) != "" {
			return nil, nil, errors.New("accepted exec-server initial connection cannot resume a session")
		}
		return initializeAcceptedClientConnection(ctx, conn, clientName, "", handleNotification)
	}
	wire, initialized, err := client.open(context.Background(), "", client.handleNotification)
	if err != nil {
		return nil, err
	}
	client.conn = wire
	client.sessionID = initialized.SessionID
	go client.readLoop(wire)
	return client, nil
}

// ReplaceAcceptedConnection mirrors Rust ExecServerClient::replace_accepted_websocket:
// it retires the current transport and resumes the same exec-server session on
// a host-supplied replacement connection. Overlapping replacements are
// rejected; a failed replacement leaves the client disconnected with the
// saved session id so another replacement can be attempted.
func (c *Client) ReplaceAcceptedConnection(ctx context.Context, conn *websocket.Conn) error {
	if c == nil || conn == nil {
		return errors.New("exec-server accepted replacement requires a client and connection")
	}
	c.acceptedMu.Lock()
	defer c.acceptedMu.Unlock()
	if !c.accepted {
		return errors.New("only an accepted exec-server connection can be replaced directly")
	}
	c.mu.Lock()
	sessionID := strings.TrimSpace(c.sessionID)
	closed := c.closed
	c.mu.Unlock()
	if sessionID == "" {
		return errors.New("accepted exec-server connection is missing its session ID")
	}
	if closed {
		return errors.New("exec-server client is closed")
	}
	// Retire the current transport before attaching the replacement.
	c.mu.Lock()
	old := c.conn
	c.conn = nil
	c.mu.Unlock()
	if old != nil {
		_ = old.CloseNow()
	}
	if ctx == nil {
		ctx = context.Background()
	}
	wire, initialized, err := initializeAcceptedClientConnection(ctx, conn, c.clientName, sessionID, c.handleNotification)
	if err != nil {
		return fmt.Errorf("accepted exec-server replacement failed: %w", err)
	}
	if strings.TrimSpace(initialized.SessionID) != sessionID {
		_ = wire.CloseNow()
		return fmt.Errorf("accepted exec-server replacement initialized an unexpected session %s", initialized.SessionID)
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		_ = wire.CloseNow()
		return errors.New("exec-server client is closed")
	}
	c.conn = wire
	c.mu.Unlock()
	go c.readLoop(wire)
	return nil
}

func initializeAcceptedClientConnection(
	ctx context.Context,
	conn *websocket.Conn,
	clientName string,
	resumeSessionID string,
	handleNotification func(string, json.RawMessage) error,
) (clientConnection, *InitializeResponse, error) {
	conn.SetReadLimit(16 * 1024 * 1024)
	wire := newWebSocketClientConnection(conn)
	initialized, err := initializeClientConnection(ctx, wire, clientName, resumeSessionID, handleNotification)
	if err != nil {
		_ = wire.CloseNow()
		return nil, nil, err
	}
	return wire, initialized, nil
}
