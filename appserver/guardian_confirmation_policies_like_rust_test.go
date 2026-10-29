package appserver

import (
	"testing"

	"codex_go/session"
	"codex_go/turn"
)

// Rust #41072 omits openai/confirmation_policies for Guardian review sessions
// (is_basic_session_source). Go carries that decision as the guardian turn
// marker that later suppresses the actor tool metadata.
func TestGuardianTurnStartSuppressesConfirmationPoliciesLikeRust(t *testing.T) {
	for _, tc := range []struct {
		name   string
		params *turn.TurnStartParams
		want   bool
	}{
		{name: "nil params", params: nil, want: false},
		{
			name:   "guardian originator",
			params: &turn.TurnStartParams{Originator: "Guardian"},
			want:   true,
		},
		{
			name: "guardian subagent metadata",
			params: &turn.TurnStartParams{
				ResponsesAPIMetadata: map[string]string{"x-openai-subagent": "guardian"},
			},
			want: true,
		},
		{
			name: "ordinary user turn",
			params: &turn.TurnStartParams{
				Originator:           "user",
				ResponsesAPIMetadata: map[string]string{"x-openai-subagent": "general"},
			},
			want: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := guardianTurnStart(tc.params); got != tc.want {
				t.Fatalf("guardianTurnStart() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestGuardianReviewTurnUsesRecordedSessionSourceLikeRust(t *testing.T) {
	router := &RuntimeRouter{threads: &ThreadManager{
		ephemeral: map[string]*session.Record{
			"guardian-legacy":   {ID: "guardian-legacy", Metadata: session.Metadata{Source: session.GuardianReviewSource}},
			"guardian-internal": {ID: "guardian-internal", Metadata: session.Metadata{Source: "internal_guardian"}},
			"ordinary":          {ID: "ordinary", Metadata: session.Metadata{Source: "cli"}},
		},
	}}
	for _, threadID := range []string{"guardian-legacy", "guardian-internal"} {
		if !router.guardianReviewTurn(threadID, &turn.TurnStartParams{}) {
			t.Fatalf("guardianReviewTurn(%q) = false, want the recorded Guardian source to suppress confirmation policies", threadID)
		}
	}
	if router.guardianReviewTurn("ordinary", &turn.TurnStartParams{}) {
		t.Fatal("guardianReviewTurn(ordinary) = true, want false")
	}
	if router.guardianReviewTurn("missing", &turn.TurnStartParams{}) {
		t.Fatal("guardianReviewTurn(missing) = true, want false")
	}
	if !router.guardianReviewTurn("ordinary", &turn.TurnStartParams{Originator: "guardian"}) {
		t.Fatal("guardianReviewTurn() ignored the turn guardian marker")
	}
}
