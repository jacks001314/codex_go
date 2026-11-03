package agentboard

// In-memory launcher for the message-board MCP HTTP endpoint.
//
// Rust parity: codex-rs/message-board-mcp/src/local.rs (#58909). Runners
// register one tree-owned board per session; the loopback endpoint uses a
// generated bearer credential and never creates a SQLite board database.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"
)

// InMemoryMessageBoardMCPOptions configures the generated loopback endpoint.
type InMemoryMessageBoardMCPOptions struct {
	// AllowedHosts lists extra Host header values accepted besides loopback.
	AllowedHosts []string
	// MaxBodyBytes bounds one JSON-RPC request body.
	MaxBodyBytes int64
}

// InMemoryMessageBoardMCP is one running loopback message-board MCP endpoint.
type InMemoryMessageBoardMCP struct {
	server   *MessageBoardMCPHTTP
	boards   *InMemoryMessageBoards
	endpoint string
	token    string
	mu       sync.Mutex
	sessions map[string]*inMemoryBoardSession
	closed   bool
}

type inMemoryBoardSession struct {
	host  Host
	board *InMemoryBoard
}

// LaunchInMemoryMessageBoardMCP starts a loopback HTTP MCP endpoint with a
// generated bearer credential. The returned handle cancels itself when ctx is
// done; callers register participating sessions with AddBoard.
func LaunchInMemoryMessageBoardMCP(ctx context.Context, options InMemoryMessageBoardMCPOptions) (*InMemoryMessageBoardMCP, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	token, err := generateMessageBoardMCPToken()
	if err != nil {
		return nil, err
	}
	launcher := &InMemoryMessageBoardMCP{boards: &InMemoryMessageBoards{}, token: token, sessions: map[string]*inMemoryBoardSession{}}
	server, err := NewMessageBoardMCPHTTP(MessageBoardMCPHTTPConfig{
		Token:        token,
		AllowedHosts: options.AllowedHosts,
		MaxBodyBytes: options.MaxBodyBytes,
		Bind:         launcher.bind,
	})
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	endpoint, err := server.Serve(listener)
	if err != nil {
		_ = listener.Close()
		return nil, err
	}
	launcher.server = server
	launcher.endpoint = endpoint
	go func() {
		<-ctx.Done()
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = launcher.Shutdown(closeCtx)
	}()
	return launcher, nil
}

// AddBoard registers the shared tree-owned board for one session. Both the
// parent and every child thread use the same session id, so they see one board.
func (l *InMemoryMessageBoardMCP) AddBoard(sessionID string, host Host) error {
	if l == nil {
		return ErrMessageBoardMCPLauncherClosed
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return errors.New("message-board session id is required")
	}
	if host == nil {
		return errors.New("message-board host is required")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return ErrMessageBoardMCPLauncherClosed
	}
	if l.sessions[sessionID] != nil {
		return fmt.Errorf("message-board session %q is already registered", sessionID)
	}
	l.sessions[sessionID] = &inMemoryBoardSession{host: host, board: l.boards.Open(sessionID, host)}
	return nil
}

// Endpoint returns the /mcp URL for MCP configuration.
func (l *InMemoryMessageBoardMCP) Endpoint() string {
	if l == nil {
		return ""
	}
	return l.endpoint
}

// AuthorizationHeader returns the generated bearer credential.
func (l *InMemoryMessageBoardMCP) AuthorizationHeader() string {
	if l == nil {
		return ""
	}
	return MessageBoardMCPAuthorizationHeader(l.token)
}

// Shutdown stops HTTP and MCP sessions, waits for in-flight handlers, and
// releases every registered in-memory board.
func (l *InMemoryMessageBoardMCP) Shutdown(ctx context.Context) error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil
	}
	l.closed = true
	sessions := l.sessions
	l.sessions = map[string]*inMemoryBoardSession{}
	l.mu.Unlock()
	var err error
	if l.server != nil {
		err = l.server.Close(ctx)
	}
	for _, session := range sessions {
		if session != nil && session.board != nil {
			session.board.Close()
		}
	}
	return err
}

func (l *InMemoryMessageBoardMCP) bind(ctx context.Context, threadID string, sessionID string) (*MessageBoardMCPServer, error) {
	if l == nil {
		return nil, ErrMessageBoardMCPLauncherClosed
	}
	l.mu.Lock()
	session := l.sessions[strings.TrimSpace(sessionID)]
	l.mu.Unlock()
	if session == nil {
		return nil, ErrUnknownBoardCaller
	}
	path, err := session.host.AgentPath(ctx, threadID)
	if err != nil {
		return nil, err
	}
	return NewMessageBoardMCPServer(MessageBoardDiscussionTools, session.board, threadID, path), nil
}

// ErrMessageBoardMCPLauncherClosed reports a launcher that already shut down.
var ErrMessageBoardMCPLauncherClosed = errors.New("message-board MCP launcher is closed")

func generateMessageBoardMCPToken() (string, error) {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generate message-board MCP token: %w", err)
	}
	return hex.EncodeToString(buffer), nil
}
