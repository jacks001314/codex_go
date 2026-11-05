package session

import "encoding/json"

const (
	harnessHistorySequenceKey        = "history_sequence"
	harnessSampledThroughSequenceKey = "sampled_through_sequence"
)

// ItemHarnessMetadata returns a copy-safe view of the harness sidecar stored on
// an item. Harness metadata is persisted with the item but never reaches model
// input (Rust CodexHarnessMetadata).
func ItemHarnessMetadata(item *Item) map[string]any {
	if item == nil {
		return nil
	}
	return harnessMetadata(item.Data)
}

// MergeItemHarnessMetadata adds fields to an item's harness sidecar.
func MergeItemHarnessMetadata(item *Item, fields map[string]any) {
	mergeHistoryHarnessMetadata(item, fields)
}

// ItemHistorySequence returns the thread-local append sequence for an item.
func ItemHistorySequence(item *Item) uint64 {
	return harnessMetadataUint64(ItemHarnessMetadata(item), harnessHistorySequenceKey)
}

// ItemSampledThroughSequence returns the highest input prefix acknowledged by
// a completed regular inference request, as recorded beside the item.
func ItemSampledThroughSequence(item *Item) uint64 {
	return harnessMetadataUint64(ItemHarnessMetadata(item), harnessSampledThroughSequenceKey)
}

// EnsureHistorySequences restores stable append identities for existing legacy
// items and returns the highest sequence. Restored items keep their missing
// sampling checkpoint so compaction cannot mistake them for delivered input.
func EnsureHistorySequences(items []Item) uint64 {
	last, _ := historySequenceBounds(items)
	return assignMissingHistorySequences(items, 0, len(items), last)
}

// EnsureAppendedHistorySequences restores existing identities, then assigns
// identity and the current sampling checkpoint to items starting at
// appendedStart. The checkpoint is captured before the append, matching
// SamplingProgress::record in Rust #59404.
func EnsureAppendedHistorySequences(items []Item, appendedStart int) uint64 {
	last, sampledThrough := historySequenceBounds(items)
	if appendedStart < 0 {
		appendedStart = 0
	}
	if appendedStart > len(items) {
		appendedStart = len(items)
	}
	last = assignMissingHistorySequences(items, 0, appendedStart, last)
	for i := appendedStart; i < len(items); i++ {
		if ItemHistorySequence(&items[i]) == 0 {
			last++
			MergeItemHarnessMetadata(&items[i], map[string]any{harnessHistorySequenceKey: last})
		}
		sequence := ItemSampledThroughSequence(&items[i])
		if sequence == 0 {
			MergeItemHarnessMetadata(&items[i], map[string]any{harnessSampledThroughSequenceKey: sampledThrough})
			continue
		}
		sampledThrough = max(sampledThrough, sequence)
	}
	return last
}

func historySequenceBounds(items []Item) (uint64, uint64) {
	var last uint64
	var sampledThrough uint64
	for i := range items {
		last = max(last, ItemHistorySequence(&items[i]))
		sampledThrough = max(sampledThrough, ItemSampledThroughSequence(&items[i]))
	}
	return max(last, sampledThrough), sampledThrough
}

func assignMissingHistorySequences(items []Item, start int, end int, last uint64) uint64 {
	for i := start; i < end; i++ {
		if ItemHistorySequence(&items[i]) != 0 {
			continue
		}
		last++
		MergeItemHarnessMetadata(&items[i], map[string]any{harnessHistorySequenceKey: last})
	}
	return last
}

// MaxHistorySequence returns the highest append identity in the supplied
// history without mutating it.
func MaxHistorySequence(items []Item) uint64 {
	var last uint64
	for i := range items {
		last = max(last, ItemHistorySequence(&items[i]))
		last = max(last, ItemSampledThroughSequence(&items[i]))
	}
	return last
}

// MaxSampledThroughSequence returns the highest regular-inference checkpoint.
func MaxSampledThroughSequence(items []Item) uint64 {
	var sampled uint64
	for i := range items {
		sampled = max(sampled, ItemSampledThroughSequence(&items[i]))
	}
	return sampled
}

func harnessMetadataUint64(metadata map[string]any, key string) uint64 {
	if metadata == nil {
		return 0
	}
	switch value := metadata[key].(type) {
	case uint64:
		return value
	case int:
		if value > 0 {
			return uint64(value)
		}
	case int64:
		if value > 0 {
			return uint64(value)
		}
	case float64:
		if value > 0 {
			return uint64(value)
		}
	case json.Number:
		if parsed, err := value.Int64(); err == nil && parsed > 0 {
			return uint64(parsed)
		}
	}
	return 0
}
