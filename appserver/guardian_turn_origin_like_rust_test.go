package appserver

import (
	"context"
	"testing"

	"codex_go/model"
	"codex_go/state"
	"codex_go/telemetry"
)

func guardianTriggerPtr(value string) *string { return &value }

// Mirrors Rust #62225's classification: only a bounded set of automation
// triggers is labelled, missing or empty triggers are unknown, and everything
// else collapses to other so client-supplied strings never become labels.
func TestGuardianTurnOriginTagLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		trigger *string
		want    string
	}{
		{name: "missing", trigger: nil, want: "unknown"},
		{name: "empty", trigger: guardianTriggerPtr(""), want: "unknown"},
		{name: "automation", trigger: guardianTriggerPtr("automation"), want: "automation"},
		{name: "mcp_event", trigger: guardianTriggerPtr("mcp_event"), want: "automation"},
		{name: "cron_scheduled", trigger: guardianTriggerPtr("automation_cron_scheduled"), want: "automation"},
		{name: "cron_run_now", trigger: guardianTriggerPtr("automation_cron_run_now"), want: "automation"},
		{name: "heartbeat_scheduled", trigger: guardianTriggerPtr("automation_heartbeat_scheduled"), want: "automation"},
		{name: "heartbeat_run_now", trigger: guardianTriggerPtr("automation_heartbeat_run_now"), want: "automation"},
		{name: "composer", trigger: guardianTriggerPtr("composer"), want: "other"},
		{name: "client_supplied", trigger: guardianTriggerPtr("custom-value"), want: "other"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := guardianTurnOriginTag(testCase.trigger); got != testCase.want {
				t.Fatalf("guardianTurnOriginTag(%v) = %q, want %q", testCase.trigger, got, testCase.want)
			}
		})
	}
}

// The counter carries the reviewed turn's origin, so an automation-triggered
// review and a later user turn on the same thread are counted separately for
// both verdicts.
func TestGuardianReviewCountTagsTurnOriginLikeRust(t *testing.T) {
	metrics := state.NewTaskMetrics()
	triggers := map[string]*string{
		"turn-automation": guardianTriggerPtr("automation_cron_scheduled"),
		"turn-composer":   guardianTriggerPtr("composer"),
		"turn-unknown":    nil,
	}
	// The first review (the automation turn) is denied and the later user turns
	// are approved, so both verdicts carry the origin tag.
	calls := 0
	reviewer := &modelGuardianReviewer{
		agent: guardianAgentFunc(func(context.Context, *model.AgentRequest) (*model.AgentResponse, error) {
			calls++
			if calls == 1 {
				return &model.AgentResponse{Message: `{"riskLevel":"high","userAuthorization":"low","outcome":"deny","rationale":"denied"}`}, nil
			}
			return &model.AgentResponse{Message: guardianDecisionDurationAssessment}, nil
		}),
		store:   state.NewReviewStore(),
		metrics: metrics,
		turnTrigger: func(_ string, turnID string) *string {
			return triggers[turnID]
		},
	}
	for _, turn := range []string{"turn-automation", "turn-composer", "turn-unknown"} {
		if _, _, err := reviewer.Review(context.Background(), "thread-origin", turn, "call-"+turn,
			state.Action{Type: "command", Command: "ls", CWD: t.TempDir()}); err != nil {
			t.Fatalf("Review(%s) error = %v", turn, err)
		}
	}

	records := guardianMetricRecords(metrics, telemetry.GuardianReviewCountMetric)
	if len(records) != 3 {
		t.Fatalf("review counters = %d, want one per turn", len(records))
	}
	seen := map[string]int{}
	for _, record := range records {
		seen[record.Tags["turn_origin"]]++
	}
	want := map[string]int{"automation": 1, "other": 1, "unknown": 1}
	for origin, count := range want {
		if seen[origin] != count {
			t.Fatalf("turn_origin counts = %v, want %v", seen, want)
		}
	}
}
