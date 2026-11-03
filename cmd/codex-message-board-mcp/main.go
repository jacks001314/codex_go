package main

// Standalone message-board MCP server.
//
// Rust parity: codex-rs/message-board-mcp/src/main.rs (#58904). It serves the
// discussion tools on /mcp through ordinary MCP configuration, backed by the
// local SQLite board and a read-only Codex thread registry.

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"codex_go/agent"
	"codex_go/agentboard"
	"codex_go/state"
)

const defaultMessageBoardMCPPort = 7777

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

type messageBoardMCPOptions struct {
	host         string
	port         int
	codexHome    string
	board        string
	token        string
	allowedHosts stringListFlag
}

type stringListFlag []string

func (f *stringListFlag) String() string { return strings.Join(*f, ",") }

func (f *stringListFlag) Set(value string) error {
	value = strings.TrimSpace(value)
	if value != "" {
		*f = append(*f, value)
	}
	return nil
}

func run(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("codex-message-board-mcp", flag.ContinueOnError)
	flags.SetOutput(stderr)
	options := messageBoardMCPOptions{}
	flags.StringVar(&options.host, "host", "127.0.0.1", "loopback address to bind")
	flags.IntVar(&options.port, "port", defaultMessageBoardMCPPort, "port to bind")
	flags.StringVar(&options.codexHome, "codex-home", "", "Codex home directory (defaults to CODEX_HOME or ~/.codex)")
	flags.StringVar(&options.board, "board", "codex", "message-board identity shared by participating threads")
	flags.StringVar(&options.token, "token", "", "bearer token (defaults to CODEX_MESSAGE_BOARD_TOKEN)")
	flags.Var(&options.allowedHosts, "allowed-host", "additional Host header value to accept (repeatable)")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(options.token) == "" {
		options.token = os.Getenv("CODEX_MESSAGE_BOARD_TOKEN")
	}
	if options.codexHome == "" {
		options.codexHome = defaultMessageBoardMCPCodexHome()
	}
	if strings.TrimSpace(options.token) == "" {
		fmt.Fprintln(stderr, "codex-message-board-mcp: a bearer token is required (--token or CODEX_MESSAGE_BOARD_TOKEN)")
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := serve(ctx, options, stdout, stderr); err != nil {
		fmt.Fprintln(stderr, "codex-message-board-mcp:", err)
		return 1
	}
	return 0
}

func serve(ctx context.Context, options messageBoardMCPOptions, stdout io.Writer, stderr io.Writer) error {
	sqliteConfig, err := state.NewSqliteConfig(options.codexHome)
	if err != nil {
		return err
	}
	registry, err := openMessageBoardMCPRegistry(ctx, sqliteConfig.StateDBPath())
	if err != nil {
		return err
	}
	defer registry.Close()
	board := agentboard.NewLocalBoard(sqliteConfig, options.board)
	host := &registryBoardHost{registry: registry, now: time.Now}
	binder := newMessageBoardMCPBinder(board, host)
	defer binder.Close()
	server, err := agentboard.NewMessageBoardMCPHTTP(agentboard.MessageBoardMCPHTTPConfig{
		Token:        options.token,
		AllowedHosts: options.allowedHosts,
		Bind:         binder.Bind,
	})
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(options.host, fmt.Sprintf("%d", options.port)))
	if err != nil {
		return err
	}
	endpoint, err := server.Serve(listener)
	if err != nil {
		_ = listener.Close()
		return err
	}
	fmt.Fprintln(stdout, endpoint)
	if stderr != nil {
		fmt.Fprintf(stderr, "codex-message-board-mcp: serving %s\n", endpoint)
	}
	<-ctx.Done()
	closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return server.Close(closeCtx)
}

func defaultMessageBoardMCPCodexHome() string {
	if home := strings.TrimSpace(os.Getenv("CODEX_HOME")); home != "" {
		return home
	}
	userHome, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(userHome) == "" {
		return ""
	}
	return filepath.Join(userHome, ".codex")
}

// registryBoardHost resolves membership and clocks from the persisted thread
// registry. The standalone server has no live runtime, so notifications are
// skipped instead of starting idle agents.
type registryBoardHost struct {
	registry *messageBoardMCPRegistry
	now      func() time.Time
}

func (h *registryBoardHost) AgentPath(ctx context.Context, caller string) (agent.AgentPath, error) {
	path, ok, err := h.registry.AgentPath(ctx, caller)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", agentboard.ErrUnknownBoardCaller
	}
	return path, nil
}

func (h *registryBoardHost) ResolveAgent(ctx context.Context, path agent.AgentPath) (string, error) {
	caller, ok, err := h.registry.ResolveAgent(ctx, path)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", agentboard.ErrUnknownBoardCaller
	}
	return caller, nil
}

func (h *registryBoardHost) CurrentTime(context.Context, string) (time.Time, error) {
	if h.now == nil {
		return time.Now().UTC(), nil
	}
	return h.now().UTC(), nil
}

func (h *registryBoardHost) Notify(context.Context, string, agentboard.PostPreview) (agentboard.NotificationDelivery, error) {
	return agentboard.NotificationSkippedInactive, nil
}

// messageBoardMCPRegistry is the read-only view of persisted agent membership.
type messageBoardMCPRegistry struct {
	db *sql.DB
}

func openMessageBoardMCPRegistry(ctx context.Context, stateDBPath string) (*messageBoardMCPRegistry, error) {
	registry := &messageBoardMCPRegistry{}
	path := strings.TrimSpace(stateDBPath)
	if path == "" {
		return registry, nil
	}
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// Startup without an existing registry still serves, but every
			// caller is rejected until one exists.
			return registry, nil
		}
		return nil, err
	}
	db, err := state.OpenSQLite(ctx, "file:"+path+"?mode=ro")
	if err != nil {
		return nil, err
	}
	registry.db = db
	return registry, nil
}

func (r *messageBoardMCPRegistry) Close() error {
	if r == nil || r.db == nil {
		return nil
	}
	return r.db.Close()
}

func (r *messageBoardMCPRegistry) AgentPath(ctx context.Context, threadID string) (agent.AgentPath, bool, error) {
	if r == nil || r.db == nil {
		return "", false, nil
	}
	var path sql.NullString
	err := r.db.QueryRowContext(ctx, `SELECT agent_path FROM threads WHERE id = ?`, threadID).Scan(&path)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if !path.Valid || strings.TrimSpace(path.String) == "" {
		return "", false, nil
	}
	return agent.AgentPath(strings.TrimSpace(path.String)), true, nil
}

func (r *messageBoardMCPRegistry) ResolveAgent(ctx context.Context, path agent.AgentPath) (string, bool, error) {
	if r == nil || r.db == nil {
		return "", false, nil
	}
	var threadID string
	err := r.db.QueryRowContext(ctx, `SELECT id FROM threads WHERE agent_path = ? ORDER BY updated_at DESC LIMIT 1`, string(path)).Scan(&threadID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return threadID, true, nil
}

// Member reports whether threadID belongs to sessionID's spawn tree.
func (r *messageBoardMCPRegistry) Member(ctx context.Context, threadID string, sessionID string) (bool, error) {
	if r == nil || r.db == nil {
		return false, nil
	}
	current := threadID
	for depth := 0; depth < 64; depth++ {
		if current == sessionID {
			return true, nil
		}
		var parent sql.NullString
		err := r.db.QueryRowContext(ctx, `SELECT parent_thread_id FROM thread_spawn_edges WHERE child_thread_id = ?`, current).Scan(&parent)
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if !parent.Valid || strings.TrimSpace(parent.String) == "" {
			return false, nil
		}
		current = parent.String
	}
	return false, nil
}

// messageBoardMCPBinder opens one board handle per caller and keeps adapters
// alive for the server lifetime so MCP request-id deduplication stays scoped
// to a caller.
type messageBoardMCPBinder struct {
	board  *agentboard.LocalBoard
	host   agentboard.Host
	mu     sync.Mutex
	boards map[string]*agentboard.LocalAgentMessageBoard
	reg    *messageBoardMCPRegistry
}

func newMessageBoardMCPBinder(board *agentboard.LocalBoard, host *registryBoardHost) *messageBoardMCPBinder {
	binder := &messageBoardMCPBinder{board: board, host: host, boards: map[string]*agentboard.LocalAgentMessageBoard{}, reg: host.registry}
	return binder
}

func (b *messageBoardMCPBinder) Bind(ctx context.Context, threadID string, sessionID string) (*agentboard.MessageBoardMCPServer, error) {
	if b == nil || b.board == nil {
		return nil, errors.New("message-board backend is unavailable")
	}
	member, err := b.reg.Member(ctx, threadID, sessionID)
	if err != nil {
		return nil, err
	}
	if !member {
		return nil, agentboard.ErrUnknownBoardCaller
	}
	path, err := b.host.AgentPath(ctx, threadID)
	if err != nil {
		return nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	board := b.boards[threadID]
	if board == nil {
		connection := b.board.ReadWrite()
		board, err = connection.Open(ctx, b.host)
		if err != nil {
			return nil, err
		}
		b.boards[threadID] = board
	}
	return agentboard.NewMessageBoardMCPServer(agentboard.MessageBoardDiscussionTools, board, threadID, path), nil
}

func (b *messageBoardMCPBinder) Close() error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for caller, board := range b.boards {
		board.Close()
		delete(b.boards, caller)
	}
	return nil
}
