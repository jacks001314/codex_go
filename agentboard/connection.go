package agentboard

import (
	"context"
	"errors"
	"strings"
	"sync"

	"codex_go/agent"
	"codex_go/state"
)

// LocalBoard is shared local storage from which a host grants individual
// runtime connections.
//
// Rust parity: codex-rs/ext/agent-message-board/src/local/connection.rs
// (#53400).
type LocalBoard struct {
	sqlite   state.SqliteConfig
	identity string
	options  LocalBoardOptions
}

// NewLocalBoard binds one board identity to a SQLite configuration.
func NewLocalBoard(sqlite state.SqliteConfig, identity string) *LocalBoard {
	return &LocalBoard{sqlite: sqlite, identity: strings.TrimSpace(identity)}
}

// WithOptions selects the storage namespace shared by this board's
// connections (Rust #55971). Reopening must pass the same options.
func (b *LocalBoard) WithOptions(options LocalBoardOptions) *LocalBoard {
	if b == nil {
		return nil
	}
	b.options = options
	return b
}

// ReadOnly returns a connection that may read and manage its own
// subscriptions but cannot mutate board content.
func (b *LocalBoard) ReadOnly() *LocalBoardConnection {
	return &LocalBoardConnection{board: b, access: accessReadOnly}
}

// ReadWrite returns a connection with the legacy full-write access.
func (b *LocalBoard) ReadWrite() *LocalBoardConnection {
	return &LocalBoardConnection{board: b, access: accessReadWrite}
}

// LocalBoardConnection is a host-supplied access grant for one runtime. The
// grant is fixed per connection and must be supplied again for cold resume or
// fork. It is not inherited through spawn_agent.
type LocalBoardConnection struct {
	board    *LocalBoard
	access   accessMode
	mu       sync.Mutex
	database string
}

// Identity returns the shared board identity.
func (c *LocalBoardConnection) Identity() string {
	if c == nil || c.board == nil {
		return ""
	}
	return c.board.identity
}

// SameBoard reports whether both connections were opened against the same
// board identity and canonical database path.
func (c *LocalBoardConnection) SameBoard(other *LocalBoardConnection) bool {
	if c == nil || other == nil || c.Identity() == "" || c.Identity() != other.Identity() {
		return false
	}
	c.mu.Lock()
	database := c.database
	c.mu.Unlock()
	other.mu.Lock()
	otherDatabase := other.database
	other.mu.Unlock()
	return database != "" && database == otherDatabase
}

// CallerPath gives independently created runtimes a unique path independent of
// their agent trees.
func (c *LocalBoardConnection) CallerPath(caller string) (agent.AgentPath, error) {
	caller = strings.TrimSpace(caller)
	if caller == "" {
		return agent.AgentPathRoot, errors.New("invalid board caller identity")
	}
	return agent.AgentPath("/root/" + strings.ReplaceAll(caller, "-", "")), nil
}

// Open attaches the fixed access grant to a board handle. The first open
// captures the canonical database path so symlink changes or a reused
// connection cannot silently point at another database.
func (c *LocalBoardConnection) Open(ctx context.Context, host Host) (*LocalAgentMessageBoard, error) {
	if c == nil || c.board == nil || c.board.identity == "" {
		return nil, invalid("invalid board connection")
	}
	board, err := OpenLocalBoardWithOptions(ctx, c.board.sqlite, c.board.options, c.board.identity, host)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	if c.database == "" {
		c.database = board.poolPath
	} else if c.database != board.poolPath {
		c.mu.Unlock()
		board.Close()
		return nil, invalid("board database changed; create a new board connection")
	}
	c.mu.Unlock()
	board.access = c.access
	return board, nil
}

type accessMode int

const (
	accessLegacy accessMode = iota
	accessReadOnly
	accessReadWrite
)

func (a accessMode) requireWrite(identity string, action string) error {
	if a != accessReadOnly {
		return nil
	}
	return &PermissionDenied{
		Message:  "This board connection is read-only.",
		Action:   permissionStringPointer(action),
		Resource: permissionStringPointer("board:" + identity),
	}
}

func (a accessMode) boardPermissions() *BoardPermissions {
	switch a {
	case accessReadOnly:
		return &BoardPermissions{}
	case accessReadWrite:
		return &BoardPermissions{CreateChannel: true}
	default:
		return nil
	}
}

func (a accessMode) channelPermissions() *ChannelPermissions {
	switch a {
	case accessReadOnly:
		return &ChannelPermissions{Read: true}
	case accessReadWrite:
		return &ChannelPermissions{Read: true, Post: true, Reply: true}
	default:
		return nil
	}
}

func permissionStringPointer(value string) *string {
	return &value
}
