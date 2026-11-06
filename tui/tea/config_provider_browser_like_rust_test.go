package tea

import (
	"errors"
	"strings"
	"testing"

	bubbletea "charm.land/bubbletea/v2"

	"codex_go/model"
	codextui "codex_go/tui"
	"codex_go/tui/keymsg"
	"codex_go/utils"
)

func providerBrowserModel(t *testing.T, load ModelProviderListFunc) *Model {
	t.Helper()
	m := NewModel(codextui.NewState(nil), Options{
		Width:                100,
		Height:               30,
		OnListModelProviders: load,
	})
	m.State.ThreadID = "thread-providers"
	m.State.Provider = "openai"
	return m
}

func openProviderBrowser(t *testing.T, m *Model) bubbletea.Cmd {
	t.Helper()
	m.openConfigPanel()
	if m.modal == nil || m.modal.configPanel == nil {
		t.Fatal("config panel did not open")
	}
	m.modal.configPanel.section = configSectionSessions
	row := configPanelRowByLabel(t, "Model providers")
	cmd := m.activateConfigRow(row)
	if m.modal.configPanel.providers == nil {
		t.Fatal("provider browser did not open")
	}
	return cmd
}

func applyCmd(t *testing.T, m *Model, cmd bubbletea.Cmd) *Model {
	t.Helper()
	if cmd == nil {
		return m
	}
	msg := cmd()
	if msg == nil {
		return m
	}
	next, _ := m.Update(msg)
	cast, ok := next.(*Model)
	if !ok || cast == nil {
		t.Fatalf("Update() returned %T", next)
	}
	return cast
}

func providerBrowserPage() model.ProviderListResponse {
	baseURL := "https://gateway.example.com/v1"
	envKey := "GATEWAY_KEY"
	return model.ProviderListResponse{
		Data: []model.ProviderSummary{
			{ID: "openai", Name: "OpenAI", Settings: model.ProviderSettings{Type: "openai"}},
			{
				ID:      "gateway",
				Name:    "Gateway",
				BaseURL: &baseURL,
				Settings: model.ProviderSettings{
					Type: "customGateway",
					Auth: &model.GatewayAuth{Mode: "environmentVariable", EnvKey: &envKey},
				},
			},
		},
		DefaultProviderID: "gateway",
	}
}

// Rust #59541: the Sessions entry browses the connected server's providers,
// names the current chat's provider, and opens scrollable details without
// changing the active provider.
func TestConfigPanelModelProvidersBrowserLikeRust(t *testing.T) {
	m := providerBrowserModel(t, func() (model.ProviderListResponse, error) {
		return providerBrowserPage(), nil
	})
	cmd := openProviderBrowser(t, m)
	view := utils.StripANSI(m.viewString())
	if !strings.Contains(view, "Loading model providers") || !strings.Contains(view, "Back") {
		t.Fatalf("loading view = %q", view)
	}
	m = applyCmd(t, m, cmd)
	view = utils.StripANSI(m.viewString())
	for _, want := range []string{
		"Model providers",
		"Current chat: openai \u00b7 Default for new chats: gateway",
		"OpenAI (current)",
		"Gateway",
		"gateway",
		"enter view details \u00b7 esc back",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("provider list missing %q:\n%s", want, view)
		}
	}

	// OpenAI is selected as the current provider; entering shows its details.
	m.Update(key(keymsg.KeyEnter))
	view = utils.StripANSI(m.viewString())
	for _, want := range []string{"Model provider details", "Provider ID  openai", "Type  Built-in OpenAI provider"} {
		if !strings.Contains(view, want) {
			t.Fatalf("openai details missing %q:\n%s", want, view)
		}
	}
	m.Update(key(keymsg.KeyEsc))
	m.Update(key(keymsg.KeyDown))
	m.Update(key(keymsg.KeyEnter))
	view = utils.StripANSI(m.viewString())
	for _, want := range []string{"Gateway", "Provider ID  gateway", "Base URL  https://gateway.example.com/v1", "API key variable  GATEWAY_KEY"} {
		if !strings.Contains(view, want) {
			t.Fatalf("gateway details missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "Built-in OpenAI provider") {
		t.Fatalf("gateway details reused the OpenAI type: %q", view)
	}
	// Browsing never changes the active provider.
	if m.State.Provider != "openai" {
		t.Fatalf("active provider = %q, want openai", m.State.Provider)
	}
	// Esc returns to the list, then to the configuration panel.
	m.Update(key(keymsg.KeyEsc))
	if m.modal.configPanel.providers == nil {
		t.Fatal("esc from details closed the browser")
	}
	m.Update(key(keymsg.KeyEsc))
	if m.modal.configPanel.providers != nil || m.modal.kind != ModalKindConfig {
		t.Fatalf("esc from list did not return to the configuration panel: %#v", m.modal)
	}
}

// An older app server reports the method as unsupported; the browser explains
// what to update instead of failing silently.
func TestConfigPanelModelProvidersUnsupportedServerLikeRust(t *testing.T) {
	m := providerBrowserModel(t, func() (model.ProviderListResponse, error) {
		return model.ProviderListResponse{}, errors.New("Update the connected app server to browse model providers.")
	})
	cmd := openProviderBrowser(t, m)
	m = applyCmd(t, m, cmd)
	view := utils.StripANSI(m.viewString())
	if !strings.Contains(view, "Update the connected app server to browse model providers.") {
		t.Fatalf("unsupported view = %q", view)
	}
	if !strings.Contains(view, "1. Back") {
		t.Fatalf("unsupported view has no Back row: %q", view)
	}
	m.Update(key(keymsg.KeyEnter))
	if m.modal.configPanel.providers != nil {
		t.Fatal("Back did not dismiss the error view")
	}
}

// A reply for a dismissed popup must not replace the reopened list.
func TestConfigPanelModelProvidersDropsStaleResultLikeRust(t *testing.T) {
	calls := 0
	m := providerBrowserModel(t, func() (model.ProviderListResponse, error) {
		calls++
		return providerBrowserPage(), nil
	})
	staleCmd := openProviderBrowser(t, m)
	m.Update(key(keymsg.KeyEsc))
	if m.modal.configPanel.providers != nil {
		t.Fatal("esc did not dismiss the browser")
	}
	m.Update(key(keymsg.KeyEsc))
	if m.modal != nil {
		t.Fatal("second esc did not close the configuration panel")
	}
	// Reopen and deliver the stale reply first.
	m.openConfigPanel()
	m.modal.configPanel.section = configSectionSessions
	freshCmd := m.activateConfigRow(configPanelRowByLabel(t, "Model providers"))
	m = applyCmd(t, m, staleCmd)
	view := utils.StripANSI(m.viewString())
	if strings.Contains(view, "Current chat:") {
		t.Fatalf("stale reply replaced the reopened list:\n%s", view)
	}
	m = applyCmd(t, m, freshCmd)
	view = utils.StripANSI(m.viewString())
	if !strings.Contains(view, "Current chat: openai") {
		t.Fatalf("fresh reply did not load the list:\n%s", view)
	}
	if calls != 2 {
		t.Fatalf("loader calls = %d, want 2", calls)
	}
}

// A list opened before the first chat attached names "No active chat" and
// refreshes once the chat's provider is known (Rust refresh_list_if_present).
func TestConfigPanelModelProvidersRefreshesAfterFirstChatLikeRust(t *testing.T) {
	m := NewModel(codextui.NewState(nil), Options{
		Width:                100,
		Height:               30,
		OnListModelProviders: func() (model.ProviderListResponse, error) { return providerBrowserPage(), nil },
	})
	cmd := openProviderBrowser(t, m)
	m = applyCmd(t, m, cmd)
	if view := utils.StripANSI(m.viewString()); !strings.Contains(view, "Current chat: No active chat") {
		t.Fatalf("pre-attach view = %q", view)
	}
	m.State.ThreadID = "thread-late"
	m.State.Provider = "gateway"
	refresh := m.refreshOpenProviderBrowserCmd()
	if refresh == nil {
		t.Fatal("first chat attachment did not refresh the open provider list")
	}
	m = applyCmd(t, m, refresh)
	if view := utils.StripANSI(m.viewString()); !strings.Contains(view, "Current chat: gateway") {
		t.Fatalf("refreshed view = %q", view)
	}
	if again := m.refreshOpenProviderBrowserCmd(); again != nil {
		t.Fatal("provider list refreshed twice for the same chat attachment")
	}
}
