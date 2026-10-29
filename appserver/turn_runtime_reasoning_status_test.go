package appserver

import "testing"

func TestResponsesStreamReasoningSummaryIsConsumedOnce(t *testing.T) {
	state := newResponsesStreamNotificationState(false, "turn-1")
	state.reasoningSummaryByItem["reasoning-1"] = map[int]string{0: " first "}
	if got := state.takeReasoningSummary("reasoning-1"); got != "first" {
		t.Fatalf("summary = %q", got)
	}
	if got := state.takeReasoningSummary("reasoning-1"); got != "" {
		t.Fatalf("summary was retained: %q", got)
	}
	state.reasoningSummaryByItem["reasoning-2"] = map[int]string{0: "  "}
	if got := state.takeReasoningSummary("reasoning-2"); got != "" {
		t.Fatalf("empty summary = %q", got)
	}
}

// Rust #47377 relays the last non-empty entry of a completed reasoning item's
// public `summary_text`, so trailing empty parts and earlier parts are ignored.
func TestResponsesStreamReasoningSummaryUsesLastNonEmptyPartLikeRust(t *testing.T) {
	state := newResponsesStreamNotificationState(false, "turn-1")
	state.reasoningSummaryByItem["reasoning-1"] = map[int]string{0: "first", 1: "  ", 2: " third "}
	if got := state.takeReasoningSummary("reasoning-1"); got != "third" {
		t.Fatalf("summary = %q, want third", got)
	}
	state.reasoningSummaryByItem["reasoning-2"] = map[int]string{1: "one", 3: "three", 5: " \t "}
	if got := state.takeReasoningSummary("reasoning-2"); got != "three" {
		t.Fatalf("sparse summary = %q, want three", got)
	}
	if got := state.takeReasoningSummary("unknown"); got != "" {
		t.Fatalf("unknown item summary = %q", got)
	}
}

// Summary deltas accumulate per summary part index; parts never bleed into one
// another, and raw reasoning text is not part of the status relay.
func TestResponsesStreamReasoningSummaryPartsAccumulateIndependentlyLikeRust(t *testing.T) {
	state := newResponsesStreamNotificationState(false, "turn-1")
	state.recordReasoningSummaryDelta("reasoning-1", 0, "first ")
	state.recordReasoningSummaryDelta("reasoning-1", 1, "second ")
	state.recordReasoningSummaryDelta("reasoning-1", 0, "part")
	state.recordReasoningSummaryDelta("reasoning-1", 1, "part")
	if got := state.reasoningSummaryByItem["reasoning-1"][0]; got != "first part" {
		t.Fatalf("part 0 = %q", got)
	}
	if got := state.reasoningSummaryByItem["reasoning-1"][1]; got != "second part" {
		t.Fatalf("part 1 = %q", got)
	}
	if got := state.takeReasoningSummary("reasoning-1"); got != "second part" {
		t.Fatalf("summary = %q, want second part", got)
	}
	state.recordReasoningSummaryDelta("reasoning-1", 0, "")
	if _, ok := state.reasoningSummaryByItem["reasoning-1"]; ok {
		t.Fatalf("empty delta created a summary entry")
	}
}
