package rollout

import (
	"encoding/json"
	"testing"
)

func resumeSettingsEventLine(t *testing.T, threadID, approvalPolicy, reviewer string, profile map[string]any, rawPermissionProfile any) Line {
	t.Helper()
	settings := map[string]any{
		"approval_policy":    approvalPolicy,
		"approvals_reviewer": reviewer,
		"cwd":                "/work",
	}
	if profile != nil {
		settings["active_permission_profile"] = profile
	}
	if rawPermissionProfile != nil {
		settings["permission_profile"] = rawPermissionProfile
	}
	event := map[string]any{"type": "thread_settings_applied", "thread_settings": settings}
	if threadID != "" {
		event["thread_id"] = threadID
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("Marshal(settings) error = %v", err)
	}
	return Line{Type: "event_msg", Payload: encoded}
}

func resumeSettingsTurnContextLine(t *testing.T, turnID, approvalPolicy, reviewer string, profile map[string]any) Line {
	t.Helper()
	context := map[string]any{"turn_id": turnID, "approval_policy": approvalPolicy}
	if reviewer != "" {
		context["approvals_reviewer"] = reviewer
	}
	if profile != nil {
		context["active_permission_profile"] = profile
	}
	encoded, err := json.Marshal(context)
	if err != nil {
		t.Fatalf("Marshal(turn_context) error = %v", err)
	}
	return Line{Type: "turn_context", TurnContext: encoded}
}

func TestLatestPersistedResumeSettingsLatestSnapshotWinsLikeRust(t *testing.T) {
	lines := []Line{
		resumeSettingsEventLine(t, "", "never", "user", nil, nil),
		resumeSettingsEventLine(t, "", "on-request", "auto-review", map[string]any{"id": "dev"}, nil),
	}
	settings, ok := LatestPersistedResumeSettings("thread-a", lines)
	if !ok {
		t.Fatal("LatestPersistedResumeSettings() = none, want legacy settings")
	}
	if settings.ApprovalPolicy != "on-request" || settings.ApprovalsReviewer != "auto-review" {
		t.Fatalf("settings = %#v, want the latest legacy snapshot", settings)
	}
	if string(settings.ActivePermissionProfile) != `{"id":"dev"}` {
		t.Fatalf("active permission profile = %s", settings.ActivePermissionProfile)
	}
	if settings.UnnamedPermissionProfile != nil {
		t.Fatalf("unnamed permission profile = %s, want none", settings.UnnamedPermissionProfile)
	}
}

func TestLatestPersistedResumeSettingsTurnContextWinsOverEarlierSettingsLikeRust(t *testing.T) {
	lines := []Line{
		resumeSettingsEventLine(t, "", "never", "auto-review", map[string]any{"id": "dev"}, nil),
		resumeSettingsTurnContextLine(t, "turn-1", "unless-trusted", "user", map[string]any{"id": "read-only"}),
	}
	settings, ok := LatestPersistedResumeSettings("thread-a", lines)
	if !ok {
		t.Fatal("LatestPersistedResumeSettings() = none")
	}
	if settings.ApprovalPolicy != "unless-trusted" || settings.ApprovalsReviewer != "user" {
		t.Fatalf("settings = %#v, want the latest turn context", settings)
	}
	if string(settings.ActivePermissionProfile) != `{"id":"read-only"}` {
		t.Fatalf("active permission profile = %s", settings.ActivePermissionProfile)
	}
}

func TestLatestPersistedResumeSettingsOwnershipBoundariesLikeRust(t *testing.T) {
	owned := resumeSettingsEventLine(t, "thread-a", "never", "auto-review", map[string]any{"id": ":workspace"}, nil)
	legacy := resumeSettingsEventLine(t, "", "on-request", "user", map[string]any{"id": "read-only"}, nil)
	staleTurn := resumeSettingsTurnContextLine(t, "turn-1", "never", "auto-review", map[string]any{"id": ":workspace"})
	foreign := resumeSettingsEventLine(t, "thread-b", "never", "auto-review", map[string]any{"id": ":workspace"}, nil)
	foreignLegacy := resumeSettingsEventLine(t, "", "never", "auto-review", map[string]any{"id": ":workspace"}, nil)

	lines := []Line{owned, legacy, staleTurn}
	settings, _ := LatestPersistedResumeSettings("thread-a", lines)
	if settings.ApprovalPolicy != "on-request" || settings.ApprovalsReviewer != "user" ||
		string(settings.ActivePermissionProfile) != `{"id":"read-only"}` {
		t.Fatalf("settings = %#v, want the thread's newer legacy update", settings)
	}

	lines = append(lines, foreign, foreignLegacy)
	settings, _ = LatestPersistedResumeSettings("thread-a", lines)
	if settings.ApprovalPolicy != "on-request" || settings.ApprovalsReviewer != "user" ||
		string(settings.ActivePermissionProfile) != `{"id":"read-only"}` {
		t.Fatalf("settings = %#v, want foreign snapshots ignored", settings)
	}

	settings, _ = LatestPersistedResumeSettings("thread-a", []Line{owned, foreign, legacy, staleTurn})
	if settings.ApprovalPolicy != "never" || settings.ApprovalsReviewer != "auto-review" ||
		string(settings.ActivePermissionProfile) != `{"id":":workspace"}` {
		t.Fatalf("settings = %#v, want the owned checkpoint without a trailing legacy update", settings)
	}
}

func TestLatestPersistedResumeSettingsOlderReviewerFillsLatestContextLikeRust(t *testing.T) {
	lines := []Line{
		resumeSettingsEventLine(t, "", "never", "auto-review", nil, nil),
		resumeSettingsTurnContextLine(t, "turn-1", "on-request", "", nil),
	}
	settings, ok := LatestPersistedResumeSettings("thread-a", lines)
	if !ok || settings.ApprovalPolicy != "on-request" || settings.ApprovalsReviewer != "auto-review" {
		t.Fatalf("settings = %#v, want the earlier reviewer", settings)
	}
}

func TestLatestPersistedResumeSettingsUnattributedSnapshotHasNoUnnamedProfileLikeRust(t *testing.T) {
	profile := map[string]any{"type": "managed", "network": "restricted"}
	lines := []Line{resumeSettingsEventLine(t, "", "on-request", "user", nil, profile)}
	settings, ok := LatestPersistedResumeSettings("thread-a", lines)
	if !ok {
		t.Fatal("LatestPersistedResumeSettings() = none, want legacy settings")
	}
	if settings.UnnamedPermissionProfile != nil {
		t.Fatalf("unnamed permission profile = %s, want none", settings.UnnamedPermissionProfile)
	}
}

func TestLatestPersistedResumeSettingsOwnedSnapshotRestoresUnnamedProfile(t *testing.T) {
	profile := map[string]any{"type": "managed", "network": "restricted"}
	lines := []Line{resumeSettingsEventLine(t, "thread-a", "on-request", "user", nil, profile)}
	settings, ok := LatestPersistedResumeSettings("thread-a", lines)
	if !ok || settings.UnnamedPermissionProfile == nil {
		t.Fatalf("settings = %#v, want an owned unnamed profile", settings)
	}
	if string(settings.UnnamedPermissionProfile) != `{"network":"restricted","type":"managed"}` {
		t.Fatalf("unnamed permission profile = %s", settings.UnnamedPermissionProfile)
	}
}

func TestLatestPersistedResumeSettingsNamedProfileSuppressesUnnamedLikeRust(t *testing.T) {
	profile := map[string]any{"type": "managed", "network": "restricted"}
	lines := []Line{resumeSettingsEventLine(t, "thread-a", "on-request", "user", map[string]any{"id": "dev"}, profile)}
	settings, ok := LatestPersistedResumeSettings("thread-a", lines)
	if !ok || settings.UnnamedPermissionProfile != nil {
		t.Fatalf("settings = %#v, want no unnamed profile beside a named one", settings)
	}
}
