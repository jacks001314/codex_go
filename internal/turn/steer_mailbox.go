package turn

import (
	"fmt"
	"strings"
	"sync"
)

type SteerMailbox struct {
	mu    sync.Mutex
	items map[string][]any
}

type SteerEnqueueParams struct {
	ThreadID   string
	TurnID     string
	InputItems []any
}

type SteerDrainParams struct {
	ThreadID string
	TurnID   string
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
	if len(items) == 0 {
		return nil
	}
	key := steerMailboxKey(params.ThreadID, params.TurnID)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.items == nil {
		m.items = map[string][]any{}
	}
	m.items[key] = append(m.items[key], items...)
	return nil
}

func (m *SteerMailbox) Drain(params *SteerDrainParams) []any {
	if m == nil || params == nil || strings.TrimSpace(params.ThreadID) == "" || strings.TrimSpace(params.TurnID) == "" {
		return nil
	}
	key := steerMailboxKey(params.ThreadID, params.TurnID)
	m.mu.Lock()
	defer m.mu.Unlock()
	items := append([]any(nil), m.items[key]...)
	delete(m.items, key)
	return items
}

func (m *SteerMailbox) Clear(params *SteerDrainParams) {
	if m == nil || params == nil || strings.TrimSpace(params.ThreadID) == "" || strings.TrimSpace(params.TurnID) == "" {
		return
	}
	key := steerMailboxKey(params.ThreadID, params.TurnID)
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.items, key)
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
