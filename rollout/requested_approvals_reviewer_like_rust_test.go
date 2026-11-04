package rollout

import (
	"encoding/json"
	"testing"
	"time"
)

func requestedReviewerSettingsLine(t *testing.T, threadID, effective, requested string) Line {
	t.Helper()
	settings := map[string]any{
		"approval_policy":    "on-request",
		"approvals_reviewer": effective,
		"cwd":                "/work",
	}
	if requested != "" {
		settings["requested_approvals_reviewer"] = requested
	}
	encoded, err := json.Marshal(map[string]any{
		"type":            "thread_settings_applied",
		"thread_id":       threadID,
		"thread_settings": settings,
	})
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}
	return Line{Type: "event_msg", Payload: encoded}
}

// Rust #59272 restores the requested reviewer rather than the effective value a
// model requirement imposed, and keeps older histories working.
func TestPersistedResumeSettingsPreferRequestedReviewerLikeRust(t *testing.T) {
	settings, ok := LatestPersistedResumeSettings("thread-a", []Line{
		requestedReviewerSettingsLine(t, "thread-a", "auto_review", "user"),
	})
	if !ok || settings.ApprovalsReviewer != "user" {
		t.Fatalf("settings = %#v, ok = %v, want the requested reviewer", settings, ok)
	}
	if settings.RequestedApprovalsReviewer == nil || *settings.RequestedApprovalsReviewer != "user" {
		t.Fatalf("requested reviewer = %v", settings.RequestedApprovalsReviewer)
	}

	legacy, ok := LatestPersistedResumeSettings("thread-a", []Line{
		requestedReviewerSettingsLine(t, "thread-a", "auto_review", ""),
	})
	if !ok || legacy.ApprovalsReviewer != "auto_review" {
		t.Fatalf("legacy settings = %#v, ok = %v, want the effective fallback", legacy, ok)
	}
}

// A TurnContext that recorded both values also restores the requested one.
func TestPersistedResumeSettingsTurnContextPrefersRequestedReviewerLikeRust(t *testing.T) {
	encoded, err := json.Marshal(map[string]any{
		"turn_id":                      "turn-1",
		"approval_policy":              "on-request",
		"approvals_reviewer":           "auto_review",
		"requested_approvals_reviewer": "user",
	})
	if err != nil {
		t.Fatalf("marshal turn context: %v", err)
	}
	settings, ok := LatestPersistedResumeSettings("thread-a", []Line{{Type: "turn_context", TurnContext: encoded}})
	if !ok || settings.ApprovalsReviewer != "user" {
		t.Fatalf("settings = %#v, ok = %v, want the requested reviewer", settings, ok)
	}
}

// The app-server snapshot writer persists the requested reviewer separately and
// leaves it unset when the value is only inherited.
func TestThreadSettingsAppliedPersistsRequestedReviewerLikeRust(t *testing.T) {
	recorder, err := NewRecorder(&CreateParams{
		CodexHome: t.TempDir(),
		ThreadID:  "thread-a",
		SessionID: "thread-a",
		Source:    "cli",
	})
	if err != nil {
		t.Fatalf("NewRecorder: %v", err)
	}
	requested := "user"
	if err := recorder.AppendThreadSettingsAppliedSnapshot("thread-a", ThreadSettingsApplied{
		ApprovalPolicy:             "on-request",
		ApprovalsReviewer:          "auto_review",
		RequestedApprovalsReviewer: &requested,
		CWD:                        "/work",
	}, time.Time{}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	lines, _, err := Load(recorder.Path())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	settings, ok := LatestPersistedResumeSettings("thread-a", lines)
	if !ok || settings.ApprovalsReviewer != "user" {
		t.Fatalf("persisted settings = %#v, ok = %v", settings, ok)
	}
}
