package appserver

import "testing"

func TestResponsesStreamReasoningSummaryIsConsumedOnce(t *testing.T) {
	state := newResponsesStreamNotificationState(false, "turn-1")
	state.reasoningSummaryByItem["reasoning-1"] = " first "
	if got := state.takeReasoningSummary("reasoning-1"); got != "first" {
		t.Fatalf("summary = %q", got)
	}
	if got := state.takeReasoningSummary("reasoning-1"); got != "" {
		t.Fatalf("summary was retained: %q", got)
	}
	state.reasoningSummaryByItem["reasoning-2"] = "  "
	if got := state.takeReasoningSummary("reasoning-2"); got != "" {
		t.Fatalf("empty summary = %q", got)
	}
}
