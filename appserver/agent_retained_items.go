package appserver

import (
	"encoding/json"
	"sort"
	"time"

	"codex_go/compact"
	"codex_go/session"
)

// retainedItemsForFreshChild mirrors Rust `retained_items_for_fresh_child`
// (core/src/agent/history.rs, #61344/#62654): a child that starts with New
// history inherits its parent's items the client kept for children under the
// shared retention budget, so a fresh spawn still carries instructions the client
// asked to hand down. Thread-only retention stays with the parent, checkpoints
// stay with the parent, and the retention and replay evidence the child needs
// survives in the rewritten metadata.
func retainedItemsForFreshChild(parent *session.Record, now time.Time) []session.Item {
	if parent == nil || len(parent.Items) == 0 {
		return nil
	}
	positions := compact.SelectChildRetainedPositions(
		compactItemsFromSessionItems(parent.Items),
		compact.RemoteRetainedMessageTokenBudget,
		nil,
	)
	if len(positions) == 0 {
		return nil
	}
	ordered := make([]int, 0, len(positions))
	for position := range positions {
		if position >= 0 && position < len(parent.Items) {
			ordered = append(ordered, position)
		}
	}
	sort.Ints(ordered)
	items := make([]session.Item, 0, len(ordered))
	for _, position := range ordered {
		item := parent.Items[position]
		// Apply the selected value: a retained message may have been truncated
		// to fit the shared retention budget.
		applySelectedRetainedItem(&item, positions[position])
		metadata := map[string]any{}
		if raw := harnessMetadataRawFromItem(&item); len(raw) > 0 {
			_ = json.Unmarshal(raw, &metadata)
		}
		rewritten := map[string]any{"inherited_user_message": true}
		for _, key := range []string{
			compact.RetainAcrossWindowsMetadataKey,
			compact.RetainInChildrenMetadataKey,
			"retained_source",
			"fallback_token_limit_override",
			"image_replay_sources",
		} {
			if value, ok := metadata[key]; ok {
				rewritten[key] = value
			}
		}
		encoded, err := json.Marshal(rewritten)
		if err != nil {
			continue
		}
		if item.Data == nil {
			item.Data = map[string]any{}
		} else {
			item.Data = cloneAnyMapForRouter(item.Data)
		}
		item.Data[harnessMetadataKey] = json.RawMessage(encoded)
		// Rust records inherited items in the child's rollout at spawn time.
		item.CreatedAt = now
		items = append(items, item)
	}
	return items
}

// excludeChildExcludedItems drops the items a client kept for this thread only.
// A full-history fork copies the parent's items, but `alwaysInThread` must not
// reach the spawned agent, so the fork scrubs the explicit exclusion while every
// other item keeps its ordinary inheritance rule (Rust #62654
// `keep_forked_rollout_item` / `sanitize_forked_rollout_item`).
func excludeChildExcludedItems(items []session.Item) []session.Item {
	kept := items[:0]
	for index := range items {
		if inherit, explicit := sessionItemChildRetention(&items[index]); explicit && !inherit {
			continue
		}
		kept = append(kept, items[index])
	}
	return kept
}

// applySelectedRetainedItem copies the model-visible payload the retention
// selector returned onto the persisted session item. The original item keeps
// host-only fields such as its metadata; the text, content, data, and raw
// payload follow the selection so a shortened message is also shortened for
// model history replay.
func applySelectedRetainedItem(item *session.Item, selected compact.Item) {
	if item == nil {
		return
	}
	item.Text = compact.ItemText(&selected)
	item.Data = cloneAnyMapForRouter(selected.Data)
	item.Raw = append(json.RawMessage(nil), selected.Raw...)
	if len(selected.Content) == 0 {
		item.Content = nil
		return
	}
	parts := make([]session.ContentPart, 0, len(selected.Content))
	for _, part := range selected.Content {
		parts = append(parts, session.ContentPart{
			Type:     part.Type,
			Text:     part.Text,
			ImageURL: part.ImageURL,
			FileID:   part.FileID,
			AudioURL: part.AudioURL,
			Detail:   part.Detail,
		})
	}
	item.Content = parts
}
