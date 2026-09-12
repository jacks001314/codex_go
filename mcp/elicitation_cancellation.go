package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
)

// maxMCPElicitationCancellations bounds the per-connection memory of server
// cancellations that arrived before (or while) their elicitation request, so a
// misbehaving server cannot grow the set without bound (Rust #44238).
const maxMCPElicitationCancellations = 1024

// mcpElicitationCancellationMemory remembers server `notifications/cancelled`
// frames for one connection so a form or URL elicitation returns `cancel`
// instead of waiting for a response that will never come. It mirrors Rust's
// ElicitationClientService cancellation map: remembered cancellations are never
// evicted, and once the memory saturates every new elicitation for the
// connection is cancelled.
type mcpElicitationCancellationMemory struct {
	mu           sync.Mutex
	cancelledIDs map[string]struct{}
	saturated    bool
}

// remember records a cancellation for id. It is safe to call before the
// matching request arrives (Rust's early cancellation).
func (m *mcpElicitationCancellationMemory) remember(id json.RawMessage) {
	key := mcpElicitationRequestIDKey(id)
	if key == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.saturated {
		return
	}
	if m.cancelledIDs == nil {
		m.cancelledIDs = map[string]struct{}{}
	}
	if _, ok := m.cancelledIDs[key]; ok {
		return
	}
	if len(m.cancelledIDs) >= maxMCPElicitationCancellations {
		m.saturated = true
		m.cancelledIDs = nil
		return
	}
	m.cancelledIDs[key] = struct{}{}
}

// take reports whether id was already cancelled and forgets it so a reused
// request id starts fresh on the same connection.
func (m *mcpElicitationCancellationMemory) take(id json.RawMessage) bool {
	if m == nil {
		return false
	}
	key := mcpElicitationRequestIDKey(id)
	if key == "" {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.saturated {
		return true
	}
	if _, ok := m.cancelledIDs[key]; ok {
		delete(m.cancelledIDs, key)
		return true
	}
	return false
}

// cancelled reports whether a cancellation for id is currently remembered
// without forgetting it.
func (m *mcpElicitationCancellationMemory) cancelled(id json.RawMessage) bool {
	if m == nil {
		return false
	}
	key := mcpElicitationRequestIDKey(id)
	if key == "" {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.saturated {
		return true
	}
	_, ok := m.cancelledIDs[key]
	return ok
}

// forget drops a remembered cancellation for id.
func (m *mcpElicitationCancellationMemory) forget(id json.RawMessage) {
	if m == nil {
		return
	}
	key := mcpElicitationRequestIDKey(id)
	if key == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.cancelledIDs, key)
}

// reset drops all remembered cancellations. A new connection attempt calls this
// so cancellation state stays scoped to its own connection (Rust #44238).
func (m *mcpElicitationCancellationMemory) reset() {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cancelledIDs = nil
	m.saturated = false
}

func mcpElicitationRequestIDKey(id json.RawMessage) string {
	trimmed := strings.TrimSpace(string(id))
	if trimmed == "" || trimmed == "null" {
		return ""
	}
	return trimmed
}

// mcpCancelledNotificationRequestID extracts the cancelled request id from a
// `notifications/cancelled` params payload.
func mcpCancelledNotificationRequestID(params json.RawMessage) json.RawMessage {
	if len(params) == 0 {
		return nil
	}
	var payload struct {
		RequestID json.RawMessage `json:"requestId"`
	}
	if err := json.Unmarshal(params, &payload); err != nil {
		return nil
	}
	if len(payload.RequestID) == 0 || mcpElicitationRequestIDKey(payload.RequestID) == "" {
		var snakeCase struct {
			RequestID json.RawMessage `json:"request_id"`
		}
		if err := json.Unmarshal(params, &snakeCase); err != nil {
			return nil
		}
		return snakeCase.RequestID
	}
	return payload.RequestID
}

// isMCPElicitationMethod reports whether method is a server-to-client
// elicitation request whose response honors cancellation (Rust #44238).
func isMCPElicitationMethod(method string) bool {
	switch strings.TrimSpace(method) {
	case "elicitation/create", "openai/form":
		return true
	default:
		return false
	}
}

func mcpContextCancelled(ctx context.Context) bool {
	return ctx != nil && ctx.Err() != nil
}
