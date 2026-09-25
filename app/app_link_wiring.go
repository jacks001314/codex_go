package app

import (
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/auth"
	codextea "codex_go/tui/tea"
)

// interactiveAppLinkActionHandler performs the app-link popup's host actions:
// the install/sign-in URL opens in the local browser, mirroring Rust's
// AppEvent::OpenUrlInBrowser (tui/src/bottom_pane/app_link_view.rs, #48015).
// Connector refreshes and app toggles are driven by the app-server requests the
// popup resolves, so they need no local action here.
func interactiveAppLinkActionHandler() func(codextea.AppLinkAction) bubbletea.Cmd {
	return func(action codextea.AppLinkAction) bubbletea.Cmd {
		if action.Kind != codextea.AppLinkActionOpenURL {
			return nil
		}
		target := strings.TrimSpace(action.URL)
		if target == "" {
			return nil
		}
		return func() bubbletea.Msg {
			if err := auth.OpenBrowser(target); err != nil {
				return codextea.StatusMsg{Status: "Could not open the link: " + err.Error()}
			}
			return nil
		}
	}
}
