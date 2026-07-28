package chatwidget

import (
	"strings"
	"testing"
)

func TestBuiltinApprovalPresetsMatchRustOrderAndCopy(t *testing.T) {
	presets := BuiltinApprovalPresets()
	if len(presets) != 3 {
		t.Fatalf("preset count = %d", len(presets))
	}
	if presets[0].ID != "read-only" || presets[1].ID != "auto" || presets[2].ID != "full-access" {
		t.Fatalf("preset order = %#v", presets)
	}
	if presets[1].Label != "Default" || presets[1].Approval != ApprovalOnRequest || presets[1].ProfileID != WorkspaceProfile {
		t.Fatalf("auto preset = %#v", presets[1])
	}
}

func TestPermissionsPopupViewSkipsReadOnlyOffWindowsAndAddsGuardian(t *testing.T) {
	view := NewPermissionsPopupView(PermissionMenuConfig{
		GuardianApprovalEnabled: true,
		CurrentApprovalPolicy:   ApprovalOnRequest,
		CurrentReviewer:         ApprovalsReviewerAutoReview,
		CurrentProfileID:        WorkspaceProfile,
	})
	if len(view.Items) != 3 {
		t.Fatalf("items = %#v", view.Items)
	}
	if view.Items[0].Name != AskForApprovalLabel || view.Items[1].Name != ApproveForMeLabel || view.Items[2].Name != "Full Access" {
		t.Fatalf("item names = %#v", view.Items)
	}
	if !view.Items[1].Current {
		t.Fatalf("approve-for-me should be current: %#v", view.Items[1])
	}
	if !view.Items[2].RequiresConfirmation {
		t.Fatal("full access should require confirmation by default")
	}
}

func TestPermissionsPopupViewIncludesReadOnlyAndWindowsHint(t *testing.T) {
	view := NewPermissionsPopupView(PermissionMenuConfig{
		IncludeReadOnly:        true,
		WindowsDegradedSandbox: true,
	})
	if len(view.Items) != 3 {
		t.Fatalf("items = %#v", view.Items)
	}
	if view.Items[0].Name != "Read Only" || view.Items[1].Name != "Ask for approval (non-admin sandbox)" || view.Items[2].Name != "Full Access" {
		t.Fatalf("windows items = %#v", view.Items)
	}
	if !strings.Contains(view.FooterNote, "/setup-default-sandbox") {
		t.Fatalf("footer note = %q", view.FooterNote)
	}
}

func TestPermissionsPopupViewDisablesBuiltinsByRequirements(t *testing.T) {
	view := NewPermissionsPopupView(PermissionMenuConfig{
		IncludeReadOnly: true,
		Requirements: PermissionRequirements{
			AllowedApprovalPolicies: []ApprovalPolicy{ApprovalOnRequest},
			AllowedReviewers:        []ApprovalsReviewer{ApprovalsReviewerUser},
			AllowedProfiles:         map[string]bool{WorkspaceProfile: true, ReadOnlyProfile: true},
		},
	})
	if len(view.Items) != 3 {
		t.Fatalf("items = %#v", view.Items)
	}
	if view.Items[0].DisabledReason != "" || view.Items[1].DisabledReason != "" {
		t.Fatalf("read-only/workspace should be enabled: %#v", view.Items)
	}
	if view.Items[2].Name != "Full Access" || view.Items[2].DisabledReason != "Disabled by requirements." {
		t.Fatalf("full access should be disabled by requirements: %#v", view.Items[2])
	}
}

func TestPermissionsPopupViewDisablesReviewerByRequirements(t *testing.T) {
	view := NewPermissionsPopupView(PermissionMenuConfig{
		GuardianApprovalEnabled: true,
		Requirements: PermissionRequirements{
			AllowedReviewers: []ApprovalsReviewer{ApprovalsReviewerUser},
		},
	})
	if len(view.Items) != 3 {
		t.Fatalf("items = %#v", view.Items)
	}
	if view.Items[0].Name != AskForApprovalLabel || view.Items[0].DisabledReason != "" {
		t.Fatalf("user approval should be enabled: %#v", view.Items[0])
	}
	if view.Items[1].Name != ApproveForMeLabel || view.Items[1].DisabledReason != "Disabled by requirements." {
		t.Fatalf("auto reviewer should be disabled: %#v", view.Items[1])
	}
}

func TestWindowsSandboxModeAllowedHonorsRequirements(t *testing.T) {
	requirements := PermissionRequirements{
		AllowedWindowsSandboxModes: []WindowsSandboxMode{WindowsSandboxModeDefault},
	}
	if !WindowsSandboxModeAllowed(requirements, WindowsSandboxModeUnelevated) {
		t.Fatal("default requirement should allow unelevated setup")
	}
	if WindowsSandboxModeAllowed(requirements, WindowsSandboxModeElevated) {
		t.Fatal("default requirement should not allow elevated setup")
	}
	requirements.AllowedWindowsSandboxModes = []WindowsSandboxMode{WindowsSandboxModeDisabled}
	if WindowsSandboxModeAllowed(requirements, WindowsSandboxModeUnelevated) || WindowsSandboxModeAllowed(requirements, WindowsSandboxModeElevated) {
		t.Fatal("disabled requirement should block setup modes")
	}
}

func TestPermissionProfilesPopupViewIncludesBuiltinsAndCustomProfiles(t *testing.T) {
	view := NewPermissionProfilesPopupView(PermissionMenuConfig{
		GuardianApprovalEnabled: true,
		CurrentApprovalPolicy:   ApprovalOnRequest,
		CurrentReviewer:         ApprovalsReviewerUser,
		CurrentProfileID:        "dev",
		CustomProfiles: []CustomPermissionProfile{
			{ID: "dev", Description: "Developer profile.", Allowed: true},
			{ID: "locked", Allowed: false},
		},
	})
	if len(view.Items) != 6 {
		t.Fatalf("items = %#v", view.Items)
	}
	if view.Items[0].Name != AskForApprovalLabel || view.Items[1].Name != ApproveForMeLabel || view.Items[2].Name != "Full Access" || view.Items[3].Name != "Read Only" {
		t.Fatalf("builtin order = %#v", view.Items[:4])
	}
	if !view.Items[4].Current || view.Items[4].Description != "Developer profile." {
		t.Fatalf("custom current = %#v", view.Items[4])
	}
	if view.Items[5].DisabledReason != "Disabled by requirements." || view.Items[5].Description != "Configured permission profile." {
		t.Fatalf("custom disabled = %#v", view.Items[5])
	}
}

func TestPermissionModeActionDecisionOrderMatchesRust(t *testing.T) {
	presets := approvalPresetsByID()
	fullAccess := PermissionModeActionDecisionForPreset(PermissionModeActionContext{
		Preset:                presets["full-access"],
		Reviewer:              ApprovalsReviewerUser,
		ReturnToPermissions:   true,
		HideFullAccessWarning: false,
		IsWindows:             true,
		WindowsSandboxLevel:   WindowsSandboxLevelDisabled,
	})
	if fullAccess.Kind != PermissionModeActionOpenFullAccessConfirmation || !fullAccess.ReturnToPermissions {
		t.Fatalf("full access decision = %#v", fullAccess)
	}

	hiddenWarning := PermissionModeActionDecisionForPreset(PermissionModeActionContext{
		Preset:                presets["full-access"],
		Reviewer:              ApprovalsReviewerUser,
		HideFullAccessWarning: true,
		IsWindows:             true,
		WindowsSandboxLevel:   WindowsSandboxLevelDisabled,
	})
	if hiddenWarning.Kind != PermissionModeActionOpenFullAccessConfirmation {
		t.Fatalf("hidden warning must not bypass full-access confirmation: %#v", hiddenWarning)
	}

	setupPrompt := PermissionModeActionDecisionForPreset(PermissionModeActionContext{
		Preset:                      presets["auto"],
		Reviewer:                    ApprovalsReviewerUser,
		IsWindows:                   true,
		WindowsSandboxLevel:         WindowsSandboxLevelDisabled,
		WindowsSandboxSetupComplete: false,
	})
	if setupPrompt.Kind != PermissionModeActionOpenWindowsSandboxEnablePrompt || setupPrompt.WindowsSandboxMode != WindowsSandboxModeElevated {
		t.Fatalf("setup prompt decision = %#v", setupPrompt)
	}

	enableSandbox := PermissionModeActionDecisionForPreset(PermissionModeActionContext{
		Preset:                      presets["auto"],
		Reviewer:                    ApprovalsReviewerUser,
		IsWindows:                   true,
		WindowsSandboxLevel:         WindowsSandboxLevelDisabled,
		WindowsSandboxSetupComplete: true,
	})
	if enableSandbox.Kind != PermissionModeActionEnableWindowsSandboxForAgent || enableSandbox.WindowsSandboxMode != WindowsSandboxModeElevated {
		t.Fatalf("enable sandbox decision = %#v", enableSandbox)
	}

	worldWritable := PermissionModeActionDecisionForPreset(PermissionModeActionContext{
		Preset:                        presets["auto"],
		Reviewer:                      ApprovalsReviewerUser,
		IsWindows:                     true,
		WindowsSandboxLevel:           WindowsSandboxLevelUnelevated,
		WorldWritableWarningAvailable: true,
	})
	if worldWritable.Kind != PermissionModeActionOpenWorldWritableWarning {
		t.Fatalf("world writable decision = %#v", worldWritable)
	}

	autoReview := PermissionModeActionDecisionForPreset(PermissionModeActionContext{
		Preset:                        presets["auto"],
		Reviewer:                      ApprovalsReviewerAutoReview,
		IsWindows:                     true,
		WindowsSandboxLevel:           WindowsSandboxLevelDisabled,
		WorldWritableWarningAvailable: true,
	})
	if autoReview.Kind != PermissionModeActionApply {
		t.Fatalf("auto-review decision = %#v", autoReview)
	}
}

func TestFullAccessConfirmationView(t *testing.T) {
	view := FullAccessConfirmationView()
	if len(view.Items) != 2 {
		t.Fatalf("items = %#v", view.Items)
	}
	if view.Items[0].Name != "Yes, continue anyway" || view.Items[1].Name != "Cancel" {
		t.Fatalf("confirmation items = %#v", view.Items)
	}
}

func TestPermissionPresetMatchesCurrentAliases(t *testing.T) {
	config := PermissionMenuConfig{
		CurrentApprovalPolicy: ApprovalNever,
		CurrentReviewer:       ApprovalsReviewerUser,
		CurrentProfileID:      "full-access",
	}
	if !PermissionPresetMatchesCurrent(config, DangerFullAccessProfile, ApprovalNever, ApprovalsReviewerUser) {
		t.Fatal("full-access alias should match danger profile")
	}
	if PermissionPresetMatchesCurrent(config, WorkspaceProfile, ApprovalOnRequest, ApprovalsReviewerUser) {
		t.Fatal("different preset should not match")
	}
}
