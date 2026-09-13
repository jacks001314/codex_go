package tea

import (
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	"codex_go/tui/chatwidget"
)

// Rust parity: codex-rs/tui/src/bottom_pane/actionable_banner.rs and
// codex-rs/tui/src/backend_banners.rs. The app parses and validates the
// backend-owned payload and resolves each CTA; the model renders the inline
// banner above the composer and reports selection/dismissal.

// BackendBannerActionKind mirrors Rust's BannerAction.
type BackendBannerActionKind string

const (
	BannerActionOpenURL     BackendBannerActionKind = "open_url"
	BannerActionNotifyOwner BackendBannerActionKind = "notify_owner"
	BannerActionResetUsage  BackendBannerActionKind = "reset_usage"
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

// BackendBannerView is one validated inline banner: copy plus the CTA labels in
// backend order.
type BackendBannerView struct {
	Title       string
	Description string
	Actions     []BackendBannerAction
	Dismissible bool
}

// BackendBannerResultMsg carries the startup/refresh banner read.
type BackendBannerResultMsg struct {
	Banner *BackendBannerView
	Err    error
}

// SetBackendBanner installs (or clears) the inline banner. A fresh banner
// resets the dismissal state, matching Rust's set_inline_banner.
func (m *Model) SetBackendBanner(banner *BackendBannerView) {
	if m == nil {
		return
	}
	if banner == nil {
		m.backendBanner = nil
		m.backendBannerDismissed = false
		m.backendBannerShown = false
		return
	}
	m.backendBanner = banner
	m.backendBannerDismissed = false
	m.backendBannerShown = false
}

// BackendBanner returns the active banner, if any.
func (m *Model) BackendBanner() *BackendBannerView {
	if m == nil {
		return nil
	}
	return m.backendBanner
}

func (m *Model) backendBannerVisible() bool {
	return m != nil && m.backendBanner != nil && !m.backendBannerDismissed
}

// dismissBackendBannerForNewTurn mirrors Rust's
// dismiss_backend_banner_for_new_turn: once a dismissible banner has been shown,
// starting a new turn hides it.
func (m *Model) dismissBackendBannerForNewTurn() {
	if m == nil || !m.backendBannerVisible() || !m.backendBannerShown {
		return
	}
	if !m.backendBanner.Dismissible {
		return
	}
	m.backendBannerDismissed = true
}

// backendBannerCmd performs the startup/refresh banner read.
func (m *Model) backendBannerCmd() bubbletea.Cmd {
	if m == nil || m.onReadBackendBanner == nil {
		return nil
	}
	reader := m.onReadBackendBanner
	return func() bubbletea.Msg {
		banner, err := reader()
		return BackendBannerResultMsg{Banner: banner, Err: err}
	}
}

func (m *Model) applyBackendBannerResult(message BackendBannerResultMsg) {
	if m == nil {
		return
	}
	// A failed read leaves the previous banner in place (Rust keeps the last
	// successfully parsed banner across transient refreshes).
	if message.Err != nil {
		return
	}
	m.SetBackendBanner(message.Banner)
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
	switch message.Type {
	case bubbletea.KeyEsc:
		if !m.backendBanner.Dismissible {
			return nil, false
		}
		m.backendBannerDismissed = true
		return nil, true
	case bubbletea.KeyRunes:
		if message.Alt || len(message.Runes) != 1 {
			return nil, false
		}
		index := int(message.Runes[0] - '1')
		if index < 0 || index >= len(m.backendBanner.Actions) {
			return nil, false
		}
		action := m.backendBanner.Actions[index]
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

// renderBackendBanner renders the inline banner above the composer.
func (m *Model) renderBackendBanner() string {
	if !m.backendBannerVisible() {
		return ""
	}
	m.backendBannerShown = true
	banner := m.backendBanner
	width := m.width
	if width <= 0 {
		width = 80
	}
	innerWidth := max(width-4, 8)
	var lines []string
	for _, line := range codextui.WrapLines(strings.Split(banner.Title, "\n"), codextui.WrapOptions{Width: innerWidth}) {
		lines = append(lines, "  "+line)
	}
	if strings.TrimSpace(banner.Description) != "" {
		for _, line := range codextui.WrapLines(strings.Split(banner.Description, "\n"), codextui.WrapOptions{Width: innerWidth}) {
			lines = append(lines, "  "+line)
		}
	}
	if len(banner.Actions) > 0 {
		lines = append(lines, "")
		for index, action := range banner.Actions {
			lines = append(lines, "  "+codextui.NumberedSelectionPrefix(index, false)+action.Label)
		}
	}
	if hint := backendBannerHint(banner.Dismissible, len(banner.Actions) > 0); hint != "" {
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
