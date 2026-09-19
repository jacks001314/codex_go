package turn

import (
	"fmt"
	"strings"
	"sync"
)

type SteerMailbox struct {
	mu       sync.Mutex
	items    map[string][]any
	metadata map[string]map[string]string
}

type SteerEnqueueParams struct {
	ThreadID       string
	TurnID         string
	InputItems     []any
	ClientMetadata map[string]string
}

type SteerDrainParams struct {
	ThreadID string
	TurnID   string
}

type SteerDrainResult struct {
	InputItems     []any
	ClientMetadata map[string]string
}

func NewSteerMailbox() *SteerMailbox {
	return &SteerMailbox{items: map[string][]any{}}
}

func (m *SteerMailbox) Enqueue(params *SteerEnqueueParams) error {
	if m == nil {
		return fmt.Errorf("%w: steer mailbox is nil", ErrInvalidTurnRequest)
	}
	if params == nil || strings.TrimSpace(params.ThreadID) == "" || strings.TrimSpace(params.TurnID) == "" {
		return fmt.Errorf("%w: threadId and turnId are required", ErrInvalidTurnRequest)
	}
	items := compactInputItems(params.InputItems)
	metadata := compactStringMap(params.ClientMetadata)
	if len(items) == 0 && len(metadata) == 0 {
		return nil
	}
	key := steerMailboxKey(params.ThreadID, params.TurnID)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.items == nil {
		m.items = map[string][]any{}
	}
	if len(items) > 0 {
		m.items[key] = append(m.items[key], items...)
	}
	if len(metadata) > 0 {
		if m.metadata == nil {
			m.metadata = map[string]map[string]string{}
		}
		m.metadata[key] = metadata
	}
	return nil
}

func (m *SteerMailbox) Drain(params *SteerDrainParams) []any {
	return m.DrainWithMetadata(params).InputItems
}

// HasPending reports whether queued input is waiting for the turn. Rust's
// post-turn compaction skips while the turn's input queue is non-empty
// (#46541); the steer mailbox is Go's queue for input that arrives mid-turn.
func (m *SteerMailbox) HasPending(threadID string, turnID string) bool {
	if m == nil {
		return false
	}
	key := steerMailboxKey(threadID, turnID)
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.items[key]) > 0 {
		return true
	}
	return len(m.metadata[key]) > 0
}

func (m *SteerMailbox) DrainWithMetadata(params *SteerDrainParams) *SteerDrainResult {
	if m == nil || params == nil || strings.TrimSpace(params.ThreadID) == "" || strings.TrimSpace(params.TurnID) == "" {
		return &SteerDrainResult{}
	}
	key := steerMailboxKey(params.ThreadID, params.TurnID)
	m.mu.Lock()
	defer m.mu.Unlock()
	items := append([]any(nil), m.items[key]...)
	metadata := cloneStringMap(m.metadata[key])
	delete(m.items, key)
	delete(m.metadata, key)
	return &SteerDrainResult{InputItems: items, ClientMetadata: metadata}
}

func (m *SteerMailbox) Clear(params *SteerDrainParams) {
	if m == nil || params == nil || strings.TrimSpace(params.ThreadID) == "" || strings.TrimSpace(params.TurnID) == "" {
		return
	}
	key := steerMailboxKey(params.ThreadID, params.TurnID)
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.items, key)
	delete(m.metadata, key)
}

func compactInputItems(items []any) []any {
	out := make([]any, 0, len(items))
	for i := range items {
		if items[i] != nil {
			out = append(out, items[i])
		}
	}
	return out
}

func steerMailboxKey(threadID string, turnID string) string {
	return strings.TrimSpace(threadID) + "\x00" + strings.TrimSpace(turnID)
}
