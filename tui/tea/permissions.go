package tea

import (
	"errors"
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/features"
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

// selectServerPermissionProfile asks the server to adopt a permission profile
// and tracks the selection until the server confirms it (Rust #43340/#46008).
// Built-in presets use the same path so the server's confirmed settings remain
// authoritative, and they may be selected while a turn is running.
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
	profileID := strings.TrimSpace(item.ProfileID)
	if profileID == "" {
		return nil
	}
	builtin := strings.HasPrefix(profileID, ":")
	if !builtin && m.isTaskRunning() {
		m.notice = "Wait for the current turn to finish before changing permissions."
		m.refreshTranscript()
		return nil
	}
	selection := permissionSelectionFromItem(item)
	if builtin && m.permissionSelectionMatches(selection) {
		// Rust #46008: an unchanged selection may not emit a settings
		// notification, so adopt it locally instead of waiting for one.
		m.notice = ""
		m.refreshTranscript()
		return nil
	}
	if err := m.onUpdateThreadPermissions(threadID, selection); err != nil {
		if errors.Is(err, ErrNamedPermissionProfilesUnsupported) {
			m.notice = "Permission selection requires a newer app server."
		} else {
			m.notice = "Failed to select permissions: " + strings.TrimSpace(err.Error())
		}
		m.refreshTranscript()
		return nil
	}
	m.pendingServerProfile = profileID
	label := strings.TrimSpace(selection.DisplayLabel)
	if label == "" {
		label = profileID
	}
	m.notice = "Permission selection requested: " + label
	m.refreshTranscript()
	return nil
}

// permissionSelectionFromItem converts a picker row into the app-server
// selection payload.
func permissionSelectionFromItem(item chatwidget.PermissionMenuItem) PermissionSelection {
	selection := PermissionSelection{
		ProfileID:    strings.TrimSpace(item.ProfileID),
		DisplayLabel: strings.TrimSpace(item.Name),
	}
	if item.ApprovalPolicy != nil {
		selection.ApprovalPolicy = string(*item.ApprovalPolicy)
	}
	if item.Reviewer != nil {
		selection.ApprovalsReviewer = string(*item.Reviewer)
	}
	return selection
}

// permissionSelectionMatches reports whether a built-in selection already
// matches the active settings (Rust #46008 already_selected).
func (m *Model) permissionSelectionMatches(selection PermissionSelection) bool {
	if m == nil || m.State == nil {
		return false
	}
	if normalizePermissionProfileID(strings.TrimSpace(m.State.Sandbox)) !=
		normalizePermissionProfileID(strings.TrimSpace(selection.ProfileID)) {
		return false
	}
	if policy := strings.TrimSpace(selection.ApprovalPolicy); policy != "" &&
		policy != strings.TrimSpace(m.State.ApprovalPolicy) {
		return false
	}
	if reviewer := strings.TrimSpace(selection.ApprovalsReviewer); reviewer != "" &&
		reviewer != string(currentApprovalsReviewer(m)) {
		return false
	}
	return true
}

// rejectPendingPermissionRootSwitch blocks starting or switching tasks while a
// permission selection awaits the server's confirmation (Rust #46008
// reject_pending_permission_root_switch).
func (m *Model) rejectPendingPermissionRootSwitch(message string) bool {
	if m == nil || strings.TrimSpace(m.pendingServerProfile) == "" {
		return false
	}
	if strings.TrimSpace(message) == "" {
		message = "Wait for permissions to update before switching tasks."
	}
	m.addErrorHistoryMessage(message)
	m.refreshTranscript()
	return true
}

// normalizePermissionProfileID maps legacy sandbox-policy spellings onto the
// built-in profile ids used by the permission picker.
func normalizePermissionProfileID(value string) string {
	switch value {
	case "read-only", ":read-only":
		return chatwidget.ReadOnlyProfile
	case "workspace", "workspace-write", ":workspace", ":workspace-write":
		return chatwidget.WorkspaceProfile
	case "danger-full-access", ":danger-full-access":
		return chatwidget.DangerFullAccessProfile
	default:
		return value
	}
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
// permissionMenuConfig builds the picker configuration the popup and an inline
// `/permissions` request both resolve against (Rust ChatWidget::
// open_permission_profiles_popup plus the Feature::GuardianApproval gate of
// try_select_permission_profile).
func (m *Model) permissionMenuConfig(explicit bool) chatwidget.PermissionMenuConfig {
	config := chatwidget.PermissionMenuConfig{
		IncludeReadOnly:       true,
		HideFullAccessWarning: m.hideFullAccessWarning,
		CurrentApprovalPolicy: currentPermissionApprovalPolicy(m),
		CurrentReviewer:       currentApprovalsReviewer(m),
		CurrentProfileID:      strings.TrimSpace(m.State.Sandbox),
		Requirements:          m.permissionRequirements,
		// Rust #45821: the app-server-owned sandbox level decides whether the
		// permission menu advertises the non-admin degraded sandbox.
		WindowsDegradedSandbox: m.windowsSandboxLevel == chatwidget.WindowsSandboxLevelUnelevated,
		// Rust #54383 gates the approve-for-me row and the automatic reviewer
		// on Feature::GuardianApproval (chatwidget/permissions_menu.rs:61-68).
		GuardianApprovalEnabled: guardianApprovalEnabled(m),
	}
	if explicit {
		// Rust #43340: the connected server owns named profiles, so the picker
		// lists its discovery result instead of local presets only.
		config.ExplicitPermissionProfileMode = true
		config.CustomProfiles = append([]chatwidget.CustomPermissionProfile(nil), m.permissionProfiles...)
	}
	return config
}

// guardianApprovalEnabled reports whether the guardian-approval feature is on
// (Rust Feature::GuardianApproval).
func guardianApprovalEnabled(m *Model) bool {
	if m == nil {
		return false
	}
	return features.Enabled(m.featureSettings, experimentalFeatureGuardianApprovalKey)
}

// applyPermissionsCommand handles `/permissions` (Rust #54383: SlashCommand::Permissions
// supports inline arguments): no arguments open the picker, arguments select a
// profile inline through the same validation the picker applies.
func (m *Model) applyPermissionsCommand(args string) bubbletea.Cmd {
	if m == nil || m.State == nil {
		return nil
	}
	value := strings.TrimSpace(args)
	if value == "" {
		return m.openPermissionsMenu()
	}
	return m.selectPermissionProfileInline(value)
}

// selectPermissionProfileInline validates an inline `/permissions` request and
// applies it the way the picker would (Rust select_permission_profile,
// apply_pending_permission_profile and try_select_permission_profile): the
// connected server's catalog is discovered first when the request needs it, the
// full-access row still opens its confirmation, and a rejected request only
// reports the Rust reason.
func (m *Model) selectPermissionProfileInline(args string) bubbletea.Cmd {
	if m == nil || m.State == nil {
		return nil
	}
	if m.onListPermissionProfiles != nil && !m.permissionProfilesDiscovered && !m.permissionProfilesLoading {
		// Rust InlinePermissionProfileSelection::Discovering: remember the
		// request while the catalog loads, then apply it
		// (permission_discovery.rs:70-77).
		m.pendingPermissionProfileArgs = args
		m.permissionProfilesLoading = true
		m.showPermissionsLoadingView()
		return m.fetchPermissionProfilesCmd()
	}
	m.pendingPermissionProfileArgs = ""
	parsed, failure := parsePermissionProfileArgs(args)
	item := chatwidget.PermissionMenuItem{}
	if failure == "" {
		item, failure = m.resolveInlinePermissionProfile(parsed)
	}
	if failure != "" {
		m.closePermissionsModal()
		m.notice = failure
		m.refreshTranscript()
		return nil
	}
	m.closePermissionsModal()
	if item.RequiresConfirmation {
		// Rust #54385 keeps the full-access confirmation for every reviewer.
		pending := item
		m.pendingPermissionItem = &pending
		m.pendingPermissionSessionOnly = false
		m.openFullAccessConfirmation()
		return nil
	}
	return m.applyPermissionSelection(item)
}

// resolveInlinePermissionProfile maps the parsed request onto the picker row
// Rust's try_select_permission_profile would have produced.
func (m *Model) resolveInlinePermissionProfile(parsed permissionProfileArgs) (chatwidget.PermissionMenuItem, string) {
	var requestedApprovalPolicy *chatwidget.ApprovalPolicy
	if parsed.hasApprovalPolicy {
		value := chatwidget.ApprovalPolicy(parsed.approvalPolicy)
		requestedApprovalPolicy = &value
	}
	var requestedReviewer *chatwidget.ApprovalsReviewer
	if parsed.hasReviewer {
		value := parsed.reviewer
		requestedReviewer = &value
	}
	return chatwidget.InlinePermissionProfileSelection(m.permissionMenuConfig(true), parsed.profileID, requestedApprovalPolicy, requestedReviewer)
}

// closePermissionsModal dismisses the permissions loading view or picker when it
// is the active modal (Rust AppEvent::SettingsSelectionClosed).
func (m *Model) closePermissionsModal() {
	if m == nil || m.modal == nil || m.modal.kind != ModalKindPermissions {
		return
	}
	m.modal = nil
}

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
	view := chatwidget.NewPermissionsPopupView(m.permissionMenuConfig(explicit))
	m.permissionItems = append([]chatwidget.PermissionMenuItem(nil), view.Items...)
	m.pendingPermissionItem = nil
	m.pendingPermissionSessionOnly = false
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
func (m *Model) applyPermissionProfilesLoaded(msg permissionProfilesLoadedMsg) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	m.permissionProfilesLoading = false
	if msg.err != nil {
		// A failed discovery leaves the picker undecided so Retry re-runs it
		// (Rust open_permissions_popup's gate is unchanged by a failure).
		m.permissionProfilesErr = strings.TrimSpace(msg.err.Error())
		m.openPermissionDiscoveryRetryView(m.permissionProfilesErr)
		return nil
	}
	m.permissionProfilesDiscovered = true
	m.permissionProfilesErr = ""
	m.permissionProfilesExplicit = msg.explicitProfileMode
	m.permissionProfiles = append([]chatwidget.CustomPermissionProfile(nil), msg.profiles...)
	if args := m.pendingPermissionProfileArgs; args != "" {
		// Rust apply_pending_permission_profile: an inline `/permissions`
		// request that had to wait for the catalog is applied now.
		m.pendingPermissionProfileArgs = ""
		return m.selectPermissionProfileInline(args)
	}
	if m.modal == nil || m.modal.kind != ModalKindPermissions {
		return nil
	}
	m.showPermissionsMenu(msg.explicitProfileMode)
	return nil
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
		// Rust #43340: Retry on the failed-discovery view re-runs the flow; an
		// inline request is retried with its own arguments (Rust
		// AppEvent::ResolvePermissionProfile).
		if m.pendingPermissionProfileArgs != "" {
			return m.selectPermissionProfileInline(m.pendingPermissionProfileArgs)
		}
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
		m.pendingPermissionSessionOnly = false
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
		m.pendingPermissionSessionOnly = false
		m.notice = "Cancelled"
		m.refreshTranscript()
		return nil
	}
	item := *m.pendingPermissionItem
	sessionOnly := m.pendingPermissionSessionOnly
	m.pendingPermissionItem = nil
	m.pendingPermissionSessionOnly = false
	return m.applyPermissionSelectionWithPersistence(item, !sessionOnly)
}

func (m *Model) applyPermissionSelection(item chatwidget.PermissionMenuItem) bubbletea.Cmd {
	return m.applyPermissionSelectionWithPersistence(item, true)
}

func (m *Model) applyPermissionSelectionWithPersistence(item chatwidget.PermissionMenuItem, persistReviewer bool) bubbletea.Cmd {
	if m == nil || m.State == nil {
		return nil
	}
	profileID := strings.TrimSpace(item.ProfileID)
	// Rust #43340/#46008: a connected thread's profile selection, including a
	// built-in preset, goes through thread/settings/update so the server's
	// confirmed settings stay authoritative.
	if profileID != "" && m.onUpdateThreadPermissions != nil {
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
	if persistReviewer && item.Reviewer != nil && m.onWriteSettings != nil {
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

// applyPermissionShortcutKey cycles the built-in permission modes without
// writing config. Full access still uses the normal warning confirmation.
func (m *Model) applyPermissionShortcutKey(keySpec string) (bubbletea.Cmd, bool) {
	if m == nil || m.State == nil || m.readOnlyThread || m.modal != nil || m.overlay != nil ||
		m.slashPopup.Active || m.skillPopup.Active {
		return nil, false
	}
	forward := false
	switch {
	case m.keyMatches("chat", "previous_permission_mode", keySpec):
	case m.keyMatches("chat", "next_permission_mode", keySpec):
		forward = true
	default:
		return nil, false
	}
	view := chatwidget.NewPermissionsPopupView(m.permissionMenuConfig(false))
	available := make([]chatwidget.PermissionMenuItem, 0, len(view.Items))
	for _, item := range view.Items {
		if strings.TrimSpace(item.DisabledReason) == "" {
			available = append(available, item)
		}
	}
	if len(available) <= 1 {
		m.applyHistoryCell(historycell.NewInfoEvent("No other permission modes are available.", ""))
		m.notice = "Permissions"
		m.refreshTranscript()
		return nil, true
	}
	current := 0
	for index, item := range available {
		if item.Current || (strings.TrimSpace(item.ProfileID) != "" &&
			strings.TrimSpace(item.ProfileID) == strings.TrimSpace(m.State.Sandbox)) {
			current = index
			break
		}
	}
	next := current + 1
	if !forward {
		next = current - 1
	}
	if next < 0 {
		next = len(available) - 1
	}
	if next >= len(available) {
		next = 0
	}
	item := available[next]
	if item.RequiresConfirmation {
		pending := item
		m.pendingPermissionItem = &pending
		m.pendingPermissionSessionOnly = true
		m.openFullAccessConfirmation()
		return nil, true
	}
	return m.applyPermissionSelectionWithPersistence(item, false), true
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
