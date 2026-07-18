package chatwidget

import (
	"strings"

	pluginapi "codex_go/plugin"
)

type PluginListFetchState struct {
	CacheCWD                 string
	InFlightCWD              string
	VerticalSectionRequested bool
}

type PluginsCacheKind string

const (
	PluginsCacheUninitialized PluginsCacheKind = "uninitialized"
	PluginsCacheLoading       PluginsCacheKind = "loading"
	PluginsCacheReady         PluginsCacheKind = "ready"
	PluginsCacheFailed        PluginsCacheKind = "failed"
)

type PluginsCacheState struct {
	Kind     PluginsCacheKind
	Response *pluginapi.PluginListResponse
	Error    string
}

type PluginsRuntimeState struct {
	CurrentCWD                     string
	Fetch                          PluginListFetchState
	Cache                          PluginsCacheState
	ActiveTabID                    string
	NewlyInstalledMarketplaceTabID string
	RemoteSectionsLoading          bool
	RemoteSectionsLoaded           bool
	RemoteSectionErrors            []PluginCatalogRemoteSectionError
	PluginInstallAppsNeedingAuth   []pluginapi.AppSummary
	PluginInstallAuthFlow          *PluginInstallAuthFlowState
}

type PluginsRuntimeOutcome struct {
	Ignored              bool
	RefreshPopup         bool
	ShowLoadingPopup     bool
	ShowErrorPopup       bool
	ShowMarketplaceError bool
	OpenAuthPopup        bool
	FinishedAuthFlow     bool
	ErrorMessage         string
	InfoMessage          string
	InfoHint             string
	ActiveTabID          string
}

type PluginsPopupKeyEvent struct {
	CtrlR bool
	CtrlU bool
	Press bool
}

type PluginsPopupKeyDecision struct {
	Handled                 bool
	OpenRemoveConfirmation  bool
	OpenMarketplaceUpgrade  bool
	FetchMarketplaceUpgrade bool
	MarketplaceName         string
	MarketplaceDisplayName  string
}

func NewPluginsCacheReady(response pluginapi.PluginListResponse) PluginsCacheState {
	clone := clonePluginListResponse(response)
	return PluginsCacheState{Kind: PluginsCacheReady, Response: &clone}
}

func NewPluginsCacheFailed(message string) PluginsCacheState {
	return PluginsCacheState{Kind: PluginsCacheFailed, Error: strings.TrimSpace(message)}
}

func (s *PluginsRuntimeState) PluginsCacheForCurrentCWD() PluginsCacheState {
	if s == nil || strings.TrimSpace(s.Fetch.CacheCWD) == "" || cleanPluginCWD(s.Fetch.CacheCWD) != cleanPluginCWD(s.CurrentCWD) {
		return PluginsCacheState{Kind: PluginsCacheUninitialized}
	}
	if s.Cache.Kind == "" {
		return PluginsCacheState{Kind: PluginsCacheUninitialized}
	}
	return clonePluginsCacheState(s.Cache)
}

func (s *PluginsRuntimeState) AddPluginsOutput(pluginsEnabled bool) PluginsRuntimeOutcome {
	if s == nil {
		return PluginsRuntimeOutcome{Ignored: true}
	}
	if !pluginsEnabled {
		return PluginsRuntimeOutcome{
			InfoMessage: "Plugins are disabled.",
			InfoHint:    "Enable the plugins feature to use /plugins.",
		}
	}
	s.ActiveTabID = AllPluginsTabID
	cache := s.PluginsCacheForCurrentCWD()
	switch cache.Kind {
	case PluginsCacheReady:
		return PluginsRuntimeOutcome{RefreshPopup: true, ActiveTabID: s.ActiveTabID}
	case PluginsCacheFailed:
		return PluginsRuntimeOutcome{ShowErrorPopup: true, ErrorMessage: cache.Error, ActiveTabID: s.ActiveTabID}
	default:
		return PluginsRuntimeOutcome{ShowLoadingPopup: true, ActiveTabID: s.ActiveTabID}
	}
}

func (s *PluginsRuntimeState) OnPluginsListFetchStarted(cwd string, remotePluginFeatureEnabled bool) PluginsRuntimeOutcome {
	if s == nil || cleanPluginCWD(s.CurrentCWD) != cleanPluginCWD(cwd) {
		return PluginsRuntimeOutcome{Ignored: true}
	}
	s.Fetch.InFlightCWD = strings.TrimSpace(cwd)
	s.Fetch.VerticalSectionRequested = !remotePluginFeatureEnabled
	if cleanPluginCWD(s.Fetch.CacheCWD) != cleanPluginCWD(cwd) {
		s.Cache = PluginsCacheState{Kind: PluginsCacheLoading}
	}
	return PluginsRuntimeOutcome{ShowLoadingPopup: s.Cache.Kind == PluginsCacheLoading}
}

func (s *PluginsRuntimeState) OnPluginsLoaded(cwd string, response *pluginapi.PluginListResponse, err string, popupActive bool, selectedIndexActive bool) PluginsRuntimeOutcome {
	if s == nil {
		return PluginsRuntimeOutcome{Ignored: true}
	}
	requestWasInFlight := cleanPluginCWD(s.Fetch.InFlightCWD) == cleanPluginCWD(cwd)
	if requestWasInFlight {
		s.Fetch.InFlightCWD = ""
	}
	if cleanPluginCWD(s.CurrentCWD) != cleanPluginCWD(cwd) {
		return PluginsRuntimeOutcome{Ignored: true}
	}

	cache := s.PluginsCacheForCurrentCWD()
	shouldRefresh := s.PluginInstallAuthFlow == nil &&
		(popupActive || selectedIndexActive || cache.Kind != PluginsCacheReady)
	if strings.TrimSpace(err) != "" || response == nil {
		s.RemoteSectionsLoading = false
		s.RemoteSectionsLoaded = false
		s.Fetch.VerticalSectionRequested = false
		if shouldRefresh {
			s.Fetch.CacheCWD = ""
			s.Cache = NewPluginsCacheFailed(err)
			return PluginsRuntimeOutcome{ShowErrorPopup: true, ErrorMessage: strings.TrimSpace(err)}
		}
		return PluginsRuntimeOutcome{}
	}

	cloned := clonePluginListResponse(*response)
	s.Fetch.CacheCWD = strings.TrimSpace(cwd)
	s.RemoteSectionsLoading = requestWasInFlight
	if requestWasInFlight {
		s.RemoteSectionsLoaded = false
	}
	s.RemoteSectionErrors = nil
	if tabID, ok := MarketplaceTabIDMatchingSavedID(s.ActiveTabID, cloned.Marketplaces); ok {
		s.ActiveTabID = tabID
	}
	if tabID, ok := MarketplaceTabIDMatchingSavedID(s.NewlyInstalledMarketplaceTabID, cloned.Marketplaces); ok {
		s.NewlyInstalledMarketplaceTabID = tabID
	} else {
		s.NewlyInstalledMarketplaceTabID = ""
	}
	s.Cache = NewPluginsCacheReady(cloned)
	out := PluginsRuntimeOutcome{RefreshPopup: shouldRefresh, ActiveTabID: s.ActiveTabID}
	s.NewlyInstalledMarketplaceTabID = ""
	return out
}

func (s *PluginsRuntimeState) OnPluginRemoteSectionsLoaded(cwd string, marketplaces []pluginapi.PluginMarketplaceEntry, sectionErrors []PluginCatalogRemoteSectionError, popupActive bool) PluginsRuntimeOutcome {
	if s == nil || cleanPluginCWD(s.CurrentCWD) != cleanPluginCWD(cwd) {
		return PluginsRuntimeOutcome{Ignored: true}
	}
	s.RemoteSectionsLoading = false
	s.RemoteSectionsLoaded = true
	s.Fetch.VerticalSectionRequested = false
	s.RemoteSectionErrors = clonePluginSectionErrors(sectionErrors)
	if s.Cache.Kind == PluginsCacheReady && cleanPluginCWD(s.Fetch.CacheCWD) == cleanPluginCWD(cwd) && s.Cache.Response != nil {
		MergeRemoteMarketplaces(s.Cache.Response, clonePluginMarketplaces(marketplaces))
		return PluginsRuntimeOutcome{RefreshPopup: popupActive}
	}
	return PluginsRuntimeOutcome{}
}

func (s *PluginsRuntimeState) OpenPluginsList(cwd string, response pluginapi.PluginListResponse, activeViewTabID string) PluginsRuntimeOutcome {
	if s == nil || cleanPluginCWD(s.CurrentCWD) != cleanPluginCWD(cwd) {
		return PluginsRuntimeOutcome{Ignored: true}
	}
	cache := s.PluginsCacheForCurrentCWD()
	if cache.Kind == PluginsCacheReady && cache.Response != nil {
		response = *cache.Response
	}
	s.Fetch.CacheCWD = strings.TrimSpace(cwd)
	s.Cache = NewPluginsCacheReady(response)
	s.ActiveTabID = firstNonEmptyPluginRuntime(activeViewTabID, s.ActiveTabID, AllPluginsTabID)
	return PluginsRuntimeOutcome{RefreshPopup: true, ActiveTabID: s.ActiveTabID}
}

func (s *PluginsRuntimeState) OnMarketplaceAddLoaded(cwd string, response *pluginapi.MarketplaceAddResponse, err string) PluginsRuntimeOutcome {
	if s == nil || cleanPluginCWD(s.CurrentCWD) != cleanPluginCWD(cwd) {
		return PluginsRuntimeOutcome{Ignored: true}
	}
	if strings.TrimSpace(err) != "" || response == nil {
		s.ActiveTabID = AddMarketplaceTabID
		return PluginsRuntimeOutcome{ShowMarketplaceError: true, ActiveTabID: s.ActiveTabID, ErrorMessage: strings.TrimSpace(err)}
	}
	tabID := MarketplaceTabIDFromPath(response.InstalledRoot)
	s.ActiveTabID = tabID
	if response.AlreadyAdded || response.AlreadyPresent {
		s.NewlyInstalledMarketplaceTabID = ""
	} else {
		s.NewlyInstalledMarketplaceTabID = tabID
	}
	message := "Added marketplace " + strings.TrimSpace(response.MarketplaceName) + "."
	if response.AlreadyAdded || response.AlreadyPresent {
		message = "Marketplace " + strings.TrimSpace(response.MarketplaceName) + " is already added."
	}
	return PluginsRuntimeOutcome{
		InfoMessage: message,
		InfoHint:    "Marketplace root: " + strings.TrimSpace(response.InstalledRoot),
		ActiveTabID: s.ActiveTabID,
	}
}

func (s *PluginsRuntimeState) OnMarketplaceRemoveLoaded(cwd string, marketplaceName string, marketplaceDisplayName string, response *pluginapi.MarketplaceRemoveResponse, err string) PluginsRuntimeOutcome {
	if s == nil || cleanPluginCWD(s.CurrentCWD) != cleanPluginCWD(cwd) {
		return PluginsRuntimeOutcome{Ignored: true}
	}
	if strings.TrimSpace(err) != "" || response == nil {
		return PluginsRuntimeOutcome{
			ShowMarketplaceError: true,
			ErrorMessage:         strings.TrimSpace(err),
			InfoMessage:          strings.TrimSpace(marketplaceName),
			InfoHint:             strings.TrimSpace(marketplaceDisplayName),
		}
	}
	s.ActiveTabID = AllPluginsTabID
	hint := "Removed marketplace config for " + strings.TrimSpace(response.MarketplaceName) + "."
	if response.InstalledRoot != nil && strings.TrimSpace(*response.InstalledRoot) != "" {
		hint = "Marketplace root: " + strings.TrimSpace(*response.InstalledRoot)
	}
	return PluginsRuntimeOutcome{
		InfoMessage: "Removed marketplace " + strings.TrimSpace(marketplaceDisplayName) + ".",
		InfoHint:    hint,
		ActiveTabID: s.ActiveTabID,
	}
}

func (s *PluginsRuntimeState) OnMarketplaceUpgradeLoaded(cwd string, response *pluginapi.MarketplaceUpgradeResponse, err string) PluginsRuntimeOutcome {
	if s == nil || cleanPluginCWD(s.CurrentCWD) != cleanPluginCWD(cwd) {
		return PluginsRuntimeOutcome{Ignored: true}
	}
	if strings.TrimSpace(err) != "" || response == nil {
		return PluginsRuntimeOutcome{ErrorMessage: strings.TrimSpace(err)}
	}
	if len(response.UpgradedRoots) == 1 {
		s.ActiveTabID = MarketplaceTabIDFromPath(response.UpgradedRoots[0])
	}
	selectedCount := len(response.SelectedMarketplaces)
	upgradedCount := len(response.UpgradedRoots)
	errorCount := len(response.Errors)
	switch {
	case selectedCount == 0:
		return PluginsRuntimeOutcome{
			InfoMessage: "No configured Git marketplaces to upgrade.",
			InfoHint:    "Only configured Git marketplaces can be upgraded.",
			ActiveTabID: s.ActiveTabID,
		}
	case upgradedCount == 0 && errorCount == 0:
		message := "Checked " + intString(selectedCount) + " marketplaces; all are already up to date."
		if selectedCount == 1 {
			message = "Marketplace " + response.SelectedMarketplaces[0] + " is already up to date."
		}
		return PluginsRuntimeOutcome{
			InfoMessage: message,
			InfoHint:    "Checked: " + strings.Join(response.SelectedMarketplaces, ", "),
			ActiveTabID: s.ActiveTabID,
		}
	case upgradedCount > 0:
		noun := "marketplaces"
		if upgradedCount == 1 {
			noun = "marketplace"
		}
		out := PluginsRuntimeOutcome{
			InfoMessage: "Upgraded " + intString(upgradedCount) + " " + noun + ".",
			InfoHint:    "Updated roots: " + strings.Join(response.UpgradedRoots, ", "),
			ActiveTabID: s.ActiveTabID,
		}
		if errorCount > 0 {
			out.ErrorMessage = marketplaceUpgradeErrorMessage(response.Errors)
		}
		return out
	default:
		return PluginsRuntimeOutcome{ErrorMessage: marketplaceUpgradeErrorMessage(response.Errors), ActiveTabID: s.ActiveTabID}
	}
}

func (s *PluginsRuntimeState) OnPluginEnabledSet(cwd string, pluginID string, enabled bool, err string) PluginsRuntimeOutcome {
	if s == nil || cleanPluginCWD(s.CurrentCWD) != cleanPluginCWD(cwd) {
		return PluginsRuntimeOutcome{Ignored: true}
	}
	if strings.TrimSpace(err) != "" {
		return PluginsRuntimeOutcome{
			ErrorMessage: "Failed to update plugin config for " + strings.TrimSpace(pluginID) + ": " + strings.TrimSpace(err),
			RefreshPopup: s.Cache.Kind == PluginsCacheReady,
		}
	}
	if s.Cache.Kind != PluginsCacheReady || s.Cache.Response == nil || cleanPluginCWD(s.Fetch.CacheCWD) != cleanPluginCWD(cwd) {
		return PluginsRuntimeOutcome{}
	}
	for marketplaceIndex := range s.Cache.Response.Marketplaces {
		for pluginIndex := range s.Cache.Response.Marketplaces[marketplaceIndex].Plugins {
			if strings.TrimSpace(s.Cache.Response.Marketplaces[marketplaceIndex].Plugins[pluginIndex].ID) == strings.TrimSpace(pluginID) {
				s.Cache.Response.Marketplaces[marketplaceIndex].Plugins[pluginIndex].Enabled = enabled
			}
		}
	}
	return PluginsRuntimeOutcome{RefreshPopup: true}
}

func (s *PluginsRuntimeState) HandlePluginsPopupKeyEvent(activeTabID string, event PluginsPopupKeyEvent, userConfiguredMarketplaces map[string]bool, gitConfiguredMarketplaces map[string]bool) PluginsPopupKeyDecision {
	if s == nil || (!event.CtrlR && !event.CtrlU) || strings.TrimSpace(activeTabID) == "" {
		return PluginsPopupKeyDecision{}
	}
	cache := s.PluginsCacheForCurrentCWD()
	if cache.Kind != PluginsCacheReady || cache.Response == nil {
		return PluginsPopupKeyDecision{}
	}
	for _, marketplace := range cache.Response.Marketplaces {
		if MarketplaceTabID(marketplace) != strings.TrimSpace(activeTabID) || !userConfiguredMarketplaces[strings.TrimSpace(marketplace.Name)] {
			continue
		}
		if event.CtrlR {
			return PluginsPopupKeyDecision{
				Handled:                true,
				OpenRemoveConfirmation: true,
				MarketplaceName:        strings.TrimSpace(marketplace.Name),
				MarketplaceDisplayName: MarketplaceDisplayName(marketplace),
			}
		}
		if marketplace.Path == nil || !gitConfiguredMarketplaces[strings.TrimSpace(marketplace.Name)] {
			return PluginsPopupKeyDecision{}
		}
		decision := PluginsPopupKeyDecision{
			Handled:                true,
			MarketplaceName:        strings.TrimSpace(marketplace.Name),
			MarketplaceDisplayName: MarketplaceDisplayName(marketplace),
		}
		if event.Press {
			decision.OpenMarketplaceUpgrade = true
			decision.FetchMarketplaceUpgrade = true
		}
		return decision
	}
	return PluginsPopupKeyDecision{}
}

func (s *PluginsRuntimeState) OnPluginInstallLoaded(cwd string, pluginDisplayName string, response *pluginapi.PluginInstallResponse, err string) PluginsRuntimeOutcome {
	if s == nil || cleanPluginCWD(s.CurrentCWD) != cleanPluginCWD(cwd) {
		return PluginsRuntimeOutcome{Ignored: true}
	}
	if strings.TrimSpace(err) != "" || response == nil {
		s.PluginInstallAppsNeedingAuth = nil
		s.PluginInstallAuthFlow = nil
		return PluginsRuntimeOutcome{ShowErrorPopup: true, ErrorMessage: strings.TrimSpace(err)}
	}
	s.PluginInstallAppsNeedingAuth = clonePluginApps(response.AppsNeedingAuth)
	s.PluginInstallAuthFlow = nil
	if len(s.PluginInstallAppsNeedingAuth) == 0 {
		return PluginsRuntimeOutcome{
			InfoMessage: "Installed " + strings.TrimSpace(pluginDisplayName) + " plugin.",
			InfoHint:    "No additional app authentication is required.",
		}
	}
	names := make([]string, 0, len(s.PluginInstallAppsNeedingAuth))
	for _, app := range s.PluginInstallAppsNeedingAuth {
		names = append(names, strings.TrimSpace(app.Name))
	}
	s.PluginInstallAuthFlow = &PluginInstallAuthFlowState{PluginDisplayName: strings.TrimSpace(pluginDisplayName), NextAppIndex: 0}
	return PluginsRuntimeOutcome{
		InfoMessage:   "Installed " + strings.TrimSpace(pluginDisplayName) + " plugin.",
		InfoHint:      intString(len(s.PluginInstallAppsNeedingAuth)) + " app(s) still need authentication: " + strings.Join(names, ", "),
		OpenAuthPopup: true,
	}
}

func (s *PluginsRuntimeState) CurrentPluginInstallAuthView(installedAppIDs map[string]bool) (SelectionView, bool) {
	if s == nil || s.PluginInstallAuthFlow == nil {
		return SelectionView{}, false
	}
	return PluginInstallAuthPopupView(*s.PluginInstallAuthFlow, s.PluginInstallAppsNeedingAuth, installedAppIDs)
}

func (s *PluginsRuntimeState) AdvancePluginInstallAuthFlow() PluginsRuntimeOutcome {
	if s == nil || s.PluginInstallAuthFlow == nil {
		return PluginsRuntimeOutcome{Ignored: true}
	}
	s.PluginInstallAuthFlow.NextAppIndex++
	if s.PluginInstallAuthFlow.NextAppIndex >= len(s.PluginInstallAppsNeedingAuth) {
		return s.FinishPluginInstallAuthFlow(false)
	}
	return PluginsRuntimeOutcome{OpenAuthPopup: true}
}

func (s *PluginsRuntimeState) AbandonPluginInstallAuthFlow() PluginsRuntimeOutcome {
	return s.FinishPluginInstallAuthFlow(true)
}

func (s *PluginsRuntimeState) FinishPluginInstallAuthFlow(abandoned bool) PluginsRuntimeOutcome {
	if s == nil || s.PluginInstallAuthFlow == nil {
		return PluginsRuntimeOutcome{Ignored: true}
	}
	displayName := s.PluginInstallAuthFlow.PluginDisplayName
	s.PluginInstallAuthFlow = nil
	s.PluginInstallAppsNeedingAuth = nil
	if abandoned {
		return PluginsRuntimeOutcome{
			FinishedAuthFlow: true,
			InfoMessage:      "Skipped remaining app setup for " + displayName + " plugin.",
			InfoHint:         "The plugin may not be usable until required apps are installed.",
			RefreshPopup:     s.Cache.Kind == PluginsCacheReady,
		}
	}
	return PluginsRuntimeOutcome{
		FinishedAuthFlow: true,
		InfoMessage:      "Completed app setup flow for " + displayName + " plugin.",
		InfoHint:         "You can now continue managing plugins from /plugins.",
		RefreshPopup:     s.Cache.Kind == PluginsCacheReady,
	}
}

func (s *PluginsRuntimeState) OnPluginUninstallLoaded(cwd string, pluginDisplayName string, err string) PluginsRuntimeOutcome {
	if s == nil || cleanPluginCWD(s.CurrentCWD) != cleanPluginCWD(cwd) {
		return PluginsRuntimeOutcome{Ignored: true}
	}
	if strings.TrimSpace(err) != "" {
		return PluginsRuntimeOutcome{ShowErrorPopup: true, ErrorMessage: strings.TrimSpace(err)}
	}
	s.PluginInstallAppsNeedingAuth = nil
	s.PluginInstallAuthFlow = nil
	return PluginsRuntimeOutcome{
		InfoMessage: "Uninstalled " + strings.TrimSpace(pluginDisplayName) + " plugin.",
		InfoHint:    "Bundled apps remain installed.",
	}
}

func cleanPluginCWD(value string) string {
	return strings.TrimSpace(value)
}

func clonePluginsCacheState(state PluginsCacheState) PluginsCacheState {
	out := state
	if state.Response != nil {
		cloned := clonePluginListResponse(*state.Response)
		out.Response = &cloned
	}
	return out
}

func clonePluginListResponse(response pluginapi.PluginListResponse) pluginapi.PluginListResponse {
	out := response
	out.Marketplaces = clonePluginMarketplaces(response.Marketplaces)
	out.MarketplaceLoadErrors = append([]pluginapi.MarketplaceLoadErrorInfo(nil), response.MarketplaceLoadErrors...)
	out.FeaturedPluginIDs = append([]string(nil), response.FeaturedPluginIDs...)
	out.Plugins = append([]pluginapi.PluginSummary(nil), response.Plugins...)
	return out
}

func clonePluginMarketplaces(marketplaces []pluginapi.PluginMarketplaceEntry) []pluginapi.PluginMarketplaceEntry {
	out := make([]pluginapi.PluginMarketplaceEntry, len(marketplaces))
	for i, marketplace := range marketplaces {
		out[i] = marketplace
		if marketplace.Path != nil {
			path := *marketplace.Path
			out[i].Path = &path
		}
		out[i].Plugins = append([]pluginapi.PluginSummary(nil), marketplace.Plugins...)
	}
	return out
}

func clonePluginApps(apps []pluginapi.AppSummary) []pluginapi.AppSummary {
	out := make([]pluginapi.AppSummary, len(apps))
	copy(out, apps)
	return out
}

func clonePluginSectionErrors(errors []PluginCatalogRemoteSectionError) []PluginCatalogRemoteSectionError {
	out := make([]PluginCatalogRemoteSectionError, len(errors))
	copy(out, errors)
	return out
}

func marketplaceUpgradeErrorMessage(errors []pluginapi.MarketplaceUpgradeErrorInfo) string {
	if len(errors) == 0 {
		return ""
	}
	noun := "marketplaces"
	if len(errors) == 1 {
		noun = "marketplace"
	}
	parts := make([]string, 0, len(errors))
	for _, err := range errors {
		parts = append(parts, strings.TrimSpace(err.MarketplaceName)+": "+strings.TrimSpace(err.Message))
	}
	return "Failed to upgrade " + intString(len(errors)) + " " + noun + ": " + strings.Join(parts, "; ")
}

func firstNonEmptyPluginRuntime(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
