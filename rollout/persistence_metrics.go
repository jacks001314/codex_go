package rollout

import (
	"encoding/json"

	"codex_go/metrics"
)

// Rust parity: codex-rs/rollout/src/persistence_metrics.rs (#50454).
//
// The durable rollout write path measures the JSON bytes of every persisted
// item before and after the persistence projection and reports the positive
// reduction, so the size saved by truncating oversized fields stays visible in
// telemetry.

const (
	rolloutPersistenceItemBytesMetric    = "codex.rollout.persistence.item_bytes_v2"
	rolloutPersistenceBytesRemovedMetric = "codex.rollout.persistence.bytes_removed"
	// rolloutPersistenceEncodingLabel and rolloutPersistenceSampleRateLabel are
	// the Rust encoding/sample-rate tags.
	rolloutPersistenceEncodingLabel   = "rollout_item_json_v1"
	rolloutPersistenceSampleRateLabel = "0.01"
	// rolloutPersistenceSampleDenominator samples whole threads at 1% (Rust
	// SAMPLE_DENOMINATOR=100 with SAMPLE_RATE_LABEL "0.01").
	rolloutPersistenceSampleDenominator = 100
	// Stage labels align the item histogram with the turn histogram; Rust
	// renamed the former pre_filter/post_filter stages to before_projection /
	// after_projection in the same change.
	rolloutPersistenceStageBeforeProjection = "before_projection"
	rolloutPersistenceStageAfterProjection  = "after_projection"
	// Go's paginated projection truncates oversized fields but never drops an
	// item (rollout/persist_policy.go), so every measured item carries Rust's
	// `kept` decision.
	rolloutPersistenceDecisionKept = "kept"
)

// Rust `is_thread_sampled` folds the thread id with FNV-1a and keeps whole
// threads whose hash lands on the sample bucket.
const (
	rolloutPersistenceHashOffset = 0xcbf29ce484222325
	rolloutPersistenceHashPrime  = 0x100000001b3
)

const (
	rolloutPersistenceKiB = 1024
	rolloutPersistenceMiB = 1024 * rolloutPersistenceKiB
)

// rolloutPersistenceBytesBoundaries mirrors Rust `ROLLOUT_BYTES_BOUNDARIES`:
// explicit 1 KiB - 64 MiB buckets cover the persistence cap while larger items
// stay represented by the overflow bucket and the histogram sum.
var rolloutPersistenceBytesBoundaries = []float64{
	rolloutPersistenceKiB,
	4 * rolloutPersistenceKiB,
	16 * rolloutPersistenceKiB,
	64 * rolloutPersistenceKiB,
	256 * rolloutPersistenceKiB,
	rolloutPersistenceMiB,
	4 * rolloutPersistenceMiB,
	16 * rolloutPersistenceMiB,
	64 * rolloutPersistenceMiB,
}

// rolloutPersistenceBoundedHistogram is the explicit-boundary capability of the
// process-global recorder (Rust `MetricsClient::histogram_with_boundaries`);
// telemetry.MetricsClient implements it.
type rolloutPersistenceBoundedHistogram interface {
	HistogramWithBounds(name string, value int, boundaries []float64, tags map[string]string)
}

// recordRolloutPersistenceHistogram records one bounded observation on the
// process-global recorder, falling back to the plain histogram when the
// installed recorder has no explicit-boundary support. It is nil-safe, like the
// metrics package helpers.
func recordRolloutPersistenceHistogram(name string, value int64, tags map[string]string) {
	recorder := metrics.Global()
	if recorder == nil {
		return
	}
	if bounded, ok := recorder.(rolloutPersistenceBoundedHistogram); ok {
		bounded.HistogramWithBounds(name, saturatingInt(value), rolloutPersistenceBytesBoundaries, cloneRolloutPersistenceTags(tags))
		return
	}
	recorder.Histogram(name, saturatingInt(value), cloneRolloutPersistenceTags(tags))
}

// recordRolloutPersistenceItemBytes mirrors Rust `record_item_bytes`: one
// item_bytes_v2 observation for the given projection stage.
func recordRolloutPersistenceItemBytes(stage string, itemType string, decision string, payloadBytes int64) {
	recordRolloutPersistenceHistogram(rolloutPersistenceItemBytesMetric, payloadBytes, map[string]string{
		"stage":             stage,
		"decision":          decision,
		"rollout_item_type": itemType,
		"encoding":          rolloutPersistenceEncodingLabel,
		"sample_rate":       rolloutPersistenceSampleRateLabel,
	})
}

// recordRolloutPersistenceItemSizes mirrors Rust
// `RolloutPersistenceTelemetry::record_batch`'s per-item byte accounting for a
// single persisted rollout item: the original size (`before_projection`), the
// persisted size (`after_projection`), and the positive reduction
// (`bytes_removed`, with the full original size counted for a dropped item).
// The caller passes the item bytes before and after the persistence projection
// (Rust `persisted_rollout_item`).
func recordRolloutPersistenceItemSizes(threadID string, original json.RawMessage, persisted json.RawMessage) {
	if len(original) == 0 || !rolloutPersistenceThreadSampled(threadID) {
		return
	}
	itemType := rolloutPersistenceItemType(original)
	payloadBytes := int64(len(original))
	persistedBytes := int64(len(persisted))
	recordRolloutPersistenceItemBytes(rolloutPersistenceStageBeforeProjection, itemType, rolloutPersistenceDecisionKept, payloadBytes)
	recordRolloutPersistenceItemBytes(rolloutPersistenceStageAfterProjection, itemType, rolloutPersistenceDecisionKept, persistedBytes)
	if removed := payloadBytes - persistedBytes; removed > 0 {
		recordRolloutPersistenceHistogram(rolloutPersistenceBytesRemovedMetric, removed, map[string]string{
			"decision":          rolloutPersistenceDecisionKept,
			"rollout_item_type": itemType,
			"encoding":          rolloutPersistenceEncodingLabel,
			"sample_rate":       rolloutPersistenceSampleRateLabel,
		})
	}
}

// rolloutPersistenceThreadSampled reports whether a thread is inside the 1%
// sample (Rust `is_thread_sampled`): FNV-1a over the thread id bytes, sampled
// by whole threads so a thread never reports a partial stream of batches.
func rolloutPersistenceThreadSampled(threadID string) bool {
	hash := uint64(rolloutPersistenceHashOffset)
	for index := 0; index < len(threadID); index++ {
		hash = (hash ^ uint64(threadID[index])) * uint64(rolloutPersistenceHashPrime)
	}
	return hash%rolloutPersistenceSampleDenominator == 0
}

// rolloutPersistenceItemType mirrors Rust `rollout_item_type` for the paginated
// ItemCompleted items Go persists: "event.item_completed.<snake_case turn item
// type>". The durable payload is the core TurnItem JSON (rollout/turn_item.go).
func rolloutPersistenceItemType(item json.RawMessage) string {
	var decoded map[string]any
	if err := json.Unmarshal(item, &decoded); err != nil {
		return "event.item_completed.unknown"
	}
	kind := snakeEnum(anyString(decoded, "type"))
	if kind == "" {
		return "event.item_completed.unknown"
	}
	return "event.item_completed." + kind
}

// cloneRolloutPersistenceTags copies the tag map so the caller's literal can be
// discarded after recording.
func cloneRolloutPersistenceTags(tags map[string]string) map[string]string {
	if len(tags) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(tags))
	for key, value := range tags {
		cloned[key] = value
	}
	return cloned
}
