package tea

import (
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
)

// Rust parity: codex-rs/tui/src/chatwidget/backend_banners.rs
// (backend_banner_fallback / finish_backend_banner_fallback) and
// codex-rs/tui/src/app/backend_banner_fallback.rs. Go applies the transition to
// its local selection (the model is sent with each turn), so no server round
// trip is needed.

// backendBannerCatalogNeeded reports whether resolving the current banner
// requires the hidden-inclusive catalog.
func (m *Model) backendBannerCatalogNeeded() bool {
	if m == nil || m.backendBanner.banner == nil {
		return m != nil && m.currentBannerModel() == LunaReserveModel && m.backendBanner.ordinaryUsageRecovered
	}

	banner := m.backendBanner.banner
	return banner.BannerType == BackendBannerLunaReserve ||
		(banner.BlockedModelSlug != nil && len(banner.FallbackModelSlugs) > 0)
}

// applyBackendBannerFallbackCmd resolves and applies a backend-authorized model
// switch, fetching the catalog first when needed.
func (m *Model) applyBackendBannerFallbackCmd() bubbletea.Cmd {
	if m == nil || m.State == nil || !m.State.HasChatGPTAccount || !m.requiresOpenAIAuth() {
		return nil
	}
	if !m.backendBannerCatalogNeeded() {
		return nil
	}
	if len(m.modelCatalogOpts) == 0 {
		return m.fetchModelCatalog()
	}
	m.applyBackendBannerFallback()
	return nil
}

// requiresOpenAIAuth mirrors Rust's ChatWidget::requires_openai_auth gate: only a
// provider that authenticates through OpenAI can move between the account's
// billed models. Go resolves it from the session provider, defaulting to true
// for the implicit/OpenAI provider.
func (m *Model) requiresOpenAIAuth() bool {
	if m == nil {
		return false
	}
	if m.requiresOpenAIAuthOverride != nil {
		return *m.requiresOpenAIAuthOverride
	}
	provider := ""
	if m.State != nil {
		provider = strings.ToLower(strings.TrimSpace(m.State.Provider))
	}
	return provider == "" || provider == "openai"
}

// applyBackendBannerFallback applies the switch (Rust's
// apply_backend_banner_fallback + finish_backend_banner_fallback).
func (m *Model) applyBackendBannerFallback() {
	if m == nil || m.State == nil || !m.State.HasChatGPTAccount || !m.requiresOpenAIAuth() {
		return
	}
	previousModel := m.currentBannerModel()
	previousEffort := m.State.EffectiveReasoningEffort()
	// A reattached Reserve task restores its saved return target; a target from
	// another account is stale and is cleared (Rust clear_reserve_return).
	if previousModel == LunaReserveModel && m.reserveReturn == nil {
		m.reserveReturn = loadReserveReturn(m.codexHome, m.currentThreadID())
	}
	if m.reserveReturn != nil && m.reserveReturn.AccountID != m.backendBanner.accountID {
		clearReserveReturn(m.codexHome, m.currentThreadID())
		m.reserveReturn = nil
	}
	transition := m.backendBanner.fallbackSwitch(m.modelCatalogOpts, previousModel, previousEffort, m.reserveReturn)
	if transition == nil {
		return
	}
	// The return target is saved before changing state when the switch enters
	// Reserve; any other switch (including a recovery) clears it.
	if transition.Model == LunaReserveModel {
		m.reserveReturn = &ReserveReturn{
			AccountID: m.backendBanner.accountID,
			Model:     previousModel,
			Effort:    previousEffort,
		}
		_ = saveReserveReturn(m.codexHome, m.currentThreadID(), m.reserveReturn)
	} else {
		clearReserveReturn(m.codexHome, m.currentThreadID())
		m.reserveReturn = nil
	}
	m.State.Model = transition.Model
	if strings.TrimSpace(transition.Effort) != "" {
		m.State.ReasoningEffort = transition.Effort
	}
	// A submission queued before the switch may still name the replaced model.
	if previousModel == LunaReserveModel || transition.Model == LunaReserveModel {
		m.rewriteQueuedSubmissionsForModelSwitch(previousModel, transition.Model, transition.Effort)
	}
	m.refreshServiceTierCommands()
	// The post-switch notice is a new occurrence, not the blocked-state one.
	m.backendBanner.shown = false
	m.backendBanner.dismissed = m.backendBanner.reserveNoticeAlreadyShown()
	m.backendBanner.presented = nil
	m.addInfoHistoryMessage(m.backendBannerSwitchNotice(transition))
	m.refreshTranscript()
}

// backendBannerSwitchNotice renders Rust's automatic-switch info message.
func (m *Model) backendBannerSwitchNotice(transition *AutomaticModelSwitch) string {
	label := transition.Model
	for _, option := range m.modelCatalogOpts {
		if option.ID == transition.Model {
			label = strings.TrimSpace(option.Label)
			if label == "" {
				label = transition.Model
			}
			break
		}
	}
	prefix := "Automatically switched to"
	suffix := "due to usage limits."
	if transition.Reason == AutomaticModelSwitchUsageRecovered {
		prefix = "Automatically switched back to"
		suffix = "because ordinary usage is available again."
	}
	message := prefix + " " + label
	if strings.TrimSpace(transition.Effort) != "" {
		if effortLabel := strings.TrimSpace(codextui.ReasoningEffortLabel(transition.Effort)); effortLabel != "" {
			message += " " + effortLabel
		}
	}
	return message + " " + suffix
}

// rewriteQueuedSubmissionsForModelSwitch mirrors Rust's
// apply_reserve_fallback_to_pending_turn: a queued turn composed before the
// switch is retargeted at the accepted model.
func (m *Model) rewriteQueuedSubmissionsForModelSwitch(replacedModel string, newModel string, newEffort string) {
	if m == nil || strings.TrimSpace(replacedModel) == "" || strings.TrimSpace(newModel) == "" {
		return
	}
	var effort *string
	if strings.TrimSpace(newEffort) != "" {
		value := newEffort
		effort = &value
	}
	rewrite := func(request *SubmitRequest) {
		if request == nil || strings.TrimSpace(request.Model) != replacedModel {
			return
		}
		request.Model = newModel
		if request.CollaborationMode != nil {
			request.CollaborationMode.Settings.Model = newModel
			request.CollaborationMode.Settings.ReasoningEffort = effort
		}
	}
	for index := range m.queued {
		rewrite(&m.queued[index].Request)
	}
	for index := range m.rejectedSteers {
		rewrite(&m.rejectedSteers[index].Request)
	}
	for index := range m.pendingSteers {
		rewrite(&m.pendingSteers[index].Request)
	}
}
