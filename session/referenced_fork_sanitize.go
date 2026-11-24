package session

import "encoding/json"

// sanitizeReferencedAgentForkItems projects the inherited prefix of a fork that
// references its ancestor's stored history. Rust #62314
// (`sanitize_referenced_agent_fork` / `sanitize_agent_fork_prefix`) keeps that
// prefix as conversation context while keeping the ancestor's user messages and
// approvals out of the child's authorization evidence: every inherited response
// item is marked inherited and loses its acceptance order and captured sender
// snapshot.
//
// Go materializes the prefix inside the thread store, so the boundary is exact:
// `InheritedItems` is the number of leading items stitched from the history
// base. Rust has to rediscover the same boundary from a child-owned settings
// event, a checkpoint catalog snapshot, or a revert cutoff because its rollout
// reader sees one flat list.
//
// The returned record is a read-time projection; Save drops the inherited
// prefix again, so the referenced source history is never rewritten.
func sanitizeReferencedAgentForkItems(record *Record) {
	if record == nil || record.HistoryBase == nil || record.ForkedFromID == "" {
		return
	}
	// A fork whose session is its own thread id copied its history and was marked
	// at spawn time. A referenced fork keeps the root session id (Rust #62314).
	if record.SessionID == string(record.ID) {
		return
	}
	boundary := record.InheritedItems
	if boundary <= 0 || boundary > len(record.Items) {
		return
	}
	for index := 0; index < boundary; index++ {
		markInheritedConversationItem(&record.Items[index])
	}
}

// markInheritedConversationItem rewrites one item's harness sidecar so retained
// evidence treats it as inherited context. `sender_user_messages` and
// `user_input_order` belong to the ancestor's acceptance counter, so they are
// removed rather than nulled (Rust's `metadata.take()` / `user_input_order =
// None`).
func markInheritedConversationItem(item *Item) {
	if item == nil {
		return
	}
	metadata := map[string]any{}
	for key, value := range ItemHarnessMetadata(item) {
		metadata[key] = value
	}
	metadata["inherited_user_message"] = true
	delete(metadata, "user_input_order")
	delete(metadata, "sender_user_messages")
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return
	}
	if item.Data == nil {
		item.Data = map[string]any{}
	}
	item.Data["harness_metadata"] = json.RawMessage(encoded)
}
