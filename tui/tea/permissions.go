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
	err      error
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

func (m *Model) openPermissionsMenu() bubbletea.Cmd {
	if m == nil {
		return nil
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
	if m.onListPermissionProfiles != nil {
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
	if m.onListPermissionProfiles == nil || m.permissionProfilesLoading {
		return nil
	}
	m.permissionProfilesLoading = true
	return m.fetchPermissionProfilesCmd()
}

// fetchPermissionProfilesCmd loads the server's named permission profiles.
func (m *Model) fetchPermissionProfilesCmd() bubbletea.Cmd {
	if m == nil || m.onListPermissionProfiles == nil {
		return nil
	}
	loader := m.onListPermissionProfiles
	return func() bubbletea.Msg {
		profiles, err := loader()
		return permissionProfilesLoadedMsg{profiles: profiles, err: err}
	}
}

// applyPermissionProfilesLoaded stores a discovery result and refreshes the
// open permissions menu.
func (m *Model) applyPermissionProfilesLoaded(msg permissionProfilesLoadedMsg) {
	if m == nil {
		return
	}
	m.permissionProfilesLoading = false
	if msg.err != nil {
		m.permissionProfilesErr = "Failed to load permissions: " + strings.TrimSpace(msg.err.Error())
		if errors.Is(msg.err, ErrNamedPermissionProfilesUnsupported) {
			m.permissionProfilesErr = "This server does not support permission discovery. Upgrade the Codex server to use this menu."
		}
		m.notice = m.permissionProfilesErr
		m.refreshTranscript()
		return
	}
	m.permissionProfilesErr = ""
	m.permissionProfiles = append([]chatwidget.CustomPermissionProfile(nil), msg.profiles...)
	if m.modal == nil || m.modal.kind != ModalKindPermissions {
		return
	}
	// Rebuild the open menu with the discovered profiles.
	view := chatwidget.NewPermissionsPopupView(chatwidget.PermissionMenuConfig{
		ExplicitPermissionProfileMode: true,
		IncludeReadOnly:               true,
		HideFullAccessWarning:         m.hideFullAccessWarning,
		CurrentApprovalPolicy:         currentPermissionApprovalPolicy(m),
		CurrentReviewer:               currentApprovalsReviewer(m),
		CurrentProfileID:              strings.TrimSpace(m.State.Sandbox),
		Requirements:                  m.permissionRequirements,
		CustomProfiles:                append([]chatwidget.CustomPermissionProfile(nil), m.permissionProfiles...),
	})
	m.permissionItems = append([]chatwidget.PermissionMenuItem(nil), view.Items...)
	m.modal.title = view.Title
	m.modal.body = strings.TrimSpace(view.FooterNote)
	m.modal.options = permissionModalOptions(view.Items)
	if m.modal.selected >= len(m.modal.options) {
		m.modal.selected = 0
	}
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
