package agentboard

// Host capabilities needed by a board without depending on the agent runtime.
//
// Rust parity: codex-rs/ext/agent-message-board/src/host.rs.

import (
	"context"
	"time"

	"codex_go/agent"
)

// NotificationDelivery reports whether a notification was accepted.
type NotificationDelivery string

const (
	NotificationAccepted        NotificationDelivery = "accepted"
	NotificationSkippedInactive NotificationDelivery = "skipped_inactive"
)

// Host supplies tree-scoped membership, clock and notification access.
//
// The host remains authoritative for membership even when runtimes are
// unloaded. Clock reads use the posting agent's configured source.
// Notifications follow the host's queue-only agent-message scheduling and wake
// policy, and may be buffered for an idle recipient. Acceptance does not mean
// that the recipient model has consumed the notification.
type Host interface {
	// AgentPath returns the caller's path in this board's tree.
	AgentPath(ctx context.Context, caller string) (agent.AgentPath, error)
	// ResolveAgent resolves a path in this tree to its thread.
	ResolveAgent(ctx context.Context, path agent.AgentPath) (string, error)
	// CurrentTime returns the posting agent's current time.
	CurrentTime(ctx context.Context, caller string) (time.Time, error)
	// Notify pushes metadata and a bounded preview to a recipient.
	Notify(ctx context.Context, recipient string, post PostPreview) (NotificationDelivery, error)
}
