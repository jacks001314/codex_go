package appserver

// Rust parity: codex-rs/core/src/stream_events_utils.rs
// (record_completed_response_item_with_finalized_facts) records stage-1 memory
// output usage whenever a completed assistant message carries a
// <oai-mem-citation> payload. Go persists turn items at completion rather than
// per streamed item, so the equivalent hook runs when the turn's items are
// appended.

import (
	"context"
	"strings"

	"codex_go/eventmap"
	"codex_go/memories"
	"codex_go/session"
)

// sessionItemIsAssistantMessage reports whether an item is a model-authored
// assistant message, which is the only item type Rust scans for memory
// citations (raw_assistant_output_text_from_item).
func sessionItemIsAssistantMessage(item *session.Item) bool {
	if item == nil {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(item.Role), "assistant") {
		return true
	}
	switch strings.TrimSpace(item.Type) {
	case "agent_message", "agentMessage", "assistant_message":
		return true
	default:
		return false
	}
}

// memoryCitationThreadIDs collects the cited source thread IDs from a set of
// completed turn items. It returns the deduplicated IDs plus whether any item
// carried a parseable memory citation (Rust treats a citation with no usable
// thread IDs as "has citation" but records nothing).
func memoryCitationThreadIDs(items []session.Item) ([]string, bool) {
	var threadIDs []string
	seen := map[string]bool{}
	found := false
	for i := range items {
		item := &items[i]
		if !sessionItemIsAssistantMessage(item) || strings.TrimSpace(item.Text) == "" {
			continue
		}
		citation := memories.ParseCitation(eventmap.MemoryCitationBodies(item.Text))
		if citation == nil {
			continue
		}
		found = true
		for _, threadID := range memories.ThreadIDsFromCitation(citation) {
			if threadID == "" || seen[threadID] {
				continue
			}
			seen[threadID] = true
			threadIDs = append(threadIDs, threadID)
		}
	}
	return threadIDs, found
}

// recordMemoryCitationUsage mirrors the memory-citation half of Rust
// record_completed_response_item_with_finalized_facts: when a completed
// assistant message cites stage-1 memory outputs, each cited thread's usage is
// recorded against the selected memory version's store. The version-scoped
// store is only opened once a citation is actually present, so threads that
// never cite memory do not create an isolated v2 database. It reports whether
// any completed item carried a memory citation.
func (r *RuntimeRouter) recordMemoryCitationUsage(threadID string, items []session.Item) bool {
	if r == nil {
		return false
	}
	threadIDs, found := memoryCitationThreadIDs(items)
	if !found || len(threadIDs) == 0 || r.services.StateRuntime == nil {
		return found
	}
	version := "v1"
	if cfg := r.effectiveMCPConfigForThread(strings.TrimSpace(threadID)); cfg != nil {
		version = string(cfg.Memories().MemoryVersion())
	}
	store, err := r.services.StateRuntime.MemoryStoreForVersion(context.Background(), version)
	if err != nil {
		return true
	}
	_, _ = store.RecordStage1OutputUsage(context.Background(), threadIDs)
	return true
}
