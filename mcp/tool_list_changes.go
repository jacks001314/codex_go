package mcp

import (
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// mcpToolListChangedMethod is the server notification that invalidates a
// connection's tool catalog (Rust #54656).
const mcpToolListChangedMethod = "notifications/tools/list_changed"

const (
	// mcpToolListNotificationRefreshCooldown is how long a failed or churning
	// notification refresh keeps serving the previously published catalog
	// (Rust NOTIFICATION_REFRESH_COOLDOWN).
	mcpToolListNotificationRefreshCooldown = 5 * time.Second
	// mcpToolListNotificationRefreshAttempts bounds how often a single refresh
	// re-lists while notifications keep arriving across its pages. Rust retries
	// once, so the second unstable listing ends the refresh.
	mcpToolListNotificationRefreshAttempts = 2
)

// isMCPToolListChangedNotification reports whether a server notification makes
// this connection's tool catalog stale.
func isMCPToolListChangedNotification(method string) bool {
	return strings.TrimSpace(method) == mcpToolListChangedMethod
}

// recordMCPToolListChangedNotification counts one server notification in
// transport order when it invalidates the connection's tool catalog, so a
// catalog captured before it is never reused (Rust #54656).
func recordMCPToolListChangedNotification(revision *mcpToolListRevision, method string) {
	if revision == nil || !isMCPToolListChangedNotification(method) {
		return
	}
	revision.recordToolListChanged()
}

// mcpToolListRevision records catalog invalidations in transport order: the read
// path counts a notification before a later response can be delivered, so a
// catalog captured from that connection can be recognized as stale
// (Rust ToolListRevision).
type mcpToolListRevision struct {
	value atomic.Uint64
}

func (r *mcpToolListRevision) recordToolListChanged() {
	if r == nil {
		return
	}
	r.value.Add(1)
}

func (r *mcpToolListRevision) current() uint64 {
	if r == nil {
		return 0
	}
	return r.value.Load()
}

// mcpToolListRefreshDecision is what a status read must do about one server's
// outstanding tools/list_changed notifications.
type mcpToolListRefreshDecision int

const (
	// mcpToolListNoChange: nothing invalidated the connection, so the read lists
	// the server exactly as a read without the feature does.
	mcpToolListNoChange mcpToolListRefreshDecision = iota
	// mcpToolListInCooldown: a failed or churning refresh is still cooling down,
	// so the catalog published before the notification answers the read.
	mcpToolListInCooldown
	// mcpToolListRefresh: a notification invalidated the published catalog, so
	// the server must be listed again before its catalog is published.
	mcpToolListRefresh
)

// mcpToolListRefreshState is one server's notification-refresh bookkeeping: the
// invalidation revision its published catalog was listed at, and the retry
// cooldown that keeps a failed or churning refresh from hammering the
// connection (Rust ClientToolCatalog::refresh_lock).
type mcpToolListRefreshState struct {
	mu         sync.Mutex
	listed     uint64
	retryAfter time.Time
}

// decisionLocked reports what a status read must do for this server. The caller
// holds state.mu.
func (state *mcpToolListRefreshState) decisionLocked(revision uint64, now time.Time) mcpToolListRefreshDecision {
	if state.listed == revision {
		// No notification is outstanding, so nothing has to be refreshed and a
		// cooldown left over from an older revision cannot apply.
		return mcpToolListNoChange
	}
	if !state.retryAfter.IsZero() && now.Before(state.retryAfter) {
		// The refresh failed or kept churning, so the catalog published before
		// the notification keeps answering until the cooldown expires
		// (Rust refresh_after_notifications).
		return mcpToolListInCooldown
	}
	return mcpToolListRefresh
}

// publishedLocked marks the catalog listed at revision as current and clears any
// cooldown. The caller holds state.mu.
func (state *mcpToolListRefreshState) publishedLocked(revision uint64) {
	state.listed = revision
	state.retryAfter = time.Time{}
}

// cooldownLocked starts the retry cooldown after a failed or churning refresh.
// The caller holds state.mu.
func (state *mcpToolListRefreshState) cooldownLocked(now time.Time) {
	state.retryAfter = now.Add(mcpToolListNotificationRefreshCooldown)
}
