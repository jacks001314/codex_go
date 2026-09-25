package turn

import (
	"fmt"
	"strings"
	"sync"

	"codex_go/tool"
)

type SteerMailbox struct {
	mu       sync.Mutex
	items    map[string][]any
	metadata map[string]map[string]string
	// changed is closed and replaced on every enqueue that stores input, so
	// watchers wake without polling (Rust InputQueue::activity_tx).
	changed chan struct{}
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
	if m.changed != nil {
		close(m.changed)
	}
	m.changed = make(chan struct{})
	return nil
}

// WatchUserInput mirrors Rust InputQueue::watch_user_input (#48135): it
// subscribes before the first check so an arrival between the check and the wait
// cannot be missed, then cancels the signal once a queued user message is
// waiting for the turn. Other turn inputs (agent mail, message-board
// notifications) are not instant-interrupt triggers, matching Rust's
// `TurnInputQueue::has_user_input`. The returned function releases the watcher.
func (m *SteerMailbox) WatchUserInput(threadID string, turnID string, signal *tool.YieldSignal) func() {
	if m == nil || signal == nil {
		return func() {}
	}
	key := steerMailboxKey(threadID, turnID)
	stopped := make(chan struct{})
	released := make(chan struct{})

	m.mu.Lock()
	if m.changed == nil {
		m.changed = make(chan struct{})
	}
	changed := m.changed
	pending := steerItemsHaveUserInput(m.items[key])
	m.mu.Unlock()
	if pending {
		signal.Cancel()
		return func() {}
	}

	go func() {
		defer close(released)
		for {
			select {
			case <-changed:
				m.mu.Lock()
				pending := steerItemsHaveUserInput(m.items[key])
				changed = m.changed
				m.mu.Unlock()
				if pending {
					signal.Cancel()
					return
				}
			case <-stopped:
				return
			}
		}
	}()
	return func() {
		close(stopped)
		<-released
	}
}

// steerItemsHaveUserInput mirrors Rust TurnInputQueue::has_user_input: only a
// queued user message counts, not the agent mail and notification inputs the
// shared mailbox also carries.
func steerItemsHaveUserInput(items []any) bool {
	for _, item := range items {
		raw, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(fmt.Sprint(raw["type"])), "message") &&
			strings.EqualFold(strings.TrimSpace(fmt.Sprint(raw["role"])), "user") {
			return true
		}
	}
	return false
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
