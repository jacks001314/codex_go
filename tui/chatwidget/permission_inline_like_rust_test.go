package chatwidget

import (
	"testing"
)

func inlinePolicy(value ApprovalPolicy) *ApprovalPolicy { return &value }

func inlineReviewer(value ApprovalsReviewer) *ApprovalsReviewer { return &value }

// TestInlinePermissionProfileSelectionMatchesRust covers #54383's inline
// `/permissions` resolution (ChatWidget::try_select_permission_profile,
// codex-rs/tui/src/chatwidget/permissions_menu.rs:53-146) and #54385's
// requested approval policy / reviewer overrides.
func TestInlinePermissionProfileSelectionMatchesRust(t *testing.T) {
	guardian := PermissionMenuConfig{
		IncludeReadOnly:         true,
		GuardianApprovalEnabled: true,
		CurrentProfileID:        WorkspaceProfile,
		CurrentApprovalPolicy:   ApprovalOnRequest,
		CurrentReviewer:         ApprovalsReviewerUser,
		CustomProfiles: []CustomPermissionProfile{
			{ID: "locked-down", Description: "Inspect and patch only approved workspace files.", Allowed: true},
			{ID: "web-enabled", Allowed: false},
		},
	}
	vectors := []struct {
		name        string
		config      PermissionMenuConfig
		profileID   string
		policy      *ApprovalPolicy
		reviewer    *ApprovalsReviewer
		wantProfile string
		wantName    string
		wantPolicy  ApprovalPolicy
		wantReview  ApprovalsReviewer
		wantConfirm bool
		failure     string
	}{
		{
			name: "built-in profile", config: guardian, profileID: ReadOnlyProfile,
			wantProfile: ReadOnlyProfile, wantName: "Read Only", wantPolicy: ApprovalOnRequest, wantReview: ApprovalsReviewerUser,
		},
		{
			name: "full access keeps its confirmation for an automatic reviewer", config: guardian,
			profileID: DangerFullAccessProfile, reviewer: inlineReviewer(ApprovalsReviewerAutoReview),
			wantProfile: DangerFullAccessProfile, wantName: "Full Access", wantPolicy: ApprovalNever,
			wantReview: ApprovalsReviewerAutoReview, wantConfirm: true,
		},
		{
			name: "ask-for-approval alias", config: guardian, profileID: InlinePermissionProfileAskForApprovalAlias,
			wantProfile: WorkspaceProfile, wantName: AskForApprovalLabel, wantPolicy: ApprovalOnRequest, wantReview: ApprovalsReviewerUser,
		},
		{
			name: "approve-for-me alias", config: guardian, profileID: InlinePermissionProfileApproveForMeAlias,
			wantProfile: WorkspaceProfile, wantName: ApproveForMeLabel, wantPolicy: ApprovalOnRequest, wantReview: ApprovalsReviewerAutoReview,
		},
		{
			name: "requested policy wins over the preset", config: guardian, profileID: DangerFullAccessProfile,
			policy:      inlinePolicy(ApprovalOnRequest),
			wantProfile: DangerFullAccessProfile, wantName: "Full Access", wantPolicy: ApprovalOnRequest, wantReview: ApprovalsReviewerUser,
			wantConfirm: true,
		},
		{
			name: "custom profile without overrides", config: guardian, profileID: "locked-down",
			wantProfile: "locked-down", wantName: "locked-down",
		},
		{
			name: "custom profile with granular policy", config: guardian, profileID: "locked-down",
			policy: inlinePolicy(ApprovalGranular), reviewer: inlineReviewer(ApprovalsReviewerUser),
			wantProfile: "locked-down", wantName: "locked-down", wantPolicy: ApprovalGranular, wantReview: ApprovalsReviewerUser,
		},
		{
			name: "unknown profile", config: guardian, profileID: "nope",
			failure: "Unknown permission profile `nope`. Run `/permissions` to see available profiles.",
		},
		{
			name: "profile disabled by the server", config: guardian, profileID: "web-enabled",
			failure: "Permission profile `web-enabled` is unavailable: Disabled by requirements",
		},
		{
			name: "automatic reviewer unavailable", config: PermissionMenuConfig{IncludeReadOnly: true},
			profileID: WorkspaceProfile, reviewer: inlineReviewer(ApprovalsReviewerAutoReview),
			failure: "Approve for me is unavailable. Run `/permissions` to see available profiles.",
		},
		{
			name: "approve-for-me alias unavailable", config: PermissionMenuConfig{IncludeReadOnly: true},
			profileID: InlinePermissionProfileApproveForMeAlias,
			failure:   "Approve for me is unavailable. Run `/permissions` to see available profiles.",
		},
		{
			name: "requested policy disabled by requirements",
			config: PermissionMenuConfig{
				IncludeReadOnly:         true,
				GuardianApprovalEnabled: true,
				Requirements:            PermissionRequirements{AllowedApprovalPolicies: []ApprovalPolicy{ApprovalOnRequest}},
			},
			profileID: DangerFullAccessProfile,
			failure:   "Permission profile `:danger-full-access` is unavailable: Disabled by requirements",
		},
		{
			name: "requested reviewer disabled by requirements",
			config: PermissionMenuConfig{
				IncludeReadOnly:         true,
				GuardianApprovalEnabled: true,
				Requirements:            PermissionRequirements{AllowedReviewers: []ApprovalsReviewer{ApprovalsReviewerUser}},
			},
			profileID: WorkspaceProfile, reviewer: inlineReviewer(ApprovalsReviewerAutoReview),
			failure: "Permission profile `:workspace` is unavailable: Disabled by requirements",
		},
	}
	for _, vector := range vectors {
		t.Run(vector.name, func(t *testing.T) {
			item, failure := InlinePermissionProfileSelection(vector.config, vector.profileID, vector.policy, vector.reviewer)
			if failure != vector.failure {
				t.Fatalf("failure = %q, want %q", failure, vector.failure)
			}
			if vector.failure != "" {
				return
			}
			if item.ProfileID != vector.wantProfile || item.Name != vector.wantName {
				t.Fatalf("item = %#v, want profile %q name %q", item, vector.wantProfile, vector.wantName)
			}
			if item.RequiresConfirmation != vector.wantConfirm {
				t.Fatalf("RequiresConfirmation = %v, want %v", item.RequiresConfirmation, vector.wantConfirm)
			}
			if vector.wantPolicy == "" {
				if item.ApprovalPolicy != nil {
					t.Fatalf("approval policy = %q, want none", *item.ApprovalPolicy)
				}
			} else if item.ApprovalPolicy == nil || *item.ApprovalPolicy != vector.wantPolicy {
				t.Fatalf("approval policy = %#v, want %q", item.ApprovalPolicy, vector.wantPolicy)
			}
			if vector.wantReview == "" {
				if item.Reviewer != nil {
					t.Fatalf("reviewer = %q, want none", *item.Reviewer)
				}
			} else if item.Reviewer == nil || *item.Reviewer != vector.wantReview {
				t.Fatalf("reviewer = %#v, want %q", item.Reviewer, vector.wantReview)
			}
		})
	}
}
