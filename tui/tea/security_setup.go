package tea

import (
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"
)

// SecuritySetupNotice is the validated account-security reminder handed to
// the TUI (Rust #49715).
type SecuritySetupNotice struct {
	Title       string
	Description string
	ActionLabel string
	ActionURL   string
}

// SecuritySetupResultMsg delivers one asynchronous reminder read.
type SecuritySetupResultMsg struct {
	RequestID uint64
	Identity  string
	Notice    *SecuritySetupNotice
	Err       error
}

func (m *Model) securitySetupCmd() bubbletea.Cmd {
	if m == nil || m.onReadSecuritySetup == nil {
		return nil
	}
	m.securitySetupRequestID++
	requestID := m.securitySetupRequestID
	return func() bubbletea.Msg {
		identity, notice, err := m.onReadSecuritySetup()
		return SecuritySetupResultMsg{RequestID: requestID, Identity: identity, Notice: notice, Err: err}
	}
}

func (m *Model) applySecuritySetupResult(msg SecuritySetupResultMsg) {
	if m == nil || msg.RequestID != m.securitySetupRequestID {
		return
	}
	if msg.Err != nil || msg.Notice == nil {
		m.securitySetupNotice = nil
		return
	}
	identity := strings.TrimSpace(msg.Identity)
	if identity != "" && identity != m.securitySetupIdentity {
		m.securitySetupIdentity = identity
		m.securitySetupDismissed = false
	}
	if m.securitySetupDismissed {
		m.securitySetupNotice = nil
		return
	}
	notice := *msg.Notice
	notice.Title = strings.TrimSpace(notice.Title)
	notice.Description = strings.TrimSpace(notice.Description)
	notice.ActionLabel = strings.TrimSpace(notice.ActionLabel)
	notice.ActionURL = strings.TrimSpace(notice.ActionURL)
	if notice.Title == "" || notice.ActionLabel == "" || notice.ActionURL == "" {
		m.securitySetupNotice = nil
		return
	}
	m.securitySetupNotice = &notice
}

func (m *Model) securitySetupBannerVisible() bool {
	if m == nil || m.securitySetupNotice == nil || m.securitySetupDismissed {
		return false
	}
	// Usage/billing banners take priority over the reminder.
	return m.backendBanner.visibleBanner(m.currentBannerModel()) == nil
}

func (m *Model) renderSecuritySetupBanner() string {
	if !m.securitySetupBannerVisible() {
		return ""
	}
	notice := m.securitySetupNotice
	return m.renderInlineBanner(notice.Title, notice.Description, []BackendBannerAction{{
		Label: notice.ActionLabel,
		Kind:  BannerActionOpenURL,
		URL:   notice.ActionURL,
	}}, true)
}

func (m *Model) handleSecuritySetupKey(msg bubbletea.KeyMsg) (bubbletea.Cmd, bool) {
	if !m.securitySetupBannerVisible() {
		return nil, false
	}
	switch msg.Type {
	case bubbletea.KeyEsc:
		m.securitySetupDismissed = true
		m.securitySetupNotice = nil
		return nil, true
	case bubbletea.KeyRunes:
		if msg.Alt || len(msg.Runes) != 1 || msg.Runes[0] != '1' {
			return nil, false
		}
		target := m.securitySetupNotice.ActionURL
		if m.onOpenURL == nil || target == "" {
			return nil, true
		}
		opener := m.onOpenURL
		m.securitySetupDismissed = true
		m.securitySetupNotice = nil
		return func() bubbletea.Msg {
			return OpenURLResultMsg{URL: target, Err: opener(target)}
		}, true
	default:
		return nil, false
	}
}
