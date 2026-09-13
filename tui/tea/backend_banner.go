package tea

import (
	"slices"
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	"codex_go/tui/chatwidget"
)

// Rust parity: codex-rs/tui/src/backend_banners.rs,
// codex-rs/tui/src/bottom_pane/actionable_banner.rs, and
// codex-rs/tui/src/chatwidget/backend_banners.rs. The app parses and validates
// the backend-owned payload and resolves each CTA; the model owns the banner
// occurrence state machine, visibility rules, and dismissal.

const (
	// BackendBannerLunaReserve mirrors Rust's LUNA_RESERVE_BANNER.
	BackendBannerLunaReserve = "luna_reserve"
	// LunaReserveModel mirrors Rust's model_catalog::LUNA_RESERVE_MODEL.
	LunaReserveModel = "gpt-reserve"
)

// BackendBannerActionKind mirrors Rust's BannerAction.
type BackendBannerActionKind string

const (
	BannerActionOpenURL     BackendBannerActionKind = "open_url"
	BannerActionNotifyOwner BackendBannerActionKind = "notify_owner"
	BannerActionResetUsage  BackendBannerActionKind = "reset_usage"
	// BannerActionContinueReserve is the model-local "Continue with Luna
	// Reserve" choice Rust adds to the focused Reserve picker while the current
	// model is Reserve; it dismisses the notice without a backend CTA.
	BannerActionContinueReserve BackendBannerActionKind = "continue_reserve"
)

// BackendBannerAction is one resolved CTA: the app resolves the backend action
// name (including the destination URL) while the model owns the reset-credits
// view.
type BackendBannerAction struct {
	Label      string
	Kind       BackendBannerActionKind
	URL        string
	CreditType chatwidget.AddCreditsNudgeCreditType
}

// BackendBannerView is one validated inline banner: copy plus the resolved CTAs
// and the identity fields the visibility rules compare.
type BackendBannerView struct {
	BannerType         string
	Title              string
	Description        string
	Actions            []BackendBannerAction
	Dismissible        bool
	AccountID          string
	ResetAt            *int64
	ModelSlug          *string
	BlockedModelSlug   *string
	FallbackModelSlugs []string
}

// BackendBannerRecoveryInput carries the identity-validated usage facts that
// authorize ordinary-usage recovery (Rust update_backend_banner).
type BackendBannerRecoveryInput struct {
	AccountID            string
	OrdinaryUsageAllowed *bool
	HasCreditsSnapshot   bool
	CreditsUnlimited     bool
	HasCredits           bool
	SpendControlReached  *bool
	RateLimitReachedType string
	HasRateLimitUpsell   bool
}

// BackendBannerRead is one usage read: the parsed banner (nil when absent) plus
// the recovery inputs.
type BackendBannerRead struct {
	Banner   *BackendBannerView
	Recovery BackendBannerRecoveryInput
}

// BackendBannerResultMsg carries the startup/refresh banner read.
type BackendBannerResultMsg struct {
	Read BackendBannerRead
	Err  error
}

// backendBannerState mirrors Rust's BackendBannerState plus the
// luna-reserve notice memory: only explicitly dismissible, previously shown
// occurrences can be dismissed, and an authoritative replacement starts a new
// occurrence.
type backendBannerState struct {
	accountID              string
	ordinaryUsageRecovered bool
	banner                 *BackendBannerView
	// presented is the banner the surface last showed, used for the
	// "occurrence unchanged" early-out and the dismiss-on-new-turn rule.
	presented *BackendBannerView
	// shown records that the banner was presented at least once.
	shown     bool
	dismissed bool
	// reserveNoticeAccountID remembers a shown luna-reserve entry notice across
	// chats until ordinary usage recovers (Rust luna_reserve_notice_account_id).
	reserveNoticeAccountID *string
}

func (s *backendBannerState) clear() {
	*s = backendBannerState{}
}

// update applies one usage read: refresh the account, the ordinary-usage
// recovery decision, and the banner occurrence (Rust update_backend_banner).
func (s *backendBannerState) update(read BackendBannerRead) {
	s.accountID = strings.TrimSpace(read.Recovery.AccountID)
	allowed := read.Recovery.OrdinaryUsageAllowed
	hasUsableCredits := read.Recovery.HasCreditsSnapshot &&
		(read.Recovery.CreditsUnlimited || read.Recovery.HasCredits)
	spendControlReached := read.Recovery.SpendControlReached != nil && *read.Recovery.SpendControlReached
	s.ordinaryUsageRecovered = allowed != nil &&
		(*allowed || hasUsableCredits) &&
		!read.Recovery.HasRateLimitUpsell &&
		!spendControlReached &&
		strings.TrimSpace(read.Recovery.RateLimitReachedType) == ""
	if s.ordinaryUsageRecovered {
		s.reserveNoticeAccountID = nil
	}

	sameOccurrence := s.sameBannerOccurrence(s.banner, read.Banner)
	s.banner = read.Banner
	if !sameOccurrence {
		s.shown = false
		s.dismissed = s.reserveNoticeAlreadyShown()
		s.presented = nil
	}
}

// sameBannerOccurrence mirrors Rust's same_occurrence comparison.
func (s *backendBannerState) sameBannerOccurrence(oldBanner *BackendBannerView, newBanner *BackendBannerView) bool {
	if oldBanner == nil || newBanner == nil {
		return false
	}
	return oldBanner.AccountID == newBanner.AccountID &&
		oldBanner.BannerType == newBanner.BannerType &&
		int64PtrValue(oldBanner.ResetAt) == int64PtrValue(newBanner.ResetAt) &&
		oldBanner.Dismissible == newBanner.Dismissible &&
		firstNonEmptyStringPtr(oldBanner.BlockedModelSlug, oldBanner.ModelSlug) ==
			firstNonEmptyStringPtr(newBanner.BlockedModelSlug, newBanner.ModelSlug) &&
		slices.Equal(oldBanner.FallbackModelSlugs, newBanner.FallbackModelSlugs)
}

// reserveNoticeAlreadyShown reports whether this account already showed the
// reserve entry notice (Rust reserve_notice_already_shown).
func (s *backendBannerState) reserveNoticeAlreadyShown() bool {
	return s.banner != nil &&
		s.banner.BannerType == BackendBannerLunaReserve &&
		s.reserveNoticeAccountID != nil &&
		*s.reserveNoticeAccountID == s.banner.AccountID
}

// visibleBanner applies Rust's refresh_backend_banner_visibility filter.
func (s *backendBannerState) visibleBanner(currentModel string) *BackendBannerView {
	banner := s.banner
	if banner == nil {
		return nil
	}
	if banner.BannerType == BackendBannerLunaReserve {
		// Keep recovery actions available while switching.
		if s.dismissed {
			return nil
		}
		return banner
	}
	if s.dismissed {
		return nil
	}
	matchesSelectedModel := false
	switch {
	case banner.BlockedModelSlug != nil && len(banner.FallbackModelSlugs) > 0:
		// Explicit fallback payloads describe the selected replacement.
		matchesSelectedModel = *banner.BlockedModelSlug != currentModel &&
			slices.Contains(banner.FallbackModelSlugs, currentModel)
	default:
		matchesSelectedModel = banner.ModelSlug == nil ||
			strings.TrimSpace(*banner.ModelSlug) == "" ||
			*banner.ModelSlug == currentModel
	}
	if !matchesSelectedModel {
		return nil
	}
	return banner
}

// present records the banner the surface is about to show: the occurrence was
// presented, and the luna-reserve entry notice is remembered per account.
func (s *backendBannerState) present(banner *BackendBannerView, currentModel string) {
	s.presented = banner
	s.shown = true
	if banner.BannerType == BackendBannerLunaReserve && currentModel == LunaReserveModel {
		accountID := banner.AccountID
		s.reserveNoticeAccountID = &accountID
	}
}

// dismiss hides a dismissible banner (Rust dismiss_backend_banner_for_new_turn).
func (s *backendBannerState) dismiss() {
	s.dismissed = true
}

// AutomaticModelSwitchReason mirrors Rust's AutomaticModelSwitchReason.
type AutomaticModelSwitchReason string

const (
	AutomaticModelSwitchUsageLimit     AutomaticModelSwitchReason = "usage_limit"
	AutomaticModelSwitchUsageRecovered AutomaticModelSwitchReason = "usage_recovered"
)

// ReserveReturn mirrors Rust's ReserveReturnModel: the task-local model to
// restore once ordinary usage recovers.
type ReserveReturn struct {
	AccountID string
	Model     string
	Effort    string
}

// AutomaticModelSwitch is the target of a backend-authorized model transition.
type AutomaticModelSwitch struct {
	Model  string
	Effort string
	Reason AutomaticModelSwitchReason
}

// fallbackSwitch mirrors Rust ChatWidget::backend_banner_fallback: the banner
// authorizes a Reserve entry or an ordered fallback, and a recovered ordinary
// usage read authorizes returning from Reserve.
func (s *backendBannerState) fallbackSwitch(models []codextui.ModelPickerOption, currentModel string, currentEffort string, reserveReturn *ReserveReturn) *AutomaticModelSwitch {
	if s == nil {
		return nil
	}
	if reserveReturn != nil && reserveReturn.AccountID != s.accountID {
		// A saved target from another account is stale.
		reserveReturn = nil
	}
	if currentModel == LunaReserveModel && s.ordinaryUsageRecovered {
		if reserveReturn == nil || reserveReturn.AccountID != s.accountID || strings.TrimSpace(reserveReturn.Model) == "" {
			return nil
		}
		if !catalogModelVisible(models, reserveReturn.Model) {
			return nil
		}
		return &AutomaticModelSwitch{
			Model:  reserveReturn.Model,
			Effort: reserveReturn.Effort,
			Reason: AutomaticModelSwitchUsageRecovered,
		}
	}
	banner := s.banner
	if banner == nil {
		return nil
	}
	if banner.BannerType == BackendBannerLunaReserve {
		for _, option := range models {
			if option.ID == LunaReserveModel && option.ID != currentModel &&
				(banner.BlockedModelSlug == nil || *banner.BlockedModelSlug == currentModel) {
				return &AutomaticModelSwitch{Model: option.ID, Effort: currentEffort, Reason: AutomaticModelSwitchUsageLimit}
			}
		}
		return nil
	}
	if banner.BlockedModelSlug == nil || *banner.BlockedModelSlug != currentModel {
		return nil
	}
	for _, slug := range banner.FallbackModelSlugs {
		for _, option := range models {
			if option.ID == slug && option.ID != currentModel && catalogOptionVisible(models, option) {
				return &AutomaticModelSwitch{Model: option.ID, Effort: currentEffort, Reason: AutomaticModelSwitchUsageLimit}
			}
		}
	}
	return nil
}

func catalogModelVisible(models []codextui.ModelPickerOption, model string) bool {
	for _, option := range models {
		if option.ID == model {
			return catalogOptionVisible(models, option)
		}
	}
	return false
}

// catalogOptionVisible applies Rust's show_in_picker filter. A catalog whose
// entries carry no visibility flag (hand-built picker/tests) is treated as all
// visible.
func catalogOptionVisible(models []codextui.ModelPickerOption, option codextui.ModelPickerOption) bool {
	anyFlagged := false
	for _, candidate := range models {
		if candidate.ShowInPicker {
			anyFlagged = true
			break
		}
	}
	return !anyFlagged || option.ShowInPicker
}

func (m *Model) currentBannerModel() string {
	if m == nil || m.State == nil {
		return ""
	}
	return strings.TrimSpace(m.State.Model)
}

// SetBackendBanner installs (or clears) the banner without recovery inputs,
// starting a new occurrence.
func (m *Model) SetBackendBanner(banner *BackendBannerView) {
	if m == nil {
		return
	}
	m.backendBanner.update(BackendBannerRead{Banner: banner})
}

// BackendBanner returns the banner the surface would show, if any.
func (m *Model) BackendBanner() *BackendBannerView {
	if m == nil {
		return nil
	}
	return m.backendBanner.visibleBanner(m.currentBannerModel())
}

func (m *Model) backendBannerVisible() bool {
	return m != nil && m.backendBanner.visibleBanner(m.currentBannerModel()) != nil
}

// backendBannerApplicable mirrors Rust's has_applicable_backend_banner: a
// non-dismissed banner applies to the selected model, so it owns the
// account-recovery surface and the lower-cost switch prompt defers to it.
func (m *Model) backendBannerApplicable() bool {
	return m.backendBannerVisible()
}

// dismissBackendBannerForNewTurn mirrors Rust's
// dismiss_backend_banner_for_new_turn: once a dismissible banner has been
// presented, starting a new turn hides it.
func (m *Model) dismissBackendBannerForNewTurn() {
	if m == nil {
		return
	}
	state := &m.backendBanner
	if !state.shown || state.presented == nil || !state.presented.Dismissible {
		return
	}
	state.dismiss()
}

// backendBannerCmd performs the startup/refresh banner read.
func (m *Model) backendBannerCmd() bubbletea.Cmd {
	if m == nil || m.onReadBackendBanner == nil {
		return nil
	}
	reader := m.onReadBackendBanner
	return func() bubbletea.Msg {
		read, err := reader()
		return BackendBannerResultMsg{Read: read, Err: err}
	}
}

func (m *Model) applyBackendBannerResult(message BackendBannerResultMsg) bubbletea.Cmd {
	if m == nil || message.Err != nil {
		// A failed read leaves the previous banner in place.
		return nil
	}
	m.backendBanner.update(message.Read)
	m.syncRateLimitRecoveryHold()
	m.dismissRateLimitSwitchPromptForBackendBanner()
	return m.finishRateLimitRecovery()
}

// waitingForLunaReserve mirrors Rust's waiting_for_luna_reserve: the banner
// authorizes Reserve but the session is still on the blocked model.
func (m *Model) waitingForLunaReserve() bool {
	if m == nil {
		return false
	}
	banner := m.backendBanner.banner
	return m.currentBannerModel() != LunaReserveModel &&
		banner != nil &&
		banner.BannerType == BackendBannerLunaReserve
}

// syncRateLimitRecoveryHold marks user turns to be held (Rust
// hold_rate_limit_recovery).
func (m *Model) syncRateLimitRecoveryHold() {
	if m == nil {
		return
	}
	if m.waitingForLunaReserve() {
		m.rateLimitRecoveryPending = true
	}
}

// finishRateLimitRecovery releases held turns once the session is no longer
// waiting for Reserve (Rust finish_rate_limit_recovery).
func (m *Model) finishRateLimitRecovery() bubbletea.Cmd {
	if m == nil || !m.rateLimitRecoveryPending {
		return nil
	}
	if m.waitingForLunaReserve() {
		return nil
	}
	m.rateLimitRecoveryPending = false
	return m.submitNextQueued()
}

// clearRateLimitRecoveryHold drops the hold after a model change that did not
// come from the backend switch (Rust settings.rs clears the flag on a model
// change).
func (m *Model) clearRateLimitRecoveryHold() {
	if m == nil {
		return
	}
	m.rateLimitRecoveryPending = false
}

// dismissRateLimitSwitchPromptForBackendBanner mirrors Rust's banner
// presentation dismissing the lower-cost switch prompt view and re-arming it as
// pending, so it can reappear once the banner clears.
func (m *Model) dismissRateLimitSwitchPromptForBackendBanner() {
	if m == nil || !m.backendBannerApplicable() {
		return
	}
	if m.rateLimitSwitchPrompt != chatwidget.RateLimitSwitchPromptShown {
		return
	}
	m.rateLimitSwitchPrompt = chatwidget.RateLimitSwitchPromptPending
	if m.modal != nil && m.modal.kind == ModalKindRateLimitSwitch {
		m.modal = nil
		m.refreshTranscript()
	}
}

// backendBannerAcceptsKeys mirrors Rust's InlineBanner key gate: draft input,
// completion menus, and running tasks keep their normal keys, and the banner is
// only interactive while the composer is empty.
func (m *Model) backendBannerAcceptsKeys() bool {
	if m == nil || !m.backendBannerVisible() {
		return false
	}
	if m.modal != nil || m.overlay != nil || m.agentsOverview != nil || m.readOnlyThread {
		return false
	}
	if m.windowsSandboxSetupActive || m.isTaskRunning() {
		return false
	}
	if len(m.attachments) > 0 || m.composer.Value() != "" {
		return false
	}
	if m.slashPopupVisible() || m.skillPopupVisible() {
		return false
	}
	return true
}

// handleBackendBannerKey consumes Esc (dismiss) and the numbered CTA keys.
func (m *Model) handleBackendBannerKey(message bubbletea.KeyMsg) (bubbletea.Cmd, bool) {
	if !m.backendBannerAcceptsKeys() {
		return nil, false
	}
	banner := m.backendBanner.visibleBanner(m.currentBannerModel())
	actions := m.backendBannerActions(banner)
	switch message.Type {
	case bubbletea.KeyEsc:
		if !banner.Dismissible {
			return nil, false
		}
		m.backendBanner.dismiss()
		return nil, true
	case bubbletea.KeyRunes:
		if message.Alt || len(message.Runes) != 1 {
			return nil, false
		}
		index := int(message.Runes[0] - '1')
		if index < 0 || index >= len(actions) {
			return nil, false
		}
		action := actions[index]
		if action.Kind == BannerActionContinueReserve {
			// Continuing is a local dismissal, separate from the backend's CTAs.
			m.backendBanner.dismiss()
			return nil, true
		}
		if action.Kind == BannerActionResetUsage {
			return m.openRateLimitResetView(), true
		}
		if m.onBackendBannerAction == nil {
			return nil, true
		}
		return m.onBackendBannerAction(action), true
	}
	return nil, false
}

// backendBannerActions is the CTA list the surface renders. Rust appends the
// local "Continue with Luna Reserve" choice while the current model is Reserve.
func (m *Model) backendBannerActions(banner *BackendBannerView) []BackendBannerAction {
	if banner == nil {
		return nil
	}
	actions := append([]BackendBannerAction(nil), banner.Actions...)
	if banner.BannerType == BackendBannerLunaReserve && m.currentBannerModel() == LunaReserveModel {
		actions = append(actions, BackendBannerAction{Label: "Continue with Luna Reserve", Kind: BannerActionContinueReserve})
	}
	return actions
}

// renderBackendBanner renders the inline banner above the composer.
func (m *Model) renderBackendBanner() string {
	currentModel := m.currentBannerModel()
	banner := m.backendBanner.visibleBanner(currentModel)
	if banner == nil {
		return ""
	}
	m.backendBanner.present(banner, currentModel)
	actions := m.backendBannerActions(banner)
	title := banner.Title
	description := banner.Description
	// The backend emits the reserve banner only after ordinary usage is
	// exhausted; while the current model is not yet Reserve the copy describes
	// the accepted replacement instead.
	if banner.BannerType == BackendBannerLunaReserve && currentModel != LunaReserveModel {
		title = "Usage limit reached"
		description = "Your included usage is exhausted. Choose an option below to continue."
	}
	width := m.width
	if width <= 0 {
		width = 80
	}
	innerWidth := max(width-4, 8)
	var lines []string
	for _, line := range codextui.WrapLines(strings.Split(title, "\n"), codextui.WrapOptions{Width: innerWidth}) {
		lines = append(lines, "  "+line)
	}
	if strings.TrimSpace(description) != "" {
		for _, line := range codextui.WrapLines(strings.Split(description, "\n"), codextui.WrapOptions{Width: innerWidth}) {
			lines = append(lines, "  "+line)
		}
	}
	if len(actions) > 0 {
		lines = append(lines, "")
		for index, action := range actions {
			lines = append(lines, "  "+codextui.NumberedSelectionPrefix(index, false)+action.Label)
		}
	}
	if hint := backendBannerHint(banner.Dismissible, len(actions) > 0); hint != "" {
		lines = append(lines, "", "  "+hint)
	}
	for index, line := range lines {
		lines[index] = fitTerminalLine(line, width)
	}
	return strings.Join(lines, "\n")
}

// backendBannerHint mirrors Rust's inline-banner footer copy.
func backendBannerHint(dismissible bool, hasActions bool) string {
	switch {
	case dismissible && hasActions:
		return "Press a number to choose \u00b7 esc to dismiss \u00b7 type to continue"
	case dismissible:
		return "esc to dismiss \u00b7 type to continue"
	case hasActions:
		return "Press a number to choose"
	default:
		return ""
	}
}

// slashPopupVisible reports whether the slash-command popup is showing.
func (m *Model) slashPopupVisible() bool {
	return m != nil && strings.TrimSpace(m.renderSlashPopup()) != ""
}

// skillPopupVisible reports whether the skill popup is showing.
func (m *Model) skillPopupVisible() bool {
	return m != nil && strings.TrimSpace(m.renderSkillPopup()) != ""
}

func int64PtrValue(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

func firstNonEmptyStringPtr(primary *string, fallback *string) string {
	if primary != nil && strings.TrimSpace(*primary) != "" {
		return *primary
	}
	if fallback != nil {
		return *fallback
	}
	return ""
}
