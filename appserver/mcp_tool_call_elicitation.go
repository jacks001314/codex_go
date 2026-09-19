package appserver

import (
	"strings"
)

// maxPendingMCPElicitations bounds the queued classifications, mirroring Rust's
// MAX_TOOL_RESPONSE_ENTRIES; the oldest entry is evicted when it is full.
const maxPendingMCPElicitations = 256

// pendingMCPElicitation is one known classification queued before the item it
// belongs to completes (Rust's `pending_mcp_tool_elicitations`).
type pendingMCPElicitation struct {
	threadID        string
	turnID          string
	itemID          string
	elicitationType string
}

// rememberMCPToolCallElicitation queues the classification for one MCP tool call
// before its item completes: an authentication or account-linking block
// (`auth_or_link`) or a denied, aborted or timed-out approval (`approval`). The
// first classification for a call wins, the queue is bounded with oldest-first
// eviction, and ordinary calls never enqueue anything, so their emitted
// classification stays null.
func (r *RuntimeRouter) rememberMCPToolCallElicitation(threadID string, turnID string, itemID string, elicitationType string) {
	if r == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	turnID = strings.TrimSpace(turnID)
	itemID = strings.TrimSpace(itemID)
	elicitationType = strings.TrimSpace(elicitationType)
	if threadID == "" || turnID == "" || itemID == "" || elicitationType == "" {
		return
	}
	key := pendingMCPElicitation{threadID: threadID, turnID: turnID, itemID: itemID}
	r.mcpElicitationsMu.Lock()
	defer r.mcpElicitationsMu.Unlock()
	for index := range r.pendingMCPElicitations {
		if r.pendingMCPElicitations[index].sameKey(key) {
			return
		}
	}
	if len(r.pendingMCPElicitations) >= maxPendingMCPElicitations {
		r.pendingMCPElicitations = r.pendingMCPElicitations[1:]
	}
	key.elicitationType = elicitationType
	r.pendingMCPElicitations = append(r.pendingMCPElicitations, key)
}

// takeMCPToolCallElicitation consumes the queued classification for one MCP tool
// call item, which is what Rust does when the item completes.
func (r *RuntimeRouter) takeMCPToolCallElicitation(threadID string, turnID string, itemID string) *string {
	if r == nil {
		return nil
	}
	key := pendingMCPElicitation{
		threadID: strings.TrimSpace(threadID),
		turnID:   strings.TrimSpace(turnID),
		itemID:   strings.TrimSpace(itemID),
	}
	if key.threadID == "" || key.turnID == "" || key.itemID == "" {
		return nil
	}
	r.mcpElicitationsMu.Lock()
	defer r.mcpElicitationsMu.Unlock()
	for index := range r.pendingMCPElicitations {
		if !r.pendingMCPElicitations[index].sameKey(key) {
			continue
		}
		elicitationType := r.pendingMCPElicitations[index].elicitationType
		r.pendingMCPElicitations = append(r.pendingMCPElicitations[:index], r.pendingMCPElicitations[index+1:]...)
		if elicitationType == "" {
			return nil
		}
		return &elicitationType
	}
	return nil
}

// forgetThreadMCPToolCallElicitations drops a closing thread's queued
// classifications, mirroring Rust's ThreadClosed handling.
func (r *RuntimeRouter) forgetThreadMCPToolCallElicitations(threadID string) {
	if r == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	r.mcpElicitationsMu.Lock()
	defer r.mcpElicitationsMu.Unlock()
	kept := r.pendingMCPElicitations[:0]
	for _, pending := range r.pendingMCPElicitations {
		if pending.threadID != threadID {
			kept = append(kept, pending)
		}
	}
	r.pendingMCPElicitations = kept
}

func (p pendingMCPElicitation) sameKey(other pendingMCPElicitation) bool {
	return p.threadID == other.threadID && p.turnID == other.turnID && p.itemID == other.itemID
}
