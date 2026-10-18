package chatwidget

import (
	"testing"
)

// TestPermissionPresetDescriptionsMatchRustMenuWording pins #47329: built-in
// permission presets share concise, period-free descriptions across every
// permission picker.
func TestPermissionPresetDescriptionsMatchRustMenuWording(t *testing.T) {
	want := map[string]string{
		"read-only":   "Read workspace files, with approval required for edits or internet access",
		"auto":        "Read and edit workspace files and run commands, with approval required for internet access or edits outside the workspace",
		"full-access": "Use with caution: Codex can edit files outside this workspace and access the internet without approval",
	}
	for _, preset := range BuiltinApprovalPresets() {
		if description, ok := want[preset.ID]; ok {
			if got := PermissionPresetDescription(preset); got != description {
				t.Fatalf("PermissionPresetDescription(%s) = %q, want %q", preset.ID, got, description)
			}
		}
	}

	permissions := NewPermissionsPopupView(PermissionMenuConfig{GuardianApprovalEnabled: true, IncludeReadOnly: true})
	permissionByPreset := map[string]PermissionMenuItem{}
	for _, item := range permissions.Items {
		if item.ProfileID != "" {
			permissionByPreset[item.ProfileID+"|"+string(valueOrZeroReviewer(item.Reviewer))] = item
		}
	}
	if got := permissionByPreset[WorkspaceProfile+"|"+string(ApprovalsReviewerUser)].Description; got != want["auto"] {
		t.Fatalf("permissions workspace description = %q", got)
	}
	if got := permissionByPreset[ReadOnlyProfile+"|"+string(ApprovalsReviewerUser)].Description; got != want["read-only"] {
		t.Fatalf("permissions read-only description = %q", got)
	}
	if got := permissionByPreset[DangerFullAccessProfile+"|"+string(ApprovalsReviewerUser)].Description; got != want["full-access"] {
		t.Fatalf("permissions full-access description = %q", got)
	}
	if got := permissionByPreset[WorkspaceProfile+"|"+string(ApprovalsReviewerAutoReview)].Description; got != AutoReviewDescription {
		t.Fatalf("permissions approve-for-me description = %q", got)
	}

	view := NewPermissionProfilesPopupView(PermissionMenuConfig{
		GuardianApprovalEnabled: true,
		CustomProfiles: []CustomPermissionProfile{{
			ID:          "custom",
			Allowed:     false,
			Description: "",
		}},
	})
	byID := map[string]PermissionMenuItem{}
	for _, item := range view.Items {
		byID[item.ProfileID+"|"+string(valueOrZeroReviewer(item.Reviewer))] = item
	}
	workspace := byID[WorkspaceProfile+"|"+string(ApprovalsReviewerUser)]
	if got := workspace.Description; got != want["auto"] {
		t.Fatalf("workspace description = %q", got)
	}
	readOnly := byID[ReadOnlyProfile+"|"+string(ApprovalsReviewerUser)]
	if got := readOnly.Description; got != want["read-only"] {
		t.Fatalf("read-only description = %q", got)
	}
	fullAccess := byID[DangerFullAccessProfile+"|"+string(ApprovalsReviewerUser)]
	if got := fullAccess.Description; got != want["full-access"] {
		t.Fatalf("full-access description = %q", got)
	}
	custom := byID["custom|"]
	if custom.DisabledReason != "Disabled by requirements" {
		t.Fatalf("disabled reason = %q", custom.DisabledReason)
	}
	if custom.Description != "Configured permission profile" {
		t.Fatalf("custom fallback = %q", custom.Description)
	}
}

func valueOrZeroReviewer(reviewer *ApprovalsReviewer) ApprovalsReviewer {
	if reviewer == nil {
		return ""
	}
	return *reviewer
}

func TestInlinePermissionPresetDescriptionsShareMenuCopy(t *testing.T) {
	for _, preset := range BuiltinApprovalPresets() {
		entry, _, ok := inlinePermissionProfileCatalogEntry(PermissionMenuConfig{}, preset.ProfileID)
		if !ok {
			t.Fatalf("preset %s missing from inline permission catalog", preset.ID)
		}
		if entry.description != PermissionPresetDescription(preset) {
			t.Fatalf("inline %s description = %q, want %q", preset.ID, entry.description, PermissionPresetDescription(preset))
		}
	}
}
