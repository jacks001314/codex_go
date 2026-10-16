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
	// ApprovalUnlessTrusted and ApprovalGranular are the remaining
	// AskForApproval values (app-server-protocol/src/protocol/v2/shared.rs:206-221).
	// The picker only offers the first two, but `/permissions --approval-policy` accepts
	// all four.
	ApprovalUnlessTrusted ApprovalPolicy = "untrusted"
	ApprovalOnRequest     ApprovalPolicy = "on-request"
	ApprovalGranular      ApprovalPolicy = "granular"
	ApprovalNever         ApprovalPolicy = "never"
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
)

type PermissionModeActionContext struct {
	Preset                      ApprovalPreset
	Label                       string
	Reviewer                    ApprovalsReviewer
	ProfileID                   string
	ReturnToPermissions         bool
	HideFullAccessWarning       bool
	IsWindows                   bool
	WindowsSandboxLevel         WindowsSandboxLevel
	WindowsSandboxSetupComplete bool
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
		// Rust PermissionDiscovery::disabled_reason: a named profile is disabled
		// by the server's allowed flag or by the requirements' allowed profiles,
		// approval policies, and reviewers.
		disabled := disabledReasonForAllowed(profile.Allowed)
		if disabled == "" {
			disabled = permissionRequirementsDisabledReason(config.Requirements, profile.ID, config.CurrentApprovalPolicy, config.CurrentReviewer)
		}
		items = append(items, PermissionMenuItem{
			ID:              profile.ID,
			Name:            profile.ID,
			Description:     firstNonEmptyPermission(profile.Description, "Configured permission profile."),
			Current:         config.CurrentProfileID == profile.ID,
			DisabledReason:  disabled,
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
		context.Preset.ID == "full-access" {
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
		ID:              preset.ID + ":" + string(reviewer),
		Name:            label,
		Description:     description,
		Current:         PermissionPresetMatchesCurrent(config, profileID, approval, reviewer),
		DismissOnSelect: true,
		// Rust #54385 (upstream 36803b180a) keeps the full-access confirmation
		// for every reviewer (chatwidget/permission_popups.rs:340).
		RequiresConfirmation: preset.ID == "full-access",
		DisabledReason:       permissionRequirementsDisabledReason(config.Requirements, profileID, approval, reviewer),
		ApprovalPolicy:       &approvalCopy,
		Reviewer:             &reviewerCopy,
		ProfileID:            profileID,
	}
}

// Rust #54383 / #54385: `/permissions` selects a permission profile inline
// instead of only opening the picker, so the same request can name the profile,
// an approval policy, and a reviewer (codex-rs/tui/src/chatwidget/
// permission_discovery.rs:43-77).
const (
	// InlinePermissionProfileAskForApprovalAlias and
	// InlinePermissionProfileApproveForMeAlias are the shorthands Rust maps onto
	// the workspace profile (chatwidget/permissions_menu.rs:71-74).
	InlinePermissionProfileAskForApprovalAlias = ":ask-for-approval"
	InlinePermissionProfileApproveForMeAlias   = ":approve-for-me"
	// InlinePermissionProfileAutoPresetID is the built-in preset the aliases and
	// the workspace profile select (permissions_menu.rs:85-87).
	inlinePermissionProfileAutoPresetID = "auto"
	// inlinePermissionProfileFullAccessPresetID requires confirmation for every
	// reviewer (#54385).
	inlinePermissionProfileFullAccessPresetID = "full-access"
	// inlineApproveForMeUnavailableMessage is the shared copy of both guardian
	// gates (permissions_menu.rs:62-66, :96-102).
	inlineApproveForMeUnavailableMessage = "Approve for me is unavailable. Run `/permissions` to see available profiles."
	// inlineDisabledByRequirementsReason mirrors PermissionDiscovery::disabled_reason's
	// reason string (permission_discovery.rs:76); the caller wraps it as
	// "Permission profile `<id>` is unavailable: Disabled by requirements."
	inlineDisabledByRequirementsReason = "Disabled by requirements"
)

// inlinePermissionProfile is the discovery view of one profile
// (PermissionDiscovery::profiles).
type inlinePermissionProfile struct {
	id          string
	description string
	allowed     bool
}

// InlinePermissionProfileSelection resolves a `/permissions` argument string's
// profile request into the popup row the picker would have produced, mirroring
// ChatWidget::try_select_permission_profile (codex-rs/tui/src/chatwidget/
// permissions_menu.rs:53-146):
//
//   - `:approve-for-me` requires the guardian-approval feature and selects the
//     automatic reviewer, while `:ask-for-approval` and `:approve-for-me` both
//     resolve to the workspace profile;
//   - an explicitly requested `--approval-policy` / `--reviewer` wins over the
//     built-in preset's own values;
//   - automatic review is rejected again after the overrides are applied when
//     the feature is unavailable;
//   - a profile the server does not offer, or one its requirements disable,
//     is rejected before anything changes.
//
// The returned string is the Rust error message, empty on success.
func InlinePermissionProfileSelection(config PermissionMenuConfig, profileID string, requestedApprovalPolicy *ApprovalPolicy, requestedReviewer *ApprovalsReviewer) (PermissionMenuItem, string) {
	reviewer := ApprovalsReviewerUser
	if profileID == InlinePermissionProfileApproveForMeAlias {
		if !config.GuardianApprovalEnabled {
			return PermissionMenuItem{}, inlineApproveForMeUnavailableMessage
		}
		reviewer = ApprovalsReviewerAutoReview
	}
	switch profileID {
	case InlinePermissionProfileAskForApprovalAlias, InlinePermissionProfileApproveForMeAlias:
		profileID = WorkspaceProfile
	}
	profile, preset, ok := inlinePermissionProfileCatalogEntry(config, profileID)
	if !ok {
		return PermissionMenuItem{}, "Unknown permission profile `" + profileID + "`. Run `/permissions` to see available profiles."
	}
	approvalPolicy := requestedApprovalPolicy
	if approvalPolicy == nil && preset != nil {
		value := preset.Approval
		approvalPolicy = &value
	}
	approvalsReviewer := requestedReviewer
	if approvalsReviewer == nil && preset != nil {
		value := reviewer
		approvalsReviewer = &value
	}
	if approvalsReviewer != nil && *approvalsReviewer == ApprovalsReviewerAutoReview && !config.GuardianApprovalEnabled {
		return PermissionMenuItem{}, inlineApproveForMeUnavailableMessage
	}
	if reason := inlinePermissionDisabledReason(config, profile, approvalPolicy, approvalsReviewer); reason != "" {
		return PermissionMenuItem{}, "Permission profile `" + profileID + "` is unavailable: " + reason + "."
	}
	item := PermissionMenuItem{
		ID:              profile.id,
		Name:            profile.id,
		Description:     profile.description,
		DismissOnSelect: true,
		ApprovalPolicy:  approvalPolicy,
		Reviewer:        approvalsReviewer,
		ProfileID:       profile.id,
	}
	if preset == nil {
		return item, ""
	}
	item.ID = preset.ID + ":" + string(reviewer)
	item.Name = preset.Label
	switch {
	case preset.ID == inlinePermissionProfileAutoPresetID && approvalsReviewer != nil && *approvalsReviewer == ApprovalsReviewerAutoReview:
		item.Name = ApproveForMeLabel
	case preset.ID == inlinePermissionProfileAutoPresetID:
		item.Name = AskForApprovalLabel
	}
	item.Description = inlinePermissionPresetDescription(*preset)
	item.RequiresConfirmation = preset.ID == inlinePermissionProfileFullAccessPresetID
	if approvalPolicy != nil && approvalsReviewer != nil {
		item.Current = PermissionPresetMatchesCurrent(config, profile.id, *approvalPolicy, *approvalsReviewer)
	}
	return item, ""
}

// inlinePermissionProfileCatalogEntry finds the profile in the discovery catalog
// (built-in presets followed by the server's custom profiles) together with the
// built-in preset that owns the profile, if any
// (PermissionDiscovery::local + permissions_menu.rs:75-87).
func inlinePermissionProfileCatalogEntry(config PermissionMenuConfig, profileID string) (inlinePermissionProfile, *ApprovalPreset, bool) {
	for _, preset := range BuiltinApprovalPresets() {
		if preset.ProfileID != profileID {
			continue
		}
		value := preset
		return inlinePermissionProfile{
			id:          preset.ProfileID,
			description: inlinePermissionPresetDescription(preset),
			allowed:     true,
		}, &value, true
	}
	for _, profile := range config.CustomProfiles {
		if profile.ID != profileID {
			continue
		}
		return inlinePermissionProfile{
			id:          profile.ID,
			description: firstNonEmptyPermission(profile.Description, "Configured permission profile."),
			allowed:     profile.Allowed,
		}, nil, true
	}
	return inlinePermissionProfile{}, nil, false
}

// inlinePermissionPresetDescription mirrors the preset copy the picker shows.
func inlinePermissionPresetDescription(preset ApprovalPreset) string {
	return strings.ReplaceAll(preset.Description, " (Identical to Agent mode)", "")
}

// inlinePermissionDisabledReason mirrors PermissionDiscovery::disabled_reason
// (permission_discovery.rs:55-79): only the values the request actually carries
// are checked against the requirements.
func inlinePermissionDisabledReason(config PermissionMenuConfig, profile inlinePermissionProfile, approvalPolicy *ApprovalPolicy, reviewer *ApprovalsReviewer) string {
	if !profile.allowed {
		return inlineDisabledByRequirementsReason
	}
	requirements := config.Requirements
	if len(requirements.AllowedProfiles) > 0 && !permissionProfileAllowed(requirements.AllowedProfiles, profile.id) {
		return inlineDisabledByRequirementsReason
	}
	if approvalPolicy != nil && len(requirements.AllowedApprovalPolicies) > 0 && !approvalPolicyAllowed(requirements.AllowedApprovalPolicies, *approvalPolicy) {
		return inlineDisabledByRequirementsReason
	}
	if reviewer != nil && len(requirements.AllowedReviewers) > 0 && !reviewerAllowed(requirements.AllowedReviewers, *reviewer) {
		return inlineDisabledByRequirementsReason
	}
	return ""
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
