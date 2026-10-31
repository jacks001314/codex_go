package appserver

import (
	"context"
	"testing"

	"codex_go/model"
	"codex_go/state"
	"codex_go/telemetry"
)

// Mirrors Rust #43462's review-reason routing: required reviews are
// fresh_required, ordinary policy reviews are policy, and a retained serving
// assessment that cannot be reused reports why the synchronous review ran.
func TestGuardianReviewReasonRoutingLikeRust(t *testing.T) {
	current := state.ServingAuthorization{RetainedContextComplete: true, PolicyFingerprint: "model|policy"}
	tests := []struct {
		name           string
		serving        *state.ServingAssessment
		authorization  state.ServingAuthorization
		requiredReview bool
		want           state.GuardianReviewReason
	}{
		{name: "policy", authorization: current, want: state.GuardianReviewReasonPolicy},
		{name: "fresh required", authorization: current, requiredReview: true, want: state.GuardianReviewReasonFreshRequired},
		{
			name:          "elevated risk",
			serving:       &state.ServingAssessment{RequiresReview: true, Authorization: current},
			authorization: current,
			want:          state.GuardianReviewReasonElevatedRisk,
		},
		{
			name:          "authorization changed",
			serving:       &state.ServingAssessment{Authorization: current},
			authorization: state.ServingAuthorization{RetainedContextComplete: true, PolicyFingerprint: "other-model|policy"},
			want:          state.GuardianReviewReasonAuthorizationChanged,
		},
		{
			name:          "incomplete authorization",
			serving:       &state.ServingAssessment{Authorization: state.ServingAuthorization{RetainedContextComplete: false, PolicyFingerprint: "model|policy"}},
			authorization: current,
			want:          state.GuardianReviewReasonPolicy,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			var events []*state.Event
			metrics := state.NewTaskMetrics()
			reviewer := &modelGuardianReviewer{
				agent: guardianAgentFunc(func(context.Context, *model.AgentRequest) (*model.AgentResponse, error) {
					return &model.AgentResponse{Message: `{"riskLevel":"low","userAuthorization":"high","outcome":"allow","rationale":"ok"}`}, nil
				}),
				store:   state.NewReviewStore(),
				metrics: metrics,
				servingAssessment: func(_, _, callID string) *state.ServingAssessment {
					if callID == "call-reason" {
						return testCase.serving
					}
					return nil
				},
				servingAuthorization: func(string, string) state.ServingAuthorization {
					return testCase.authorization
				},
				notify: func(_ string, event *state.Event) {
					events = append(events, event)
				},
			}
			if testCase.requiredReview {
				reviewer.requiredReview = func(string, string) bool { return true }
			}

			decision, _, err := reviewer.Review(context.Background(), "thread-reason", "turn-reason", "call-reason", state.Action{
				Type: "command", Command: "ls", CWD: "/tmp", Source: state.CommandSourceShell,
			})
			if err != nil || decision != state.DecisionApproved {
				t.Fatalf("Review() = %s, %v; want approved", decision, err)
			}
			if len(events) != 2 {
				t.Fatalf("review events = %#v, want started and completed", events)
			}
			for _, event := range events {
				if event.ReviewReason == nil || *event.ReviewReason != testCase.want {
					t.Fatalf("event %s reason = %#v, want %q", event.Status, event.ReviewReason, testCase.want)
				}
			}

			routes := guardianMetricRecords(metrics, telemetry.GuardianReviewRouteMetric)
			if len(routes) != 1 || routes[0].Tags["route"] != "required_sync" || routes[0].Tags["reason"] != string(testCase.want) {
				t.Fatalf("route metric = %#v, want reason %q", routes, testCase.want)
			}
		})
	}
}

// Mirrors the Rust app-server's strict-review notification: it is emitted only
// for an in-progress review with a strict review reason.
func TestGuardianStrictReviewRequiredNotificationLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		reason state.GuardianReviewReason
		status state.Status
		want   bool
	}{
		{name: "elevated risk", reason: state.GuardianReviewReasonElevatedRisk, status: state.StatusInProgress, want: true},
		{name: "authorization changed", reason: state.GuardianReviewReasonAuthorizationChanged, status: state.StatusInProgress, want: true},
		{name: "fresh required", reason: state.GuardianReviewReasonFreshRequired, status: state.StatusInProgress, want: false},
		{name: "policy", reason: state.GuardianReviewReasonPolicy, status: state.StatusInProgress, want: false},
		{name: "completed strict reason", reason: state.GuardianReviewReasonElevatedRisk, status: state.StatusDenied, want: false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			router := NewRuntimeRouter(RuntimeServices{DefaultCWD: t.TempDir()})
			defer router.Close()
			sink := NewNotificationBuffer()
			router.SetNotificationSink(sink)

			reason := testCase.reason
			event := &state.Event{
				ID:           "review-strict",
				TurnID:       "turn-strict",
				StartedAtMS:  1234,
				Status:       testCase.status,
				ReviewReason: &reason,
				Action:       state.Action{Type: "command", Command: "ls", CWD: "/tmp", Source: state.CommandSourceShell},
			}
			router.notifyGuardianReviewEvent("thread-strict", event)

			notifications := sink.List()
			wantNotifications := 1
			if testCase.want {
				wantNotifications = 2
			}
			if len(notifications) != wantNotifications {
				t.Fatalf("notifications = %#v, want %d", notifications, wantNotifications)
			}
			if !testCase.want {
				for _, notification := range notifications {
					if notification.Method == NotificationStrictReviewRequired {
						t.Fatalf("unexpected strict review notification: %#v", notifications)
					}
				}
				return
			}
			if notifications[0].Method != NotificationItemGuardianApprovalReviewStarted || notifications[1].Method != NotificationStrictReviewRequired {
				t.Fatalf("notification order = %#v", notifications)
			}
			payload, ok := notifications[1].Params.(*StrictReviewRequiredNotification)
			if !ok || payload.ThreadID != "thread-strict" || payload.TurnID != "turn-strict" || payload.StartedAtMS != 1234 {
				t.Fatalf("strict review payload = %#v", notifications[1].Params)
			}
		})
	}
}
