package agentboard

// Host-selected message-board backend selection.
//
// Rust parity: codex-rs/ext/agent-message-board/src/backend.rs (#58986). The
// host chooses a backend and supplies its capabilities; this package opens the
// selected storage without depending on the app server's runtime types.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"codex_go/state"
)

// BoardBackendKind names where a session's board state lives.
type BoardBackendKind string

const (
	// BoardBackendInMemory shares one board per agent tree for this process.
	BoardBackendInMemory BoardBackendKind = "in_memory"
	// BoardBackendLocal stores the board in the local SQLite database.
	BoardBackendLocal BoardBackendKind = "local"
	// BoardBackendRemote calls a host-served message-board endpoint.
	BoardBackendRemote BoardBackendKind = "remote"
	// BoardBackendConnected attaches an already granted local connection.
	BoardBackendConnected BoardBackendKind = "connected"
)

var (
	// ErrRemoteBoardMissingCredential reports a remote backend with no token.
	ErrRemoteBoardMissingCredential = errors.New("remote message board requires a credential")
	// ErrRemoteBoardCredentialEnv reports an unusable credential environment.
	ErrRemoteBoardCredentialEnv = errors.New("message-board credential environment variable is missing or invalid")
	// ErrBoardBackendUnknown reports a backend kind the host did not select.
	ErrBoardBackendUnknown = errors.New("unsupported message-board backend")
)

// RemoteBoardBackend configures the remote backend. The token may come from an
// environment variable or the configuration; the environment wins when named.
type RemoteBoardBackend struct {
	Endpoint          string
	BearerToken       *string
	BearerTokenEnvVar *string
	HTTP              RemoteBoardHTTPDoer
	Clock             func(caller string) (time.Time, error)
}

// BoardBackend is the host-selected backend for one agent tree. Only the field
// matching Kind is read.
type BoardBackend struct {
	Kind      BoardBackendKind
	InMemory  *InMemoryMessageBoards
	Local     state.SqliteConfig
	Options   LocalBoardOptions
	Connected *LocalBoardConnection
	Remote    *RemoteBoardBackend
}

// OpenBoardBackend opens the selected backend with the host-supplied
// capabilities. Remote configuration errors wrap ErrRemoteBoardMissingCredential
// or ErrRemoteBoardCredentialEnv so the host can report them as invalid
// requests.
func OpenBoardBackend(ctx context.Context, backend BoardBackend, tree string, host Host) (Board, error) {
	switch backend.Kind {
	case BoardBackendInMemory:
		if backend.InMemory == nil {
			return nil, fmt.Errorf("%w: in-memory registry is missing", ErrBoardBackendUnknown)
		}
		return backend.InMemory.Open(tree, host), nil
	case BoardBackendLocal:
		return OpenLocalBoardWithOptions(ctx, backend.Local, backend.Options, tree, host)
	case BoardBackendConnected:
		if backend.Connected == nil {
			return nil, fmt.Errorf("%w: connected board grant is missing", ErrBoardBackendUnknown)
		}
		return backend.Connected.Open(ctx, host)
	case BoardBackendRemote:
		return openRemoteBoardBackend(ctx, backend.Remote, tree, host)
	default:
		return nil, fmt.Errorf("%w: %q", ErrBoardBackendUnknown, backend.Kind)
	}
}

func openRemoteBoardBackend(ctx context.Context, config *RemoteBoardBackend, tree string, host Host) (Board, error) {
	if config == nil {
		return nil, fmt.Errorf("%w: remote configuration is missing", ErrBoardBackendMissingConfig)
	}
	token := ""
	switch {
	case config.BearerTokenEnvVar != nil:
		value, ok := os.LookupEnv(strings.TrimSpace(*config.BearerTokenEnvVar))
		if !ok || strings.TrimSpace(value) == "" {
			return nil, ErrRemoteBoardCredentialEnv
		}
		token = value
	case config.BearerToken != nil:
		token = *config.BearerToken
	default:
		return nil, ErrRemoteBoardMissingCredential
	}
	if err := ValidateRemoteBoardToken(token); err != nil {
		return nil, err
	}
	clock := config.Clock
	if clock == nil && host != nil {
		if ctx == nil {
			ctx = context.Background()
		}
		clock = func(caller string) (time.Time, error) {
			return host.CurrentTime(ctx, caller)
		}
	}
	board, err := NewRemoteBoard(RemoteBoardOptions{
		HTTP:     config.HTTP,
		Endpoint: config.Endpoint,
		Board:    tree,
		Token:    token,
		Clock:    clock,
	})
	if err != nil {
		return nil, err
	}
	return board, nil
}

// ErrBoardBackendMissingConfig reports a selected backend without its config.
var ErrBoardBackendMissingConfig = errors.New("message-board backend configuration is missing")
