package tea

import (
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/features"
	"codex_go/plugin"
	chatwidget "codex_go/tui/chatwidget"
	historycell "codex_go/tui/history_cell"
)

type PluginReadResultMsg struct {
	CWD         string
	DisplayName string
	Response    plugin.PluginReadResponse
	Err         error
}

type PluginInstallResultMsg struct {
	CWD         string
	DisplayName string
	Response    plugin.PluginInstallResponse
	Err         error
}

type PluginUninstallResultMsg struct {
	CWD         string
	DisplayName string
	Response    plugin.PluginUninstallResponse
	Err         error
}

type PluginEnabledWriteResultMsg struct {
	CWD      string
	PluginID string
	Enabled  bool
	Err      error
}

type MarketplaceAddResultMsg struct {
	CWD      string
	Source   string
	Response plugin.MarketplaceAddResponse
	Err      error
}

type MarketplaceRemoveResultMsg struct {
	CWD         string
	Name        string
	DisplayName string
	Response    plugin.MarketplaceRemoveResponse
	Err         error
}

type MarketplaceUpgradeResultMsg struct {
	CWD      string
	Name     string
	Response plugin.MarketplaceUpgradeResponse
	Err      error
}

type PluginOpenURLResultMsg struct {
	Err error
}

type pluginBrowserModalState struct {
	cwd              string
	response         plugin.PluginListResponse
	catalog          chatwidget.PluginCatalogPopupModel
	activeTab        int
	selectedByTab    map[string]int
	query            string
	view             *chatwidget.SelectionView
	viewSelected     int
	detail           *plugin.PluginDetail
	addPrompt        bool
	addPromptInput   string
	pendingMarket    string
	pendingMarketTag string
	pendingActiveTab string
}

func (m *Model) applyPluginsCommand() bubbletea.Cmd {
	if m == nil {
		return nil
	}
	if !features.Enabled(m.featureSettings, "plugins") {
		m.applyHistoryCell(historycell.NewInfoEvent("Plugins are disabled.", "Enable the plugins feature to use /plugins."))
		return nil
	}
	cwd := strings.TrimSpace(m.sessionCWD)
	m.pluginRuntime.CurrentCWD = cwd
	if m.onReadPlugins == nil {
		m.openPluginBrowser(cwd, plugin.PluginListResponse{}, "")
		return nil
	}
	m.openPluginBrowserView(cwd, chatwidget.PluginsLoadingPopupView())
	return m.readPluginsCmd(cwd, false)
}

func (m *Model) readPluginsCmd(cwd string, forceRefetch bool) bubbletea.Cmd {
	if m == nil || m.onReadPlugins == nil {
		return nil
	}
	reader := m.onReadPlugins
	return func() bubbletea.Msg {
		response, err := reader(cwd, forceRefetch)
		return PluginListResultMsg{CWD: cwd, Response: response, Err: err}
	}
}

func (m *Model) applyPluginListResult(message PluginListResultMsg) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	if strings.TrimSpace(message.CWD) != strings.TrimSpace(m.sessionCWD) {
		return nil
	}
	if message.Err != nil {
		m.openPluginBrowserView(message.CWD, chatwidget.PluginsErrorPopupView(strings.TrimSpace(message.Err.Error())))
		return nil
	}
	m.mentionPluginInventory = pluginSummariesFromResponse(message.Response)
	m.mentionPluginInventoryReady = true
	m.mentionPluginInventoryErr = ""
	if m.mentionPopup != nil {
		m.mentionPopup.SetCandidates(m.mentionCandidates())
	}
	activeTabID := ""
	if state := m.pluginBrowserState(); state != nil && strings.TrimSpace(state.cwd) == strings.TrimSpace(message.CWD) {
		activeTabID = state.activeTabID()
		if strings.TrimSpace(state.pendingActiveTab) != "" {
			activeTabID = state.pendingActiveTab
		}
		m.pluginRuntime.CurrentCWD = strings.TrimSpace(message.CWD)
		m.pluginRuntime.OpenPluginsList(message.CWD, message.Response, activeTabID)
		m.rebuildPluginCatalog(message.Response, activeTabID)
		state.pendingActiveTab = ""
		m.notice = "Plugins"
		return nil
	}
	m.openPluginBrowser(message.CWD, message.Response, activeTabID)
	return nil
}

func (m *Model) openPluginBrowser(cwd string, response plugin.PluginListResponse, activeTabID string) {
	if m == nil {
		return
	}
	m.pluginRuntime.CurrentCWD = strings.TrimSpace(cwd)
	m.pluginRuntime.OpenPluginsList(cwd, response, activeTabID)
	remove, upgrade := m.pluginMarketplaceCapabilities(response)
	catalog := chatwidget.NewPluginCatalogPopupModel(response, chatwidget.PluginCatalogPopupOptions{
		ActiveTabID:            activeTabID,
		CanRemoveMarketplaces:  remove,
		CanUpgradeMarketplaces: upgrade,
	})
	state := &pluginBrowserModalState{
		cwd:           strings.TrimSpace(cwd),
		response:      response,
		catalog:       catalog,
		selectedByTab: map[string]int{},
	}
	state.selectTab(catalog.InitialTabID)
	m.modal = &modalState{kind: ModalKindPluginsBrowser, pluginBrowser: state}
	m.notice = "Plugins"
}

func (m *Model) openPluginBrowserView(cwd string, view chatwidget.SelectionView) {
	if m == nil {
		return
	}
	viewCopy := view
	state := &pluginBrowserModalState{
		cwd:           strings.TrimSpace(cwd),
		selectedByTab: map[string]int{},
		view:          &viewCopy,
	}
	state.viewSelected = firstEnabledPluginViewItem(viewCopy.Items)
	m.modal = &modalState{kind: ModalKindPluginsBrowser, pluginBrowser: state}
	m.notice = "Plugins"
}

func (m *Model) pluginBrowserState() *pluginBrowserModalState {
	if m == nil || m.modal == nil {
		return nil
	}
	return m.modal.pluginBrowser
}

func (s *pluginBrowserModalState) activeTabID() string {
	if s == nil || s.activeTab < 0 || s.activeTab >= len(s.catalog.Tabs) {
		return ""
	}
	return s.catalog.Tabs[s.activeTab].ID
}

func (s *pluginBrowserModalState) selectTab(tabID string) {
	if s == nil || len(s.catalog.Tabs) == 0 {
		return
	}
	s.activeTab = 0
	for i := range s.catalog.Tabs {
		if s.catalog.Tabs[i].ID == strings.TrimSpace(tabID) {
			s.activeTab = i
			break
		}
	}
}

func (m *Model) pluginMarketplaceCapabilities(response plugin.PluginListResponse) (map[string]bool, map[string]bool) {
	remove := map[string]bool{}
	upgrade := map[string]bool{}
	for _, marketplace := range response.Marketplaces {
		name := strings.TrimSpace(marketplace.Name)
		if name == "" || !m.pluginUserMarketplaces[name] {
			continue
		}
		remove[name] = true
		if marketplace.Path != nil && strings.TrimSpace(*marketplace.Path) != "" && m.pluginGitMarketplaces[name] {
			upgrade[name] = true
		}
	}
	return remove, upgrade
}

func (m *Model) updatePluginBrowserModal(message bubbletea.KeyMsg) bubbletea.Cmd {
	state := m.pluginBrowserState()
	if state == nil {
		return nil
	}
	if state.addPrompt {
		return m.updatePluginMarketplacePrompt(message)
	}
	if state.view != nil {
		return m.updatePluginSelectionView(message)
	}
	return m.updatePluginCatalog(message)
}

func (m *Model) updatePluginCatalog(message bubbletea.KeyMsg) bubbletea.Cmd {
	state := m.pluginBrowserState()
	if state == nil || len(state.catalog.Tabs) == 0 {
		return nil
	}
	switch message.Type {
	case bubbletea.KeyCtrlC:
		m.modal = nil
		m.notice = ""
		return nil
	case bubbletea.KeyEsc:
		if state.query != "" {
			state.query = ""
			state.setSelected(0)
			return nil
		}
		m.modal = nil
		m.notice = ""
		return nil
	case bubbletea.KeyLeft, bubbletea.KeyShiftTab:
		state.activeTab = (state.activeTab - 1 + len(state.catalog.Tabs)) % len(state.catalog.Tabs)
		return nil
	case bubbletea.KeyRight, bubbletea.KeyTab:
		state.activeTab = (state.activeTab + 1) % len(state.catalog.Tabs)
		return nil
	case bubbletea.KeyUp:
		state.moveSelected(-1)
		return nil
	case bubbletea.KeyDown:
		state.moveSelected(1)
		return nil
	case bubbletea.KeyPgUp:
		state.moveSelected(-m.pluginPageSize())
		return nil
	case bubbletea.KeyPgDown, bubbletea.KeyCtrlD:
		state.moveSelected(m.pluginPageSize())
		return nil
	case bubbletea.KeyHome:
		state.setSelected(0)
		return nil
	case bubbletea.KeyEnd:
		state.setSelected(len(state.filteredItems()) - 1)
		return nil
	case bubbletea.KeyBackspace:
		if state.query != "" {
			runes := []rune(state.query)
			state.query = string(runes[:len(runes)-1])
			state.setSelected(0)
		}
		return nil
	case bubbletea.KeySpace:
		return m.toggleSelectedPlugin()
	case bubbletea.KeyEnter:
		return m.openSelectedPlugin()
	case bubbletea.KeyCtrlR:
		return m.confirmActiveMarketplaceRemoval()
	case bubbletea.KeyCtrlU:
		return m.upgradeActiveMarketplace()
	case bubbletea.KeyRunes:
		if len(message.Runes) > 0 {
			state.query += string(message.Runes)
			state.setSelected(0)
		}
	}
	return nil
}

func (s *pluginBrowserModalState) filteredItems() []chatwidget.PluginSelectionItemModel {
	if s == nil || s.activeTab < 0 || s.activeTab >= len(s.catalog.Tabs) {
		return nil
	}
	items := s.catalog.Tabs[s.activeTab].Items
	query := strings.ToLower(strings.TrimSpace(s.query))
	if query == "" {
		return items
	}
	out := make([]chatwidget.PluginSelectionItemModel, 0, len(items))
	for _, item := range items {
		haystack := strings.ToLower(strings.Join([]string{item.Name, item.Description, item.SearchValue}, " "))
		if strings.Contains(haystack, query) {
			out = append(out, item)
		}
	}
	return out
}

func (s *pluginBrowserModalState) selected() int {
	if s == nil {
		return 0
	}
	index := s.selectedByTab[s.activeTabID()]
	count := len(s.filteredItems())
	if count == 0 {
		return 0
	}
	if index >= count {
		index = count - 1
	}
	if index < 0 {
		index = 0
	}
	return index
}

func (s *pluginBrowserModalState) setSelected(index int) {
	if s == nil {
		return
	}
	count := len(s.filteredItems())
	if count == 0 {
		index = 0
	} else {
		index = max(0, min(index, count-1))
	}
	s.selectedByTab[s.activeTabID()] = index
}

func (s *pluginBrowserModalState) moveSelected(delta int) {
	items := s.filteredItems()
	if len(items) == 0 {
		return
	}
	index := s.selected()
	step := 1
	if delta < 0 {
		step = -1
	}
	remaining := delta
	for remaining != 0 {
		for range items {
			index = (index + step + len(items)) % len(items)
			if !items[index].Disabled {
				break
			}
		}
		remaining -= step
	}
	s.setSelected(index)
}

func (m *Model) openSelectedPlugin() bubbletea.Cmd {
	state := m.pluginBrowserState()
	if state == nil {
		return nil
	}
	items := state.filteredItems()
	if len(items) == 0 {
		return nil
	}
	item := items[state.selected()]
	if item.Disabled {
		return nil
	}
	if item.Action == chatwidget.PluginMenuActionAddMarketplace {
		state.addPrompt = true
		state.addPromptInput = ""
		return nil
	}
	if item.DetailRequest == nil {
		if item.CanToggle {
			return m.togglePluginItem(item)
		}
		return nil
	}
	if m.onReadPlugin == nil {
		m.setPluginSelectionView(chatwidget.PluginDetailErrorPopupView("plugin/read is unavailable", true))
		return nil
	}
	displayName := strings.TrimSpace(item.Name)
	params := item.DetailRequest.ReadParams
	reader := m.onReadPlugin
	m.setPluginSelectionView(chatwidget.PluginDetailLoadingPopupView(displayName))
	cwd := state.cwd
	return func() bubbletea.Msg {
		response, err := reader(params)
		return PluginReadResultMsg{CWD: cwd, DisplayName: displayName, Response: response, Err: err}
	}
}

func (m *Model) toggleSelectedPlugin() bubbletea.Cmd {
	state := m.pluginBrowserState()
	if state == nil {
		return nil
	}
	items := state.filteredItems()
	if len(items) == 0 {
		return nil
	}
	return m.togglePluginItem(items[state.selected()])
}

func (m *Model) togglePluginItem(item chatwidget.PluginSelectionItemModel) bubbletea.Cmd {
	if !item.CanToggle || item.Toggle == nil || strings.TrimSpace(item.Toggle.PluginID) == "" {
		return nil
	}
	pluginID := strings.TrimSpace(item.Toggle.PluginID)
	current := item.Toggle.IsOn
	if desired, ok := m.pluginToggleDesired[pluginID]; ok {
		current = desired
	}
	enabled := !current
	state := m.pluginBrowserState()
	if state == nil {
		return nil
	}
	if m.onWritePluginEnabled == nil {
		m.addErrorHistoryMessage("Failed to update plugin config for " + pluginID + ": config/value/write is unavailable")
		return nil
	}
	if m.pluginToggleDesired == nil {
		m.pluginToggleDesired = map[string]bool{}
	}
	if m.pluginToggleActive == nil {
		m.pluginToggleActive = map[string]bool{}
	}
	m.pluginToggleDesired[pluginID] = enabled
	if m.pluginToggleActive[pluginID] {
		return nil
	}
	return m.startPluginToggleWrite(state.cwd, pluginID, enabled)
}

func (m *Model) startPluginToggleWrite(cwd string, pluginID string, enabled bool) bubbletea.Cmd {
	if m == nil || m.onWritePluginEnabled == nil {
		return nil
	}
	m.pluginToggleActive[pluginID] = true
	writer := m.onWritePluginEnabled
	return func() bubbletea.Msg {
		err := writer(pluginID, enabled)
		return PluginEnabledWriteResultMsg{CWD: cwd, PluginID: pluginID, Enabled: enabled, Err: err}
	}
}

func (m *Model) applyPluginReadResult(message PluginReadResultMsg) bubbletea.Cmd {
	state := m.pluginBrowserState()
	if state == nil || strings.TrimSpace(state.cwd) != strings.TrimSpace(message.CWD) {
		return nil
	}
	if message.Err != nil {
		m.setPluginSelectionView(chatwidget.PluginDetailErrorPopupView(strings.TrimSpace(message.Err.Error()), true))
		return nil
	}
	detail := message.Response.Plugin
	state.detail = &detail
	m.setPluginSelectionView(chatwidget.NewPluginDetailView(detail))
	return nil
}

func (m *Model) applyPluginEnabledWriteResult(message PluginEnabledWriteResultMsg) bubbletea.Cmd {
	state := m.pluginBrowserState()
	delete(m.pluginToggleActive, message.PluginID)
	if state == nil || strings.TrimSpace(state.cwd) != strings.TrimSpace(message.CWD) {
		delete(m.pluginToggleDesired, message.PluginID)
		return nil
	}
	errText := ""
	if message.Err != nil {
		errText = message.Err.Error()
	}
	outcome := m.pluginRuntime.OnPluginEnabledSet(message.CWD, message.PluginID, message.Enabled, errText)
	if outcome.ErrorMessage != "" {
		m.addErrorHistoryMessage(outcome.ErrorMessage)
	}
	cache := m.pluginRuntime.PluginsCacheForCurrentCWD()
	if cache.Response != nil {
		m.rebuildPluginCatalog(*cache.Response, state.activeTabID())
	}
	desired, pending := m.pluginToggleDesired[message.PluginID]
	if message.Err != nil {
		delete(m.pluginToggleDesired, message.PluginID)
		return nil
	}
	if pending && desired != message.Enabled {
		return m.startPluginToggleWrite(message.CWD, message.PluginID, desired)
	}
	delete(m.pluginToggleDesired, message.PluginID)
	return nil
}

func (m *Model) updatePluginSelectionView(message bubbletea.KeyMsg) bubbletea.Cmd {
	state := m.pluginBrowserState()
	if state == nil || state.view == nil {
		return nil
	}
	items := state.view.Items
	switch message.Type {
	case bubbletea.KeyCtrlC:
		m.modal = nil
		m.notice = ""
	case bubbletea.KeyEsc:
		if !state.view.AllowCancel {
			return nil
		}
		m.showPluginCatalog()
	case bubbletea.KeyUp:
		state.viewSelected = nextPluginViewItem(items, state.viewSelected, -1)
	case bubbletea.KeyDown:
		state.viewSelected = nextPluginViewItem(items, state.viewSelected, 1)
	case bubbletea.KeyPgUp:
		state.viewSelected = pluginViewPage(items, state.viewSelected, -m.pluginPageSize())
	case bubbletea.KeyPgDown, bubbletea.KeyCtrlD:
		state.viewSelected = pluginViewPage(items, state.viewSelected, m.pluginPageSize())
	case bubbletea.KeyHome:
		state.viewSelected = firstEnabledPluginViewItem(items)
	case bubbletea.KeyEnd:
		state.viewSelected = lastEnabledPluginViewItem(items)
	case bubbletea.KeyEnter:
		return m.applyPluginSelectionAction()
	}
	return nil
}

func (m *Model) applyPluginSelectionAction() bubbletea.Cmd {
	state := m.pluginBrowserState()
	if state == nil || state.view == nil || state.viewSelected < 0 || state.viewSelected >= len(state.view.Items) {
		return nil
	}
	item := state.view.Items[state.viewSelected]
	if item.Disabled {
		return nil
	}
	switch item.Action {
	case chatwidget.PluginMenuActionBackToPlugins:
		m.showPluginCatalog()
	case chatwidget.PluginMenuActionInstall:
		return m.installCurrentPlugin()
	case chatwidget.PluginMenuActionUninstall:
		return m.uninstallCurrentPlugin(item.ID)
	case chatwidget.PluginMenuActionAddMarketplace:
		state.view = nil
		state.addPrompt = true
		state.addPromptInput = ""
	case chatwidget.PluginMenuActionRemoveMarketplace:
		return m.removePendingMarketplace()
	case chatwidget.PluginMenuActionOpenAppInstallURL:
		return m.openCurrentPluginAppURL()
	case chatwidget.PluginMenuActionAuthFlowAdvance:
		return m.advancePluginAuthFlow(false)
	case chatwidget.PluginMenuActionAuthFlowAbandon:
		return m.advancePluginAuthFlow(true)
	default:
		if item.DismissOnSelect {
			m.modal = nil
			m.notice = ""
		}
	}
	return nil
}

func (m *Model) installCurrentPlugin() bubbletea.Cmd {
	state := m.pluginBrowserState()
	if state == nil || state.detail == nil || m.onInstallPlugin == nil {
		return nil
	}
	detail := *state.detail
	params, ok := pluginInstallParamsForDetail(detail)
	if !ok {
		m.setPluginSelectionView(chatwidget.PluginDetailErrorPopupView("plugin install location is unavailable", true))
		return nil
	}
	displayName := chatwidget.PluginDisplayName(detail.Summary)
	installer := m.onInstallPlugin
	cwd := state.cwd
	m.setPluginSelectionView(chatwidget.PluginInstallLoadingPopupView(displayName))
	return func() bubbletea.Msg {
		response, err := installer(params)
		return PluginInstallResultMsg{CWD: cwd, DisplayName: displayName, Response: response, Err: err}
	}
}

func pluginInstallParamsForDetail(detail plugin.PluginDetail) (plugin.PluginInstallParams, bool) {
	location, ok := chatwidget.PluginDetailLocation(detail)
	if !ok {
		return plugin.PluginInstallParams{}, false
	}
	params := plugin.PluginInstallParams{
		PluginID:   strings.TrimSpace(detail.Summary.ID),
		PluginName: chatwidget.PluginRequestName(detail.Summary),
	}
	switch location.Kind {
	case chatwidget.PluginLocationLocal:
		params.MarketplacePath = strings.TrimSpace(location.MarketplacePath)
	case chatwidget.PluginLocationRemote:
		params.RemoteMarketplaceName = strings.TrimSpace(location.MarketplaceName)
	}
	return params, true
}

func (m *Model) uninstallCurrentPlugin(pluginID string) bubbletea.Cmd {
	state := m.pluginBrowserState()
	if state == nil || state.detail == nil || m.onUninstallPlugin == nil {
		return nil
	}
	pluginID = strings.TrimSpace(pluginID)
	if pluginID == "" {
		pluginID, _ = chatwidget.PluginUninstallID(state.detail.Summary)
	}
	if pluginID == "" {
		return nil
	}
	displayName := chatwidget.PluginDisplayName(state.detail.Summary)
	uninstaller := m.onUninstallPlugin
	cwd := state.cwd
	m.setPluginSelectionView(chatwidget.PluginUninstallLoadingPopupView(displayName))
	return func() bubbletea.Msg {
		response, err := uninstaller(plugin.PluginUninstallParams{PluginID: pluginID})
		return PluginUninstallResultMsg{CWD: cwd, DisplayName: displayName, Response: response, Err: err}
	}
}

func (m *Model) applyPluginInstallResult(message PluginInstallResultMsg) bubbletea.Cmd {
	state := m.pluginBrowserState()
	if state == nil || strings.TrimSpace(state.cwd) != strings.TrimSpace(message.CWD) {
		return nil
	}
	var response *plugin.PluginInstallResponse
	if message.Err == nil {
		response = &message.Response
	}
	errText := ""
	if message.Err != nil {
		errText = message.Err.Error()
	}
	outcome := m.pluginRuntime.OnPluginInstallLoaded(message.CWD, message.DisplayName, response, errText)
	m.applyPluginRuntimeHistory(outcome)
	if outcome.ShowErrorPopup {
		m.setPluginSelectionView(chatwidget.PluginDetailErrorPopupView(outcome.ErrorMessage, true))
		return nil
	}
	if outcome.OpenAuthPopup {
		m.openCurrentPluginAuthView()
		return nil
	}
	m.setPluginSelectionView(chatwidget.PluginsLoadingPopupView())
	return m.readPluginsCmd(message.CWD, true)
}

func (m *Model) applyPluginUninstallResult(message PluginUninstallResultMsg) bubbletea.Cmd {
	state := m.pluginBrowserState()
	if state == nil || strings.TrimSpace(state.cwd) != strings.TrimSpace(message.CWD) {
		return nil
	}
	errText := ""
	if message.Err != nil {
		errText = message.Err.Error()
	}
	outcome := m.pluginRuntime.OnPluginUninstallLoaded(message.CWD, message.DisplayName, errText)
	m.applyPluginRuntimeHistory(outcome)
	if outcome.ShowErrorPopup {
		m.setPluginSelectionView(chatwidget.PluginDetailErrorPopupView(outcome.ErrorMessage, true))
		return nil
	}
	m.setPluginSelectionView(chatwidget.PluginsLoadingPopupView())
	return m.readPluginsCmd(message.CWD, true)
}

func (m *Model) openCurrentPluginAuthView() {
	if view, ok := m.pluginRuntime.CurrentPluginInstallAuthView(nil); ok {
		m.setPluginSelectionView(view)
	}
}

func (m *Model) openCurrentPluginAppURL() bubbletea.Cmd {
	flow := m.pluginRuntime.PluginInstallAuthFlow
	apps := m.pluginRuntime.PluginInstallAppsNeedingAuth
	if flow == nil || flow.NextAppIndex < 0 || flow.NextAppIndex >= len(apps) {
		return nil
	}
	app := apps[flow.NextAppIndex]
	if app.InstallURL == nil || strings.TrimSpace(*app.InstallURL) == "" || m.onOpenPluginURL == nil {
		return nil
	}
	opener := m.onOpenPluginURL
	target := strings.TrimSpace(*app.InstallURL)
	return func() bubbletea.Msg { return PluginOpenURLResultMsg{Err: opener(target)} }
}

func (m *Model) applyPluginOpenURLResult(message PluginOpenURLResultMsg) {
	if message.Err != nil {
		m.addErrorHistoryMessage("Failed to open app install URL: " + strings.TrimSpace(message.Err.Error()))
	}
}

func (m *Model) advancePluginAuthFlow(abandon bool) bubbletea.Cmd {
	var outcome chatwidget.PluginsRuntimeOutcome
	if abandon {
		outcome = m.pluginRuntime.AbandonPluginInstallAuthFlow()
	} else {
		outcome = m.pluginRuntime.AdvancePluginInstallAuthFlow()
	}
	m.applyPluginRuntimeHistory(outcome)
	if outcome.OpenAuthPopup {
		m.openCurrentPluginAuthView()
		return nil
	}
	state := m.pluginBrowserState()
	if outcome.FinishedAuthFlow && state != nil {
		m.setPluginSelectionView(chatwidget.PluginsLoadingPopupView())
		return m.readPluginsCmd(state.cwd, true)
	}
	return nil
}

func (m *Model) updatePluginMarketplacePrompt(message bubbletea.KeyMsg) bubbletea.Cmd {
	state := m.pluginBrowserState()
	if state == nil {
		return nil
	}
	switch message.Type {
	case bubbletea.KeyCtrlC:
		m.modal = nil
		m.notice = ""
	case bubbletea.KeyEsc:
		state.addPrompt = false
		state.addPromptInput = ""
	case bubbletea.KeyBackspace:
		if state.addPromptInput != "" {
			runes := []rune(state.addPromptInput)
			state.addPromptInput = string(runes[:len(runes)-1])
		}
	case bubbletea.KeyEnter:
		source := strings.TrimSpace(state.addPromptInput)
		if source == "" || m.onAddMarketplace == nil {
			return nil
		}
		adder := m.onAddMarketplace
		cwd := state.cwd
		state.addPrompt = false
		m.setPluginSelectionView(chatwidget.MarketplaceAddLoadingPopupView())
		return func() bubbletea.Msg {
			response, err := adder(plugin.MarketplaceAddParams{Source: source})
			return MarketplaceAddResultMsg{CWD: cwd, Source: source, Response: response, Err: err}
		}
	case bubbletea.KeyRunes:
		state.addPromptInput += string(message.Runes)
	}
	return nil
}

func (m *Model) applyMarketplaceAddResult(message MarketplaceAddResultMsg) bubbletea.Cmd {
	state := m.pluginBrowserState()
	if state == nil || strings.TrimSpace(state.cwd) != strings.TrimSpace(message.CWD) {
		return nil
	}
	var response *plugin.MarketplaceAddResponse
	if message.Err == nil {
		response = &message.Response
	}
	errText := ""
	if message.Err != nil {
		errText = message.Err.Error()
	}
	outcome := m.pluginRuntime.OnMarketplaceAddLoaded(message.CWD, response, errText)
	m.applyPluginRuntimeHistory(outcome)
	if outcome.ShowMarketplaceError {
		m.setPluginSelectionView(chatwidget.MarketplaceAddErrorPopupView(true))
		return nil
	}
	if m.pluginUserMarketplaces == nil {
		m.pluginUserMarketplaces = map[string]bool{}
	}
	if m.pluginGitMarketplaces == nil {
		m.pluginGitMarketplaces = map[string]bool{}
	}
	name := strings.TrimSpace(message.Response.MarketplaceName)
	m.pluginUserMarketplaces[name] = true
	if source, err := plugin.ParseMarketplaceSource(message.Source, nil); err == nil && source != nil {
		m.pluginGitMarketplaces[name] = source.Kind == plugin.MarketplaceSourceGit
	}
	state.pendingActiveTab = outcome.ActiveTabID
	m.setPluginSelectionView(chatwidget.PluginsLoadingPopupView())
	return m.readPluginsCmd(message.CWD, true)
}

func (m *Model) confirmActiveMarketplaceRemoval() bubbletea.Cmd {
	state := m.pluginBrowserState()
	marketplace, ok := state.activeMarketplace()
	if !ok || !m.pluginUserMarketplaces[strings.TrimSpace(marketplace.Name)] {
		return nil
	}
	state.pendingMarket = strings.TrimSpace(marketplace.Name)
	state.pendingMarketTag = chatwidget.MarketplaceDisplayName(marketplace)
	m.setPluginSelectionView(chatwidget.MarketplaceRemoveConfirmationView(state.pendingMarket, state.pendingMarketTag))
	return nil
}

func (m *Model) removePendingMarketplace() bubbletea.Cmd {
	state := m.pluginBrowserState()
	if state == nil || m.onRemoveMarketplace == nil || strings.TrimSpace(state.pendingMarket) == "" {
		return nil
	}
	name := strings.TrimSpace(state.pendingMarket)
	displayName := strings.TrimSpace(state.pendingMarketTag)
	remover := m.onRemoveMarketplace
	cwd := state.cwd
	m.setPluginSelectionView(chatwidget.MarketplaceRemoveLoadingPopupView(displayName))
	return func() bubbletea.Msg {
		response, err := remover(plugin.MarketplaceRemoveParams{MarketplaceName: name})
		return MarketplaceRemoveResultMsg{CWD: cwd, Name: name, DisplayName: displayName, Response: response, Err: err}
	}
}

func (m *Model) applyMarketplaceRemoveResult(message MarketplaceRemoveResultMsg) bubbletea.Cmd {
	state := m.pluginBrowserState()
	if state == nil || strings.TrimSpace(state.cwd) != strings.TrimSpace(message.CWD) {
		return nil
	}
	var response *plugin.MarketplaceRemoveResponse
	if message.Err == nil {
		response = &message.Response
	}
	errText := ""
	if message.Err != nil {
		errText = message.Err.Error()
	}
	outcome := m.pluginRuntime.OnMarketplaceRemoveLoaded(message.CWD, message.Name, message.DisplayName, response, errText)
	m.applyPluginRuntimeHistory(outcome)
	if outcome.ShowMarketplaceError {
		m.setPluginSelectionView(chatwidget.MarketplaceRemoveErrorPopupView(message.Name, message.DisplayName, true))
		return nil
	}
	delete(m.pluginUserMarketplaces, strings.TrimSpace(message.Name))
	delete(m.pluginGitMarketplaces, strings.TrimSpace(message.Name))
	state.pendingActiveTab = outcome.ActiveTabID
	m.setPluginSelectionView(chatwidget.PluginsLoadingPopupView())
	return m.readPluginsCmd(message.CWD, true)
}

func (m *Model) upgradeActiveMarketplace() bubbletea.Cmd {
	state := m.pluginBrowserState()
	marketplace, ok := state.activeMarketplace()
	if !ok || marketplace.Path == nil || !m.pluginUserMarketplaces[strings.TrimSpace(marketplace.Name)] || !m.pluginGitMarketplaces[strings.TrimSpace(marketplace.Name)] || m.onUpgradeMarketplace == nil {
		return nil
	}
	name := strings.TrimSpace(marketplace.Name)
	upgrader := m.onUpgradeMarketplace
	cwd := state.cwd
	m.setPluginSelectionView(chatwidget.MarketplaceUpgradeLoadingPopupView(name))
	return func() bubbletea.Msg {
		response, err := upgrader(plugin.MarketplaceUpgradeParams{MarketplaceName: &name})
		return MarketplaceUpgradeResultMsg{CWD: cwd, Name: name, Response: response, Err: err}
	}
}

func (m *Model) applyMarketplaceUpgradeResult(message MarketplaceUpgradeResultMsg) bubbletea.Cmd {
	state := m.pluginBrowserState()
	if state == nil || strings.TrimSpace(state.cwd) != strings.TrimSpace(message.CWD) {
		return nil
	}
	var response *plugin.MarketplaceUpgradeResponse
	if message.Err == nil {
		response = &message.Response
	}
	errText := ""
	if message.Err != nil {
		errText = message.Err.Error()
	}
	outcome := m.pluginRuntime.OnMarketplaceUpgradeLoaded(message.CWD, response, errText)
	m.applyPluginRuntimeHistory(outcome)
	state.pendingActiveTab = outcome.ActiveTabID
	m.setPluginSelectionView(chatwidget.PluginsLoadingPopupView())
	return m.readPluginsCmd(message.CWD, true)
}

func (s *pluginBrowserModalState) activeMarketplace() (plugin.PluginMarketplaceEntry, bool) {
	if s == nil {
		return plugin.PluginMarketplaceEntry{}, false
	}
	tabID := s.activeTabID()
	for _, marketplace := range s.response.Marketplaces {
		if chatwidget.MarketplaceTabID(marketplace) == tabID {
			return marketplace, true
		}
	}
	return plugin.PluginMarketplaceEntry{}, false
}

func (m *Model) applyPluginRuntimeHistory(outcome chatwidget.PluginsRuntimeOutcome) {
	if m == nil {
		return
	}
	if strings.TrimSpace(outcome.InfoMessage) != "" {
		m.applyHistoryCell(historycell.NewInfoEvent(outcome.InfoMessage, outcome.InfoHint))
	}
	if strings.TrimSpace(outcome.ErrorMessage) != "" {
		m.addErrorHistoryMessage(outcome.ErrorMessage)
	}
}

func (m *Model) rebuildPluginCatalog(response plugin.PluginListResponse, activeTabID string) {
	state := m.pluginBrowserState()
	if state == nil {
		return
	}
	remove, upgrade := m.pluginMarketplaceCapabilities(response)
	state.response = response
	state.catalog = chatwidget.NewPluginCatalogPopupModel(response, chatwidget.PluginCatalogPopupOptions{
		ActiveTabID:            activeTabID,
		CanRemoveMarketplaces:  remove,
		CanUpgradeMarketplaces: upgrade,
	})
	state.selectTab(activeTabID)
	state.view = nil
	state.detail = nil
	state.addPrompt = false
}

func (m *Model) showPluginCatalog() {
	state := m.pluginBrowserState()
	if state == nil {
		return
	}
	state.view = nil
	state.detail = nil
	state.addPrompt = false
	state.pendingMarket = ""
	state.pendingMarketTag = ""
}

func (m *Model) setPluginSelectionView(view chatwidget.SelectionView) {
	state := m.pluginBrowserState()
	if state == nil {
		return
	}
	viewCopy := view
	state.view = &viewCopy
	state.viewSelected = firstEnabledPluginViewItem(view.Items)
	state.addPrompt = false
}

func firstEnabledPluginViewItem(items []chatwidget.SelectionItem) int {
	for i := range items {
		if !items[i].Disabled {
			return i
		}
	}
	return 0
}

func lastEnabledPluginViewItem(items []chatwidget.SelectionItem) int {
	for i := len(items) - 1; i >= 0; i-- {
		if !items[i].Disabled {
			return i
		}
	}
	return 0
}

func nextPluginViewItem(items []chatwidget.SelectionItem, current int, direction int) int {
	if len(items) == 0 {
		return 0
	}
	if direction == 0 {
		return current
	}
	step := 1
	if direction < 0 {
		step = -1
	}
	index := current
	for range items {
		index = (index + step + len(items)) % len(items)
		if !items[index].Disabled {
			return index
		}
	}
	return current
}

func pluginViewPage(items []chatwidget.SelectionItem, current int, delta int) int {
	if len(items) == 0 {
		return 0
	}
	target := max(0, min(current+delta, len(items)-1))
	if !items[target].Disabled {
		return target
	}
	direction := 1
	if delta < 0 {
		direction = -1
	}
	return nextPluginViewItem(items, target, direction)
}

func (m *Model) pluginPageSize() int {
	if m == nil || m.height < 12 {
		return 5
	}
	return max(5, m.height-11)
}

func (m *Model) renderPluginBrowserModal() string {
	state := m.pluginBrowserState()
	if state == nil {
		return ""
	}
	if state.addPrompt {
		return strings.Join([]string{
			"Plugins",
			"Add Marketplace",
			"Enter owner/repo, a Git URL, or a local marketplace path.",
			"> " + state.addPromptInput,
			"Enter add | Esc back",
		}, "\n")
	}
	if state.view != nil {
		return m.renderPluginSelectionView(state)
	}
	return m.renderPluginCatalog(state)
}

func (m *Model) renderPluginCatalog(state *pluginBrowserModalState) string {
	if state == nil || len(state.catalog.Tabs) == 0 {
		return "Plugins\nNo plugins found.\nEsc close"
	}
	width := m.width
	if width <= 0 {
		width = 80
	}
	lines := []string{"Plugins"}
	tabs := make([]string, 0, len(state.catalog.Tabs))
	for i, tab := range state.catalog.Tabs {
		label := tab.Label
		if i == state.activeTab {
			label = "[" + label + "]"
		}
		tabs = append(tabs, label)
	}
	lines = append(lines, fitTerminalLine(strings.Join(tabs, "  "), width))
	tab := state.catalog.Tabs[state.activeTab]
	for _, header := range tab.HeaderLines {
		if strings.TrimSpace(header) != "" {
			lines = append(lines, fitTerminalLine(header, width))
		}
	}
	if state.query == "" {
		lines = append(lines, "/ "+state.catalog.SearchPlaceholder)
	} else {
		lines = append(lines, "/ "+state.query)
	}
	items := state.filteredItems()
	if len(items) == 0 {
		lines = append(lines, "  No plugins match your search.")
	} else {
		selected := state.selected()
		start, end := pluginVisibleRange(len(items), selected, m.pluginPageSize())
		for i := start; i < end; i++ {
			item := items[i]
			prefix := "  "
			if i == selected {
				prefix = "> "
			}
			toggle := ""
			if item.Toggle != nil {
				toggle = "[ ] "
				if item.Toggle.IsOn {
					toggle = "[x] "
				}
			}
			line := prefix + toggle + item.Name
			if strings.TrimSpace(item.Description) != "" {
				line += "  " + strings.ReplaceAll(strings.TrimSpace(item.Description), "\n", " | ")
			}
			lines = append(lines, fitTerminalLine(line, width))
		}
	}
	footer := state.catalog.FooterHint
	if tab.FooterHint != "" {
		footer = tab.FooterHint
	}
	if footer == "" {
		footer = "space toggle | left/right tabs | enter details | esc close"
	}
	lines = append(lines, fitTerminalLine(footer, width))
	return strings.Join(lines, "\n")
}

func (m *Model) renderPluginSelectionView(state *pluginBrowserModalState) string {
	view := state.view
	if view == nil {
		return ""
	}
	width := m.width
	if width <= 0 {
		width = 80
	}
	lines := []string{firstNonEmpty(view.Title, "Plugins")}
	if strings.TrimSpace(view.Subtitle) != "" {
		lines = append(lines, fitTerminalLine(view.Subtitle, width))
	}
	for _, header := range view.HeaderLines {
		if strings.TrimSpace(header) != "" {
			lines = append(lines, fitTerminalLine(header, width))
		}
	}
	start, end := pluginVisibleRange(len(view.Items), state.viewSelected, m.pluginPageSize())
	for i := start; i < end; i++ {
		item := view.Items[i]
		prefix := "  "
		if i == state.viewSelected {
			prefix = "> "
		}
		line := prefix + item.Name
		if item.Disabled {
			line += " [disabled]"
		}
		description := item.Description
		if i == state.viewSelected && strings.TrimSpace(item.SelectedDescription) != "" {
			description = item.SelectedDescription
		}
		if strings.TrimSpace(description) != "" {
			line += "  " + strings.ReplaceAll(strings.TrimSpace(description), "\n", " | ")
		}
		lines = append(lines, fitTerminalLine(line, width))
	}
	footer := strings.TrimSpace(view.FooterHint)
	if footer == "" {
		footer = "Enter choose | Esc back"
	}
	lines = append(lines, fitTerminalLine(footer, width))
	return strings.Join(lines, "\n")
}

func pluginVisibleRange(count int, selected int, pageSize int) (int, int) {
	if count <= 0 {
		return 0, 0
	}
	pageSize = max(1, pageSize)
	start := max(0, selected-pageSize/2)
	end := min(count, start+pageSize)
	start = max(0, end-pageSize)
	return start, end
}
