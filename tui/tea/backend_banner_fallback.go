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
	if m == nil || m.State == nil || !m.State.HasChatGPTAccount {
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

// applyBackendBannerFallback applies the switch (Rust's
// apply_backend_banner_fallback + finish_backend_banner_fallback).
func (m *Model) applyBackendBannerFallback() {
	if m == nil || m.State == nil {
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
