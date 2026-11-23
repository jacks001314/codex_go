package appserver

import (
	"strings"

	"codex_go/model"
)

// Rust #62883 (`619d36f810`): user feedback carries the `cot_removed` tag from
// the latest completed sampling request on the live thread. The value is
// runtime-only state (Rust SessionState::last_sampling_cot_removed): it is
// cleared when thread history is replaced and a resumed session starts without
// one.

// recordSamplingCOTRemoved stores the report of one completed sampling request.
// A response without a report clears the stored value, matching Rust's
// `reasoning_removals.map(|counts| !counts.is_empty())`.
func (r *RuntimeRouter) recordSamplingCOTRemoved(threadID string, removals *model.ReasoningRemovals) {
	if r == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	r.samplingCOTMu.Lock()
	defer r.samplingCOTMu.Unlock()
	if removals == nil {
		delete(r.samplingCOTRemoved, threadID)
		return
	}
	if r.samplingCOTRemoved == nil {
		r.samplingCOTRemoved = make(map[string]bool)
	}
	r.samplingCOTRemoved[threadID] = len(removals.Counts) > 0
}

// lastSamplingCOTRemoved reports the stored status for a live thread. The
// second result is false when the thread has no completed report.
func (r *RuntimeRouter) lastSamplingCOTRemoved(threadID string) (bool, bool) {
	if r == nil {
		return false, false
	}
	r.samplingCOTMu.Lock()
	defer r.samplingCOTMu.Unlock()
	removed, ok := r.samplingCOTRemoved[strings.TrimSpace(threadID)]
	return removed, ok
}

// clearSamplingCOTRemoved drops the stored status, as Rust's
// SessionState::replace_history does when the thread history is replaced.
func (r *RuntimeRouter) clearSamplingCOTRemoved(threadID string) {
	if r == nil {
		return
	}
	r.samplingCOTMu.Lock()
	defer r.samplingCOTMu.Unlock()
	delete(r.samplingCOTRemoved, strings.TrimSpace(threadID))
}
