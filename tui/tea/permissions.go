package tea

import (
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	chatwidget "codex_go/tui/chatwidget"
	historycell "codex_go/tui/history_cell"
)

const fullAccessConfirmationPrefix = "full-access-confirm:"

func (m *Model) openPermissionsMenu() {
	if m == nil {
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
	if item.ApprovalPolicy != nil {
		m.State.ApprovalPolicy = string(*item.ApprovalPolicy)
	}
	if item.Reviewer != nil {
		m.approvalsReviewer = *item.Reviewer
	}
	if strings.TrimSpace(item.ProfileID) != "" {
		m.State.Sandbox = strings.TrimSpace(item.ProfileID)
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
