package tea

import (
	"errors"
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	chatwidget "codex_go/tui/chatwidget"
	historycell "codex_go/tui/history_cell"
)

const fullAccessConfirmationPrefix = "full-access-confirm:"

// ErrNamedPermissionProfilesUnsupported reports that the connected app server
// cannot select named permission profiles (Rust #43340: an older server).
var ErrNamedPermissionProfilesUnsupported = errors.New("named permission profiles are not supported by this app server")

// permissionProfilesLoadedMsg carries the connected server's named permission
// profiles (Rust #43340 permission discovery).
type permissionProfilesLoadedMsg struct {
	profiles []chatwidget.CustomPermissionProfile
	// explicitProfileMode is false when the server has no configured permission
	// profiles, so the picker keeps its local built-in presets (Rust #43340
	// PermissionDiscovery::explicit_profile_mode).
	explicitProfileMode bool
	err                 error
}

// selectServerPermissionProfile asks the server to adopt a named profile and
// tracks the selection until the server confirms it (Rust #43340).
func (m *Model) selectServerPermissionProfile(item chatwidget.PermissionMenuItem) bubbletea.Cmd {
	if m == nil || m.State == nil {
		return nil
	}
	if m.pendingServerProfile != "" {
		m.notice = "Wait for permissions to update before changing permissions."
		m.refreshTranscript()
		return nil
	}
	threadID := strings.TrimSpace(m.State.ThreadID)
	if threadID == "" {
		m.notice = "Wait for the task to connect before selecting permissions."
		m.refreshTranscript()
		return nil
	}
	if m.isTaskRunning() {
		m.notice = "Wait for the current turn to finish before changing permissions."
		m.refreshTranscript()
		return nil
	}
	profileID := strings.TrimSpace(item.ProfileID)
	if err := m.onUpdateThreadPermissions(threadID, profileID); err != nil {
		if errors.Is(err, ErrNamedPermissionProfilesUnsupported) {
			m.notice = "Named profiles require a newer app server."
		} else {
			m.notice = "Failed to select permissions: " + strings.TrimSpace(err.Error())
		}
		m.refreshTranscript()
		return nil
	}
	m.pendingServerProfile = profileID
	m.notice = "Permission selection requested: " + profileID
	m.refreshTranscript()
	return nil
}

// remoteNamedPermissionProfileActive reports whether a non-built-in profile
// selected on the connected server is active (Rust #43340).
func (m *Model) remoteNamedPermissionProfileActive() bool {
	if m == nil || m.State == nil || m.onUpdateThreadPermissions == nil {
		return false
	}
	profileID := strings.TrimSpace(m.State.Sandbox)
	return profileID != "" && !strings.HasPrefix(profileID, ":")
}

// permissionsRetryOptionID is the retry item of the failed-discovery view
// (Rust #43340 shows a "Update Model Permissions" retry view on error).
const permissionsRetryOptionID = "permissions-retry"

// openPermissionsMenu mirrors Rust open_permissions_popup: the server-named flow
// (loading view plus bounded discovery) is used once the picker knows the server
// uses explicit profiles, has already listed them, or the thread has an active
// named profile; otherwise the local built-in presets open immediately.
func (m *Model) openPermissionsMenu() bubbletea.Cmd {
	if m == nil || m.State == nil {
		return nil
	}
	if m.onListPermissionProfiles == nil {
		m.showPermissionsMenu(false)
		return nil
	}
	if !m.permissionProfilesExplicit && len(m.permissionProfiles) == 0 &&
		!m.remoteNamedPermissionProfileActive() && m.permissionProfilesDiscovered {
		m.showPermissionsMenu(false)
		return nil
	}
	m.showPermissionsLoadingView()
	if m.permissionProfilesLoading {
		return nil
	}
	m.permissionProfilesLoading = true
	return m.fetchPermissionProfilesCmd()
}

// showPermissionsMenu renders the picker in named-profile (explicit) or legacy
// local-preset mode (Rust open_permission_profiles_popup /
// open_legacy_permissions_popup).
func (m *Model) showPermissionsMenu(explicit bool) {
	if m == nil || m.State == nil {
		return
	}
	config := chatwidget.PermissionMenuConfig{
		IncludeReadOnly:        true,
		HideFullAccessWarning:  m.hideFullAccessWarning,
		CurrentApprovalPolicy:  currentPermissionApprovalPolicy(m),
		CurrentReviewer:        currentApprovalsReviewer(m),
		CurrentProfileID:       strings.TrimSpace(m.State.Sandbox),
		Requirements:           m.permissionRequirements,
		WindowsDegradedSandbox: false,
	}
	if explicit {
		// Rust #43340: the connected server owns named profiles, so the picker
		// lists its discovery result instead of local presets only.
		config.ExplicitPermissionProfileMode = true
		config.CustomProfiles = append([]chatwidget.CustomPermissionProfile(nil), m.permissionProfiles...)
	}
	view := chatwidget.NewPermissionsPopupView(config)
	m.permissionItems = append([]chatwidget.PermissionMenuItem(nil), view.Items...)
	m.pendingPermissionItem = nil
	m.openModal(ModalRequestMsg{
		ID:      "permissions",
		Kind:    ModalKindPermissions,
		Title:   view.Title,
		Body:    strings.TrimSpace(view.FooterNote),
		Options: permissionModalOptions(view.Items),
	})
}

// showPermissionsLoadingView shows Rust's discovery loading view.
func (m *Model) showPermissionsLoadingView() {
	if m == nil {
		return
	}
	m.permissionItems = nil
	m.pendingPermissionItem = nil
	m.openModal(ModalRequestMsg{
		ID:    "permissions",
		Kind:  ModalKindPermissions,
		Title: "Update Model Permissions",
		Options: []ModalOption{{
			ID:       "permissions-loading",
			Label:    "Loading permission profiles\u2026",
			Disabled: true,
		}},
	})
}

// fetchPermissionProfilesCmd loads the server's named permission profiles.
func (m *Model) fetchPermissionProfilesCmd() bubbletea.Cmd {
	if m == nil || m.onListPermissionProfiles == nil {
		return nil
	}
	loader := m.onListPermissionProfiles
	return func() bubbletea.Msg {
		profiles, explicitProfileMode, err := loader()
		return permissionProfilesLoadedMsg{profiles: profiles, explicitProfileMode: explicitProfileMode, err: err}
	}
}

// applyPermissionProfilesLoaded stores a discovery result and shows the popup
// Rust selects for it: the retry view on failure, the legacy preset popup when
// the server reports no explicit profiles, and the named-profile popup
// otherwise (Rust on_permission_profiles_loaded).
func (m *Model) applyPermissionProfilesLoaded(msg permissionProfilesLoadedMsg) {
	if m == nil {
		return
	}
	m.permissionProfilesLoading = false
	if msg.err != nil {
		// A failed discovery leaves the picker undecided so Retry re-runs it
		// (Rust open_permissions_popup's gate is unchanged by a failure).
		m.permissionProfilesErr = strings.TrimSpace(msg.err.Error())
		m.openPermissionDiscoveryRetryView(m.permissionProfilesErr)
		return
	}
	m.permissionProfilesDiscovered = true
	m.permissionProfilesErr = ""
	m.permissionProfilesExplicit = msg.explicitProfileMode
	m.permissionProfiles = append([]chatwidget.CustomPermissionProfile(nil), msg.profiles...)
	if m.modal == nil || m.modal.kind != ModalKindPermissions {
		return
	}
	m.showPermissionsMenu(msg.explicitProfileMode)
}

// openPermissionDiscoveryRetryView shows Rust's failed-discovery view: the raw
// error as the subtitle and a single Retry item.
func (m *Model) openPermissionDiscoveryRetryView(message string) {
	if m == nil {
		return
	}
	m.permissionItems = nil
	m.pendingPermissionItem = nil
	m.openModal(ModalRequestMsg{
		ID:    "permissions",
		Kind:  ModalKindPermissions,
		Title: "Update Model Permissions",
		Body:  strings.TrimSpace(message),
		Options: []ModalOption{{
			ID:          permissionsRetryOptionID,
			Label:       "Retry",
			Description: "Retry permission discovery",
		}},
	})
}

func permissionModalOptions(items []chatwidget.PermissionMenuItem) []ModalOption {
	options := make([]ModalOption, 0, len(items))
	for _, item := range items {
		description := strings.TrimSpace(item.Description)
		if item.Current {
			if description != "" {
				description += " "
			}
			description += "(current)"
		}
		if strings.TrimSpace(item.DisabledReason) != "" {
			if description != "" {
				description += " "
			}
			description += item.DisabledReason
		}
		options = append(options, ModalOption{
			ID:          item.ID,
			Label:       item.Name,
			Description: description,
			Disabled:    strings.TrimSpace(item.DisabledReason) != "",
		})
	}
	return options
}

func (m *Model) applyPermissionsModalOption(optionID string) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	if optionID == permissionsRetryOptionID {
		// Rust #43340: Retry on the failed-discovery view re-runs the flow.
		return m.openPermissionsMenu()
	}
	if strings.HasPrefix(optionID, fullAccessConfirmationPrefix) {
		return m.applyFullAccessConfirmation(strings.TrimPrefix(optionID, fullAccessConfirmationPrefix))
	}
	item, ok := m.permissionItemByID(optionID)
	if !ok {
		m.notice = "Permissions"
		m.refreshTranscript()
		return nil
	}
	if item.RequiresConfirmation {
		m.pendingPermissionItem = &item
		m.openFullAccessConfirmation()
		return nil
	}
	return m.applyPermissionSelection(item)
}

func (m *Model) openFullAccessConfirmation() {
	view := chatwidget.FullAccessConfirmationView()
	options := make([]ModalOption, 0, len(view.Items))
	for _, item := range view.Items {
		options = append(options, ModalOption{
			ID:          fullAccessConfirmationPrefix + item.ID,
			Label:       item.Name,
			Description: item.Description,
		})
	}
	m.openModal(ModalRequestMsg{
		ID:      "permissions-full-access",
		Kind:    ModalKindPermissions,
		Title:   "Enable full access?",
		Body:    "When Codex runs with full access, it can edit any file on your computer and run commands with network, without your approval. Exercise caution when enabling full access. This significantly increases the risk of data loss, leaks, or unexpected behavior.",
		Options: options,
	})
}

func (m *Model) applyFullAccessConfirmation(optionID string) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	if optionID == "cancel" || m.pendingPermissionItem == nil {
		m.pendingPermissionItem = nil
		m.notice = "Cancelled"
		m.refreshTranscript()
		return nil
	}
	item := *m.pendingPermissionItem
	m.pendingPermissionItem = nil
	return m.applyPermissionSelection(item)
}

func (m *Model) applyPermissionSelection(item chatwidget.PermissionMenuItem) bubbletea.Cmd {
	if m == nil || m.State == nil {
		return nil
	}
	profileID := strings.TrimSpace(item.ProfileID)
	// Rust #43340: a named (non-built-in) profile is owned by the connected app
	// server and must be selected through thread/settings/update.
	if profileID != "" && !strings.HasPrefix(profileID, ":") && m.onUpdateThreadPermissions != nil {
		return m.selectServerPermissionProfile(item)
	}
	if item.ApprovalPolicy != nil {
		m.State.ApprovalPolicy = string(*item.ApprovalPolicy)
	}
	if item.Reviewer != nil {
		m.approvalsReviewer = *item.Reviewer
	}
	if profileID != "" {
		m.State.Sandbox = profileID
	}
	m.notice = ""
	m.applyHistoryCell(historycell.NewInfoEvent("Permissions updated to "+strings.TrimSpace(item.Name), ""))
	// Rust #46036: the chosen reviewer is persisted through the app server's
	// config/batchWrite so it survives the session, and a failed save keeps the
	// backend's cause.
	var reviewerCmd bubbletea.Cmd
	if item.Reviewer != nil && m.onWriteSettings != nil {
		reviewerCmd = m.writeSettings(settingsWriteKindApprovalsReviewer, []SettingsEdit{{
			KeyPath: "approvals_reviewer",
			Value:   string(*item.Reviewer),
		}})
	}
	if reviewerCmd != nil {
		return bubbletea.Batch(reviewerCmd, m.refreshStatusControlsCmd())
	}
	return m.refreshStatusControlsCmd()
}

func (m *Model) permissionItemByID(id string) (chatwidget.PermissionMenuItem, bool) {
	id = strings.TrimSpace(id)
	for _, item := range m.permissionItems {
		if item.ID == id {
			return item, true
		}
	}
	return chatwidget.PermissionMenuItem{}, false
}

func currentPermissionApprovalPolicy(m *Model) chatwidget.ApprovalPolicy {
	if m == nil || m.State == nil {
		return chatwidget.ApprovalOnRequest
	}
	if strings.TrimSpace(m.State.ApprovalPolicy) == string(chatwidget.ApprovalNever) {
		return chatwidget.ApprovalNever
	}
	return chatwidget.ApprovalOnRequest
}

func currentApprovalsReviewer(m *Model) chatwidget.ApprovalsReviewer {
	if m == nil || m.approvalsReviewer == "" {
		return chatwidget.ApprovalsReviewerUser
	}
	return m.approvalsReviewer
}

func clonePermissionRequirementsTea(requirements *chatwidget.PermissionRequirements) chatwidget.PermissionRequirements {
	if requirements == nil {
		return chatwidget.PermissionRequirements{}
	}
	clone := chatwidget.PermissionRequirements{
		AllowedApprovalPolicies:    append([]chatwidget.ApprovalPolicy(nil), requirements.AllowedApprovalPolicies...),
		AllowedReviewers:           append([]chatwidget.ApprovalsReviewer(nil), requirements.AllowedReviewers...),
		AllowedWindowsSandboxModes: append([]chatwidget.WindowsSandboxMode(nil), requirements.AllowedWindowsSandboxModes...),
	}
	if requirements.AllowedProfiles != nil {
		clone.AllowedProfiles = make(map[string]bool, len(requirements.AllowedProfiles))
		for key, value := range requirements.AllowedProfiles {
			clone.AllowedProfiles[key] = value
		}
	}
	return clone
}
