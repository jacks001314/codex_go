package protocol

import (
	"strings"

	"github.com/google/uuid"
)

// ContextWindowID identifies one conversation context window across
// compaction, rollout restoration and request metadata (Rust #59328
// `ContextWindowId`). It serializes as its canonical UUIDv7 string so the wire
// format is unchanged.
type ContextWindowID string

// NewContextWindowID mints a UUIDv7 context window identity.
func NewContextWindowID() ContextWindowID {
	if id, err := uuid.NewV7(); err == nil {
		return ContextWindowID(id.String())
	}
	return ContextWindowID(uuid.NewString())
}

// ParseContextWindowID reads a persisted context window ID, ignoring values
// outside the supported format. Rust's `ContextWindowId::parse` keeps only
// UUIDv7 values.
func ParseContextWindowID(value string) (ContextWindowID, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", false
	}
	parsed, err := uuid.Parse(trimmed)
	if err != nil || parsed.Version() != 7 {
		return "", false
	}
	return ContextWindowID(parsed.String()), true
}

// String returns the canonical string form used on the wire.
func (id ContextWindowID) String() string { return string(id) }
