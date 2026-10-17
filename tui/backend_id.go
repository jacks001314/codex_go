package tui

import (
	"sync"

	"github.com/google/uuid"
)

// BackendID is the logical identity of the app-server backend that owns a
// thread, independent of any single transport connection and of the native
// thread ID.
//
// Rust parity: codex-rs/tui/src/backend_id.rs (#54681). Reconnecting and
// auxiliary picker connections reuse the launch owner's identity; a different
// backend gets a fresh identity. These process-local IDs never appear on the
// wire, so they are not part of any app-server schema.
type BackendID uuid.UUID

// NewBackendID returns a fresh logical backend identity.
func NewBackendID() BackendID {
	return BackendID(uuid.New())
}

// IsZero reports whether the identity was never initialized.
func (b BackendID) IsZero() bool {
	return uuid.UUID(b) == uuid.Nil
}

func (b BackendID) String() string {
	return uuid.UUID(b).String()
}

var (
	processBackendOnce sync.Once
	processBackendID   BackendID
)

// ProcessBackendID returns the identity of the backend this process talks to.
//
// The embedded/daemon launch owns one backend for the life of the process, so
// every picker and session-start path inside it shares this identity. A TUI
// bound to a remote workspace creates its own identity with NewBackendID and
// threads it through tea.Options and the app handlers instead.
func ProcessBackendID() BackendID {
	processBackendOnce.Do(func() {
		processBackendID = NewBackendID()
	})
	return processBackendID
}
