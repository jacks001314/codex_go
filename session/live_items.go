package session

import (
	"strings"
	"sync"
	"time"
)

// LiveThreadItems tracks the conversation items a running turn has completed
// but has not committed to the durable thread record yet. Rust records items
// into the live thread store as each response item and tool result completes,
// so forking an active thread sees the same transcript the running session
// sees. Go's embedded exec runner persists a turn at its boundaries, so this
// overlay supplies the in-flight suffix to fork readers until the turn commits.
type LiveThreadItems struct {
	storeRoot string
	threadID  ThreadID
	turnID    string
	mu        sync.Mutex
	items     []Item
	closed    bool
}

var liveThreadItemsRegistry = struct {
	sync.RWMutex
	byKey map[string]*LiveThreadItems
}{byKey: map[string]*LiveThreadItems{}}

func liveThreadItemsKey(store *Store, threadID ThreadID) string {
	if store == nil || strings.TrimSpace(string(threadID)) == "" {
		return ""
	}
	return storeRootKey(store.Root()) + "\x00" + string(threadID)
}

// OpenLiveThreadItems starts a live-item overlay for one thread turn. A
// previous overlay for the same thread is closed because a thread runs at most
// one turn at a time.
func OpenLiveThreadItems(store *Store, threadID ThreadID, turnID string) *LiveThreadItems {
	key := liveThreadItemsKey(store, threadID)
	if key == "" {
		return nil
	}
	live := &LiveThreadItems{
		storeRoot: storeRootKey(store.Root()),
		threadID:  threadID,
		turnID:    strings.TrimSpace(turnID),
	}
	liveThreadItemsRegistry.Lock()
	previous := liveThreadItemsRegistry.byKey[key]
	liveThreadItemsRegistry.byKey[key] = live
	liveThreadItemsRegistry.Unlock()
	if previous != nil && previous != live {
		previous.Close()
	}
	return live
}

// LiveItems returns the process-owned in-flight overlay for threadID under
// this store root, or nil when no turn is running.
func (s *Store) LiveItems(threadID ThreadID) *LiveThreadItems {
	key := liveThreadItemsKey(s, threadID)
	if key == "" {
		return nil
	}
	liveThreadItemsRegistry.RLock()
	live := liveThreadItemsRegistry.byKey[key]
	liveThreadItemsRegistry.RUnlock()
	return live
}

// Append adds completed items to the overlay.
func (l *LiveThreadItems) Append(items ...Item) {
	if l == nil || len(items) == 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return
	}
	items = MissingItems(l.items, items)
	if len(items) == 0 {
		return
	}
	items = cloneItems(items)
	now := time.Now().UTC()
	for i := range items {
		if items[i].CreatedAt.IsZero() {
			items[i].CreatedAt = now
		}
	}
	l.items = append(l.items, items...)
}

// Snapshot returns a copy of the completed items in the overlay.
func (l *LiveThreadItems) Snapshot() []Item {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	return cloneItems(l.items)
}

// Rebase runs save while holding the overlay lock and then drops every item it
// had accumulated. Mid-turn compaction replaces the durable history, so the
// pre-compaction suffix must not reappear after the save commits. Holding the
// lock keeps a concurrent fork from observing the old suffix with the new
// baseline.
func (l *LiveThreadItems) Rebase(save func() error) error {
	if l == nil {
		if save != nil {
			return save()
		}
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if save != nil {
		if err := save(); err != nil {
			return err
		}
	}
	l.items = nil
	return nil
}

// Close removes the overlay and discards its items.
func (l *LiveThreadItems) Close() {
	if l == nil {
		return
	}
	key := storeRootKey(l.storeRoot) + "\x00" + string(l.threadID)
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return
	}
	l.closed = true
	l.items = nil
	l.mu.Unlock()
	liveThreadItemsRegistry.Lock()
	if liveThreadItemsRegistry.byKey[key] == l {
		delete(liveThreadItemsRegistry.byKey, key)
	}
	liveThreadItemsRegistry.Unlock()
}

// MissingItems returns the candidates that are not already present in
// existing. Tool outputs compare by call id because their server-style item id
// is allocated independently on each projection.
func MissingItems(existing []Item, candidates []Item) []Item {
	if len(candidates) == 0 {
		return nil
	}
	seen := make(map[string]int, len(existing))
	for i := range existing {
		seen[itemMergeIdentity(&existing[i])]++
	}
	out := make([]Item, 0, len(candidates))
	for i := range candidates {
		key := itemMergeIdentity(&candidates[i])
		if seen[key] > 0 {
			seen[key]--
			continue
		}
		out = append(out, candidates[i])
	}
	return out
}

func itemMergeIdentity(item *Item) string {
	if item == nil {
		return ""
	}
	turnID := firstNonEmpty(
		stringValue(item.Metadata, "turnId"),
		stringValue(item.Metadata, "turn_id"),
		stringValue(item.Data, "turnId"),
		stringValue(item.Data, "turn_id"),
	)
	switch item.Type {
	case "tool_output", "function_call_output", "custom_tool_call_output", "tool_search_output":
		callID := firstNonEmpty(strings.TrimSpace(item.CallID), stringValue(item.Data, "call_id"))
		if callID != "" {
			return strings.Join([]string{"tool_output", callID}, "\x00")
		}
	case "function_call", "custom_tool_call", "tool_search_call", "local_shell_call":
		callID := firstNonEmpty(strings.TrimSpace(item.CallID), stringValue(item.Data, "call_id"))
		if callID != "" {
			return strings.Join([]string{item.Type, "call", callID}, "\x00")
		}
	}
	if id := strings.TrimSpace(item.ID); id != "" {
		return strings.Join([]string{id, item.Type, item.Role, turnID, item.Text}, "\x00")
	}
	return strings.Join([]string{item.Type, item.Role, turnID, item.Text, item.ResponseID}, "\x00")
}
