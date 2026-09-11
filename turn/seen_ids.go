package turn

import (
	"hash/fnv"
	"strings"

	"codex_go/model"
)

// seen_ids.go mirrors Rust's
// codex-rs/core/src/tools/executed_tool_calls/seen_ids.rs (#44472).
//
// Observed call and runtime cell IDs are tracked with bounded memory. Bits are
// never cleared: collisions can withhold a completeness proof, but cannot make
// an observed ID appear fresh again.
const (
	seenIDFilterWords       = 8 * 1024
	seenIDMaxHistoryEntries = 100_000
	seenIDMaxHistoryIDBytes = 4 * 1024 * 1024
	// IDs are untrusted strings. A longer ID cannot prove freshness without
	// unbounded hashing.
	seenIDMaxIDBytes = 1024
)

// seenIDs is a bounded, append-only bloom filter over IDs observed by the
// executed-tool-call recorder. The zero value is not usable; construct it with
// newSeenIDs or unobservedHistorySeenIDs.
type seenIDs struct {
	bits []uint64
	// historyIDsIndexed is false when recording started after session
	// initialization or when history exceeded the bounded indexing budgets, so
	// earlier IDs are unknown.
	historyIDsIndexed bool
}

func newSeenIDs() *seenIDs {
	return &seenIDs{bits: make([]uint64, seenIDFilterWords), historyIDsIndexed: true}
}

// unobservedHistorySeenIDs mirrors Rust's SeenIds::unobserved_history: recording
// started after session initialization, so earlier IDs are unknown.
func unobservedHistorySeenIDs() *seenIDs {
	tracker := newSeenIDs()
	tracker.historyIDsIndexed = false
	return tracker
}

func (s *seenIDs) historyIndexed() bool {
	return s != nil && s.historyIDsIndexed
}

// observeCallID reports whether id is fresh relative to IDs observed so far.
// IDs longer than the bound are never fresh.
func (s *seenIDs) observeCallID(id string) bool {
	return s.observe(0, id)
}

// observeRuntimeCellID tracks runtime handles, which are only meaningful within
// this recorder's lifetime.
func (s *seenIDs) observeRuntimeCellID(id string) bool {
	return s.observe(1, id)
}

func (s *seenIDs) observe(namespace uint8, id string) bool {
	if s == nil || len(s.bits) == 0 || len(id) > seenIDMaxIDBytes {
		return false
	}
	hasher := fnv.New64a()
	_, _ = hasher.Write([]byte{namespace})
	_, _ = hasher.Write([]byte(id))
	hash := hasher.Sum64()
	first := hash
	step := (hash >> 32) | 1
	total := uint64(len(s.bits) * 64)
	fresh := false
	for probe := uint64(0); probe < 4; probe++ {
		bit := (first + probe*step) % total
		word := &s.bits[bit/64]
		mask := uint64(1) << (bit % 64)
		if *word&mask == 0 {
			fresh = true
		}
		*word |= mask
	}
	return fresh
}

// observeHistory indexes the call IDs carried by already-supplied history so a
// later cell cannot claim a historical ID as fresh. It mirrors Rust's
// SeenIds::from_history bounds: exhausting the per-item entry budget or the
// total ID-byte budget marks the history as only partially indexed, which
// withholds completeness.
func (s *seenIDs) observeHistory(items []any) {
	if s == nil {
		return
	}
	entriesLeft := seenIDMaxHistoryEntries
	idBytesLeft := seenIDMaxHistoryIDBytes
	for _, item := range items {
		if entriesLeft == 0 {
			s.historyIDsIndexed = false
			return
		}
		entriesLeft--
		for _, id := range historyObservedIDs(item) {
			if len(id) > seenIDMaxIDBytes || len(id) > idBytesLeft {
				s.historyIDsIndexed = false
				return
			}
			idBytesLeft -= len(id)
			s.observeCallID(id)
		}
	}
}

// historyObservedIDs extracts the input/output call IDs and originating Code
// Mode cell IDs that the recorder indexes from supplied history. Unsupported
// item shapes contribute nothing, matching Rust's optional accessors.
func historyObservedIDs(value any) []string {
	var ids []string
	switch item := value.(type) {
	case *ToolResponseItem:
		if item == nil {
			return nil
		}
		if callID := trimmedCallID(item.Type, item.CallID); callID != "" {
			ids = append(ids, callID)
		}
		if cellID := strings.TrimSpace(item.cellID); cellID != "" {
			ids = append(ids, cellID)
		}
	case *model.AgentItem:
		if item == nil {
			return nil
		}
		if callID := trimmedCallID(item.Type, item.CallID); callID != "" {
			ids = append(ids, callID)
		}
		if cellID := item.ExecutedToolCallCellID(); cellID != "" {
			ids = append(ids, cellID)
		}
	case *trustedExecutedToolCallMapItem:
		if item == nil {
			return nil
		}
		return historyObservedIDs(item.value)
	case map[string]any:
		if callID := trimmedCallID(mapString(item, "type"), mapString(item, "call_id")); callID != "" {
			ids = append(ids, callID)
		}
		if metadata, ok := item["internal_chat_message_metadata_passthrough"].(map[string]any); ok {
			if cellID := strings.TrimSpace(mapString(metadata, "cell_id")); cellID != "" {
				ids = append(ids, cellID)
			}
		}
	}
	return ids
}

// trimmedCallID returns the item's call ID only for call and output shapes.
func trimmedCallID(itemType string, callID string) string {
	itemType = strings.TrimSpace(itemType)
	var known bool
	switch itemType {
	case "function_call", "custom_tool_call", "tool_search_call", "local_shell_call",
		"function_call_output", "custom_tool_call_output", "tool_search_output":
		known = true
	}
	if !known {
		return ""
	}
	return strings.TrimSpace(callID)
}

func mapString(value map[string]any, key string) string {
	item, _ := value[key].(string)
	return item
}
