package appserver

import (
	"encoding/json"
	"sort"
	"time"

	"codex_go/compact"
	"codex_go/session"
)

// retainedItemsForFreshChild mirrors Rust `retained_items_for_fresh_child`
// (core/src/agent/history.rs, #61344): a child that starts with New history
// inherits its parent's explicitly retained items under the shared retention
// budget, so a fresh spawn still carries instructions the client asked to keep
// across context windows. Checkpoints stay with the parent, while the retention
// and replay evidence the child needs survive in the rewritten metadata.
func retainedItemsForFreshChild(parent *session.Record, now time.Time) []session.Item {
	if parent == nil || len(parent.Items) == 0 {
		return nil
	}
	positions := compact.SelectAlwaysRetainedPositions(
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
