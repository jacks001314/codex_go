package chatwidget

import "testing"

func TestProtocolNotificationRoutesGuardianShutdownDiffAndDeprecationMatchRust(t *testing.T) {
	started := ClassifyProtocolNotification(ProtocolNotification{
		Kind:       NotificationGuardianReviewStarted,
		GuardianID: "review-1",
		GuardianAction: GuardianAssessmentAction{
			Kind:    GuardianActionCommand,
			Command: "go test ./...",
		},
	}, ReplayNone)
	if started.Route != ProtocolNotificationRouteGuardianReview || started.GuardianAssessment == nil {
		t.Fatalf("started route = %#v", started)
	}
	if started.GuardianAssessment.Status != GuardianAssessmentInProgress || started.GuardianAssessment.ID != "review-1" {
		t.Fatalf("started assessment = %#v", started.GuardianAssessment)
	}

	completed := ClassifyProtocolNotification(ProtocolNotification{
		Kind:           NotificationGuardianReviewCompleted,
		TurnID:         "turn-1",
		GuardianStatus: GuardianAssessmentApproved,
		GuardianAction: GuardianAssessmentAction{Kind: GuardianActionNetworkAccess, Target: "api.example.com"},
	}, ReplayNone)
	if completed.Route != ProtocolNotificationRouteGuardianReview || completed.GuardianAssessment == nil || completed.GuardianAssessment.Status != GuardianAssessmentApproved || completed.GuardianAssessment.ID != "turn-1" {
		t.Fatalf("completed route = %#v", completed)
	}

	shutdown := ClassifyProtocolNotification(ProtocolNotification{Kind: NotificationShutdownComplete}, ReplayNone)
	if shutdown.Route != ProtocolNotificationRouteShutdownComplete || !shutdown.RequestImmediateExit {
		t.Fatalf("shutdown route = %#v", shutdown)
	}

	diff := ClassifyProtocolNotification(ProtocolNotification{Kind: NotificationTurnDiffUpdated, UnifiedDiff: "diff"}, ReplayNone)
	if diff.Route != ProtocolNotificationRouteTurnDiff || !diff.RefreshStatusLine {
		t.Fatalf("diff route = %#v", diff)
	}

	deprecation := ClassifyProtocolNotification(ProtocolNotification{Kind: NotificationDeprecationNotice, Summary: "old", Details: "use new"}, ReplayNone)
	if deprecation.Route != ProtocolNotificationRouteDeprecationNotice || deprecation.HistorySummary != "old" || deprecation.HistoryDetails != "use new" {
		t.Fatalf("deprecation route = %#v", deprecation)
	}
}

func TestProtocolNotificationReplaySuppressionAndRequestTrimMatchRust(t *testing.T) {
	suppressed := ClassifyProtocolNotification(ProtocolNotification{Kind: NotificationTurnStarted}, ReplayResumeInitialMessages)
	if suppressed.Route != ProtocolNotificationRouteIgnored || !suppressed.SuppressedByReplay {
		t.Fatalf("suppressed route = %#v", suppressed)
	}

	request := ClassifyProtocolRequest(ProtocolRequest{
		Kind:   RequestCommandExecutionApproval,
		ID:     "  ",
		CallID: " call-1 ",
	}, ReplayNone)
	if request.Queue == nil || request.Queue.ID != "call-1" {
		t.Fatalf("trimmed request = %#v", request)
	}

	legacy := ClassifyProtocolRequest(ProtocolRequest{Kind: RequestExecApprovalLegacy}, ReplayNone)
	if legacy.Surface != RequestSurfaceUnsupported || legacy.Error == "" {
		t.Fatalf("legacy request = %#v", legacy)
	}

	replayLegacy := ClassifyProtocolRequest(ProtocolRequest{Kind: RequestExecApprovalLegacy}, ReplayHistory)
	if replayLegacy.Surface != RequestSurfaceUnsupported || replayLegacy.Error != "" {
		t.Fatalf("replay legacy request = %#v", replayLegacy)
	}
}
