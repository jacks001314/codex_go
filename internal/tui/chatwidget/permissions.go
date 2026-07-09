package chatwidget

import "strings"

const (
	AskForApprovalLabel     = "Ask for approval"
	ApproveForMeLabel       = "Approve for me"
	AutoReviewDescription   = "Only ask for actions detected as potentially unsafe."
	DangerFullAccessProfile = ":danger-full-access"
	ReadOnlyProfile         = ":read-only"
	WorkspaceProfile        = ":workspace"
)

type ApprovalPolicy string

const (
	ApprovalOnRequest ApprovalPolicy = "on-request"
	ApprovalNever     ApprovalPolicy = "never"
)

type ApprovalsReviewer string

const (
	ApprovalsReviewerUser       ApprovalsReviewer = "user"
	ApprovalsReviewerAutoReview ApprovalsReviewer = "auto_review"
)

type ApprovalPreset struct {
	ID          string
	Label       string
	Description string
	Approval    ApprovalPolicy
	ProfileID   string
}

type CustomPermissionProfile struct {
	ID          string
	Description string
	Allowed     bool
}

type PermissionRequirements struct {
	AllowedApprovalPolicies    []ApprovalPolicy
	AllowedReviewers           []ApprovalsReviewer
	AllowedProfiles            map[string]bool
	AllowedWindowsSandboxModes []WindowsSandboxMode
}

type PermissionMenuConfig struct {
	ExplicitPermissionProfileMode bool
	GuardianApprovalEnabled       bool
	IncludeReadOnly               bool
	WindowsDegradedSandbox        bool
	HideFullAccessWarning         bool
	Requirements                  PermissionRequirements
	CurrentApprovalPolicy         ApprovalPolicy
	CurrentReviewer               ApprovalsReviewer
	CurrentProfileID              string
	CustomProfiles                []CustomPermissionProfile
}

type PermissionMenuView struct {
	Title       string
	HeaderLines []string
	FooterHint  string
	FooterNote  string
	Items       []PermissionMenuItem
}

type PermissionMenuItem struct {
	ID                   string
	Name                 string
	Description          string
	Current              bool
	DisabledReason       string
	DismissOnSelect      bool
	RequiresConfirmation bool
	ApprovalPolicy       *ApprovalPolicy
	Reviewer             *ApprovalsReviewer
	ProfileID            string
}

type PermissionModeActionKind string

const (
	PermissionModeActionApply                          PermissionModeActionKind = "apply"
	PermissionModeActionOpenFullAccessConfirmation     PermissionModeActionKind = "open_full_access_confirmation"
	PermissionModeActionEnableWindowsSandboxForAgent   PermissionModeActionKind = "enable_windows_sandbox_for_agent"
	PermissionModeActionOpenWindowsSandboxEnablePrompt PermissionModeActionKind = "open_windows_sandbox_enable_prompt"
	PermissionModeActionOpenWorldWritableWarning       PermissionModeActionKind = "open_world_writable_warning"
)

type PermissionModeActionContext struct {
	Preset                        ApprovalPreset
	Label                         string
	Reviewer                      ApprovalsReviewer
	ProfileID                     string
	ReturnToPermissions           bool
	HideFullAccessWarning         bool
	IsWindows                     bool
	WindowsSandboxLevel           WindowsSandboxLevel
	WindowsSandboxSetupComplete   bool
	WorldWritableWarningAvailable bool
}

type PermissionModeActionDecision struct {
	Kind                PermissionModeActionKind
	PresetID            string
	Label               string
	Reviewer            ApprovalsReviewer
	ProfileID           string
	ReturnToPermissions bool
	WindowsSandboxMode  WindowsSandboxMode
}

func BuiltinApprovalPresets() []ApprovalPreset {
	return []ApprovalPreset{
		{
			ID:          "read-only",
			Label:       "Read Only",
			Description: "Codex can read files in the current workspace. Approval is required to edit files or access the internet.",
			Approval:    ApprovalOnRequest,
			ProfileID:   ReadOnlyProfile,
		},
		{
			ID:          "auto",
			Label:       "Default",
			Description: "Codex can read and edit files in the current workspace, and run commands. Approval is required to access the internet or edit other files. (Identical to Agent mode)",
			Approval:    ApprovalOnRequest,
			ProfileID:   WorkspaceProfile,
		},
		{
			ID:          "full-access",
			Label:       "Full Access",
			Description: "Codex can edit files outside this workspace and access the internet without asking for approval. Exercise caution when using.",
			Approval:    ApprovalNever,
			ProfileID:   DangerFullAccessProfile,
		},
	}
}

func NewPermissionsPopupView(config PermissionMenuConfig) PermissionMenuView {
	if config.ExplicitPermissionProfileMode {
		return NewPermissionProfilesPopupView(config)
	}
	items := []PermissionMenuItem{}
	for _, preset := range BuiltinApprovalPresets() {
		if preset.ID == "read-only" && !config.IncludeReadOnly {
			continue
		}
		baseName := preset.Label
		if preset.ID == "auto" && config.WindowsDegradedSandbox {
			baseName = AskForApprovalLabel + " (non-admin sandbox)"
		} else if preset.ID == "auto" {
			baseName = AskForApprovalLabel
		}
		description := strings.ReplaceAll(preset.Description, " (Identical to Agent mode)", "")
		if preset.ID == "auto" {
			items = append(items, permissionPresetItem(config, preset, baseName, description, ApprovalsReviewerUser))
			if config.GuardianApprovalEnabled {
				items = append(items, permissionPresetItem(config, preset, ApproveForMeLabel, AutoReviewDescription, ApprovalsReviewerAutoReview))
			}
			continue
		}
		items = append(items, permissionPresetItem(config, preset, baseName, description, ApprovalsReviewerUser))
	}

	footerNote := ""
	if config.WindowsDegradedSandbox {
		footerNote = "The non-admin sandbox protects your files and prevents network access under most circumstances. To upgrade to the default sandbox, run /setup-default-sandbox."
	}
	return PermissionMenuView{
		Title:      "Update Model Permissions",
		FooterHint: standardPopupHintLine,
		FooterNote: footerNote,
		Items:      items,
	}
}

func NewPermissionProfilesPopupView(config PermissionMenuConfig) PermissionMenuView {
	presets := approvalPresetsByID()
	items := []PermissionMenuItem{}
	if preset, ok := presets["auto"]; ok {
		description := strings.ReplaceAll(preset.Description, " (Identical to Agent mode)", "")
		items = append(items, builtinPermissionModeSelectionItem(config, preset, WorkspaceProfile, AskForApprovalLabel, description, ApprovalOnRequest, ApprovalsReviewerUser))
		if config.GuardianApprovalEnabled {
			items = append(items, builtinPermissionModeSelectionItem(config, preset, WorkspaceProfile, ApproveForMeLabel, AutoReviewDescription, ApprovalOnRequest, ApprovalsReviewerAutoReview))
		}
	}
	if preset, ok := presets["full-access"]; ok {
		items = append(items, builtinPermissionModeSelectionItem(config, preset, DangerFullAccessProfile, preset.Label, preset.Description, preset.Approval, ApprovalsReviewerUser))
	}
	if preset, ok := presets["read-only"]; ok {
		items = append(items, builtinPermissionModeSelectionItem(config, preset, ReadOnlyProfile, preset.Label, preset.Description, preset.Approval, ApprovalsReviewerUser))
	}
	for _, profile := range config.CustomProfiles {
		items = append(items, PermissionMenuItem{
			ID:              profile.ID,
			Name:            profile.ID,
			Description:     firstNonEmptyPermission(profile.Description, "Configured permission profile."),
			Current:         config.CurrentProfileID == profile.ID,
			DisabledReason:  disabledReasonForAllowed(profile.Allowed),
			DismissOnSelect: true,
			ProfileID:       profile.ID,
		})
	}
	return PermissionMenuView{
		Title:      "Update Model Permissions",
		FooterHint: standardPopupHintLine,
		Items:      items,
	}
}

func PermissionModeActionDecisionForPreset(context PermissionModeActionContext) PermissionModeActionDecision {
	profileID := firstNonEmptyPermission(context.ProfileID, context.Preset.ProfileID)
	label := firstNonEmptyPermission(context.Label, context.Preset.Label)
	base := PermissionModeActionDecision{
		Kind:                PermissionModeActionApply,
		PresetID:            context.Preset.ID,
		Label:               label,
		Reviewer:            context.Reviewer,
		ProfileID:           profileID,
		ReturnToPermissions: context.ReturnToPermissions,
	}
	if context.Reviewer == ApprovalsReviewerUser &&
		context.Preset.ID == "full-access" &&
		!context.HideFullAccessWarning {
		base.Kind = PermissionModeActionOpenFullAccessConfirmation
		return base
	}
	if context.Reviewer == ApprovalsReviewerUser &&
		context.Preset.ID == "auto" &&
		context.IsWindows {
		if context.WindowsSandboxLevel == WindowsSandboxLevelDisabled {
			base.WindowsSandboxMode = WindowsSandboxModeElevated
			if context.WindowsSandboxSetupComplete {
				base.Kind = PermissionModeActionEnableWindowsSandboxForAgent
				return base
			}
			base.Kind = PermissionModeActionOpenWindowsSandboxEnablePrompt
			return base
		}
		if context.WorldWritableWarningAvailable {
			base.Kind = PermissionModeActionOpenWorldWritableWarning
			return base
		}
	}
	return base
}

func FullAccessConfirmationView() PermissionMenuView {
	return PermissionMenuView{
		FooterHint: standardPopupHintLine,
		HeaderLines: []string{
			"Enable full access?",
			"When Codex runs with full access, it can edit any file on your computer and run commands with network, without your approval. Exercise caution when enabling full access. This significantly increases the risk of data loss, leaks, or unexpected behavior.",
		},
		Items: []PermissionMenuItem{
			{
				ID:              "continue",
				Name:            "Yes, continue anyway",
				Description:     "Apply full access for this session",
				DismissOnSelect: true,
			},
			{
				ID:              "remember",
				Name:            "Yes, and don't ask again",
				Description:     "Enable full access and remember this choice",
				DismissOnSelect: true,
			},
			{
				ID:              "cancel",
				Name:            "Cancel",
				Description:     "Go back without enabling full access",
				DismissOnSelect: true,
			},
		},
	}
}

func permissionPresetItem(config PermissionMenuConfig, preset ApprovalPreset, name string, description string, reviewer ApprovalsReviewer) PermissionMenuItem {
	return builtinPermissionModeSelectionItem(config, preset, preset.ProfileID, name, description, preset.Approval, reviewer)
}

func builtinPermissionModeSelectionItem(config PermissionMenuConfig, preset ApprovalPreset, profileID string, label string, description string, approval ApprovalPolicy, reviewer ApprovalsReviewer) PermissionMenuItem {
	approvalCopy := approval
	reviewerCopy := reviewer
	return PermissionMenuItem{
		ID:                   preset.ID + ":" + string(reviewer),
		Name:                 label,
		Description:          description,
		Current:              PermissionPresetMatchesCurrent(config, profileID, approval, reviewer),
		DismissOnSelect:      true,
		RequiresConfirmation: reviewer == ApprovalsReviewerUser && preset.ID == "full-access" && !config.HideFullAccessWarning,
		DisabledReason:       permissionRequirementsDisabledReason(config.Requirements, profileID, approval, reviewer),
		ApprovalPolicy:       &approvalCopy,
		Reviewer:             &reviewerCopy,
		ProfileID:            profileID,
	}
}

func PermissionPresetMatchesCurrent(config PermissionMenuConfig, profileID string, approval ApprovalPolicy, reviewer ApprovalsReviewer) bool {
	return normalizePermissionProfileID(config.CurrentProfileID) == normalizePermissionProfileID(profileID) &&
		config.CurrentApprovalPolicy == approval &&
		config.CurrentReviewer == reviewer
}

func approvalPresetsByID() map[string]ApprovalPreset {
	out := map[string]ApprovalPreset{}
	for _, preset := range BuiltinApprovalPresets() {
		out[preset.ID] = preset
	}
	return out
}

func disabledReasonForAllowed(allowed bool) string {
	if allowed {
		return ""
	}
	return "Disabled by requirements."
}

func permissionRequirementsDisabledReason(requirements PermissionRequirements, profileID string, approval ApprovalPolicy, reviewer ApprovalsReviewer) string {
	if !approvalPolicyAllowed(requirements.AllowedApprovalPolicies, approval) {
		return "Disabled by requirements."
	}
	if !reviewerAllowed(requirements.AllowedReviewers, reviewer) {
		return "Disabled by requirements."
	}
	if !permissionProfileAllowed(requirements.AllowedProfiles, profileID) {
		return "Disabled by requirements."
	}
	return ""
}

func approvalPolicyAllowed(allowed []ApprovalPolicy, policy ApprovalPolicy) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, candidate := range allowed {
		if candidate == policy {
			return true
		}
	}
	return false
}

func reviewerAllowed(allowed []ApprovalsReviewer, reviewer ApprovalsReviewer) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, candidate := range allowed {
		if candidate == reviewer {
			return true
		}
	}
	return false
}

func permissionProfileAllowed(allowed map[string]bool, profileID string) bool {
	if len(allowed) == 0 {
		return true
	}
	normalized := normalizePermissionProfileID(profileID)
	for key, value := range allowed {
		if normalizePermissionProfileID(key) == normalized {
			return value
		}
	}
	return false
}

func normalizePermissionProfileID(id string) string {
	switch strings.TrimSpace(id) {
	case "full-access":
		return DangerFullAccessProfile
	case "read-only":
		return ReadOnlyProfile
	case "workspace-write", "auto":
		return WorkspaceProfile
	default:
		return strings.TrimSpace(id)
	}
}

func firstNonEmptyPermission(value string, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}
