package chatwidget

import (
	"sort"
	"strings"

	pluginapi "codex_go/internal/plugin"
	"codex_go/internal/utils"
)

const (
	PluginsSelectionViewID        = "plugins-selection"
	AllPluginsTabID               = "all-plugins"
	AddMarketplaceTabID           = "add-marketplace"
	InstalledPluginsTabID         = "installed-plugins"
	MarketplaceTabIDPrefix        = "marketplace:"
	OpenAICuratedTabID            = "marketplace:openai-curated"
	RemoteLoadingTabIDPrefix      = "remote-loading:"
	RemoteEmptyTabIDPrefix        = "remote-empty:"
	RemoteErrorTabIDPrefix        = "remote-error:"
	PersonalMarketplaceRelPath    = ".agents/plugins/marketplace.json"
	OpenAICuratedMarketplaceName  = "openai-curated"
	RemoteGlobalMarketplaceName   = "openai-curated-remote"
	RemoteCreatedByMeMarketplace  = "created-by-me-remote"
	RemoteWorkspaceMarketplace    = "workspace-directory"
	RemoteWorkspaceSharedWithMe   = "workspace-shared-with-me"
	RemoteWorkspaceSharedPrivate  = "workspace-shared-with-me-private"
	RemoteWorkspaceSharedUnlisted = "workspace-shared-with-me-unlisted"
	PluginRowPrefixWidth          = 6
	PluginCatalogAppsHelpURL      = "https://help.openai.com/en/articles/11487775-apps-in-chatgpt"
)

const pluginSummarySeparator = " · "

const (
	PluginMenuActionOpenDetails        UsageMenuAction = "plugin_open_details"
	PluginMenuActionInstall            UsageMenuAction = "plugin_install"
	PluginMenuActionUninstall          UsageMenuAction = "plugin_uninstall"
	PluginMenuActionToggleEnabled      UsageMenuAction = "plugin_toggle_enabled"
	PluginMenuActionBackToPlugins      UsageMenuAction = "plugin_back_to_plugins"
	PluginMenuActionOpenAppInstallURL  UsageMenuAction = "plugin_open_app_install_url"
	PluginMenuActionAuthFlowAdvance    UsageMenuAction = "plugin_auth_flow_advance"
	PluginMenuActionAuthFlowAbandon    UsageMenuAction = "plugin_auth_flow_abandon"
	PluginMenuActionAddMarketplace     UsageMenuAction = "plugin_add_marketplace"
	PluginMenuActionRemoveMarketplace  UsageMenuAction = "plugin_remove_marketplace"
	PluginMenuActionUpgradeMarketplace UsageMenuAction = "plugin_upgrade_marketplace"
)

type MarketplaceProduct string

const (
	MarketplaceProductOpenAICurated MarketplaceProduct = "openai_curated"
	MarketplaceProductWorkspace     MarketplaceProduct = "workspace"
	MarketplaceProductSharedWithMe  MarketplaceProduct = "shared_with_me"
	MarketplaceProductSharedLink    MarketplaceProduct = "shared_with_me_link"
	MarketplaceProductLocal         MarketplaceProduct = "local"
	MarketplaceProductOther         MarketplaceProduct = "other"
)

func MarketplaceProductFromEntry(marketplace pluginapi.PluginMarketplaceEntry) MarketplaceProduct {
	return MarketplaceProductFromParts(marketplace.Name, marketplace.Path)
}

func MarketplaceProductFromParts(marketplaceName string, marketplacePath *string) MarketplaceProduct {
	if marketplacePath != nil && IsPersonalMarketplacePath(*marketplacePath) {
		return MarketplaceProductLocal
	}
	return MarketplaceProductFromName(marketplaceName)
}

func MarketplaceProductFromName(marketplaceName string) MarketplaceProduct {
	marketplaceName = strings.TrimSpace(marketplaceName)
	if IsOpenAICuratedMarketplaceName(marketplaceName) || marketplaceName == RemoteGlobalMarketplaceName {
		return MarketplaceProductOpenAICurated
	}
	switch marketplaceName {
	case RemoteWorkspaceMarketplace:
		return MarketplaceProductWorkspace
	case RemoteWorkspaceSharedWithMe, RemoteWorkspaceSharedPrivate:
		return MarketplaceProductSharedWithMe
	case RemoteWorkspaceSharedUnlisted:
		return MarketplaceProductSharedLink
	default:
		return MarketplaceProductOther
	}
}

func (p MarketplaceProduct) Label() string {
	switch p {
	case MarketplaceProductOpenAICurated:
		return "OpenAI Curated"
	case MarketplaceProductWorkspace:
		return "Workspace"
	case MarketplaceProductSharedWithMe:
		return "Shared with me"
	case MarketplaceProductSharedLink:
		return "Shared with me (link)"
	case MarketplaceProductLocal:
		return "Local"
	default:
		return ""
	}
}

func (p MarketplaceProduct) TabOrder() int {
	switch p {
	case MarketplaceProductWorkspace:
		return 0
	case MarketplaceProductSharedWithMe:
		return 1
	case MarketplaceProductSharedLink:
		return 2
	case MarketplaceProductLocal:
		return 3
	default:
		return 4
	}
}

func (p MarketplaceProduct) IsByOpenAI() bool {
	return p == MarketplaceProductOpenAICurated
}

type PluginCatalogEntry struct {
	Marketplace pluginapi.PluginMarketplaceEntry
	Plugin      pluginapi.PluginSummary
	DisplayName string
}

type PreferredLocalPluginSource struct {
	MarketplacePath string
	PluginName      string
	Installed       bool
	InstallPolicy   pluginapi.PluginInstallPolicy
}

type PluginLocationKind string

const (
	PluginLocationLocal  PluginLocationKind = "local"
	PluginLocationRemote PluginLocationKind = "remote"
)

type PluginLocation struct {
	Kind            PluginLocationKind
	MarketplacePath string
	MarketplaceName string
}

type PluginDetailRequest struct {
	Location   PluginLocation
	PluginName string
	ReadParams pluginapi.PluginReadParams
}

type PluginCatalogPopupOptions struct {
	ActiveTabID                    string
	InitialSelectedIndex           int
	RemoteSectionsLoading          bool
	RemoteSectionsLoaded           bool
	VerticalSectionRequested       bool
	SectionErrors                  []PluginCatalogRemoteSectionError
	CanRemoveMarketplaces          map[string]bool
	CanUpgradeMarketplaces         map[string]bool
	NewlyInstalledMarketplaceTabID string
}

type PluginCatalogPopupModel struct {
	ViewID               string
	FooterHint           string
	TabFooterHints       map[string]string
	Tabs                 []PluginCatalogTabModel
	InitialTabID         string
	InitialSelectedIndex int
	Searchable           bool
	SearchPlaceholder    string
	NameColumnWidth      int
}

type PluginCatalogTabModel struct {
	ID          string
	Label       string
	Order       int
	HeaderLines []string
	FooterHint  string
	Items       []PluginSelectionItemModel
}

type PluginSelectionToggle struct {
	IsOn     bool
	PluginID string
}

type PluginSelectionItemModel struct {
	SelectionItem
	Toggle            *PluginSelectionToggle
	TogglePlaceholder string
	DetailRequest     *PluginDetailRequest
	CanViewDetails    bool
	CanToggle         bool
	InstallParams     *pluginapi.PluginInstallParams
	UninstallID       string
}

func IsOpenAICuratedMarketplaceName(marketplaceName string) bool {
	return strings.TrimSpace(marketplaceName) == OpenAICuratedMarketplaceName
}

func IsPersonalMarketplacePath(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	cleaned := utils.CrossPlatformSlash(path)
	return cleaned == PersonalMarketplaceRelPath || strings.HasSuffix(cleaned, "/"+PersonalMarketplaceRelPath)
}

func MarketplaceDisplayName(marketplace pluginapi.PluginMarketplaceEntry) string {
	if label := MarketplaceProductFromEntry(marketplace).Label(); label != "" {
		return label
	}
	if displayName := marketplaceInterfaceDisplayName(marketplace.Interface); displayName != "" {
		return displayName
	}
	return strings.TrimSpace(marketplace.Name)
}

func MarketplaceTabID(marketplace pluginapi.PluginMarketplaceEntry) string {
	if marketplace.Path != nil {
		return MarketplaceTabIDFromPath(*marketplace.Path)
	}
	return MarketplaceTabIDPrefix + strings.TrimSpace(marketplace.Name)
}

func MarketplaceTabIDFromPath(path string) string {
	return MarketplaceTabIDPrefix + strings.TrimSpace(path)
}

func MarketplaceTabIDMatchingSavedID(savedTabID string, marketplaces []pluginapi.PluginMarketplaceEntry) (string, bool) {
	savedTabID = strings.TrimSpace(savedTabID)
	for _, marketplace := range marketplaces {
		tabID := MarketplaceTabID(marketplace)
		if tabID == savedTabID {
			return tabID, true
		}
	}
	root := strings.TrimPrefix(savedTabID, MarketplaceTabIDPrefix)
	if root == savedTabID || strings.TrimSpace(root) == "" {
		return "", false
	}
	root = utils.CrossPlatformSlash(root)
	for _, marketplace := range marketplaces {
		if marketplace.Path == nil {
			continue
		}
		path := utils.CrossPlatformSlash(*marketplace.Path)
		if path == root || strings.HasPrefix(path, root+"/") {
			return MarketplaceTabID(marketplace), true
		}
	}
	return "", false
}

func MergeRemoteMarketplaces(response *pluginapi.PluginListResponse, remoteMarketplaces []pluginapi.PluginMarketplaceEntry) {
	if response == nil {
		return
	}
	remoteNames := map[string]bool{}
	for _, marketplace := range remoteMarketplaces {
		remoteNames[strings.TrimSpace(marketplace.Name)] = true
	}
	remoteCuratedPresent := remoteNames[RemoteGlobalMarketplaceName]
	kept := response.Marketplaces[:0]
	for _, marketplace := range response.Marketplaces {
		if remoteCuratedPresent && marketplace.Path != nil && IsOpenAICuratedMarketplaceName(marketplace.Name) {
			continue
		}
		isRemoteSection := remoteMarketplaceSectionName(marketplace.Name)
		if marketplace.Path == nil && (isRemoteSection || remoteNames[marketplace.Name]) {
			continue
		}
		kept = append(kept, marketplace)
	}
	response.Marketplaces = append(kept, remoteMarketplaces...)
}

func PluginEntriesForMarketplaces(marketplaces []pluginapi.PluginMarketplaceEntry) []PluginCatalogEntry {
	entries := make([]PluginCatalogEntry, 0)
	for _, marketplace := range marketplaces {
		for _, plugin := range marketplace.Plugins {
			if strings.TrimSpace(plugin.MarketplaceName) == "" {
				plugin.MarketplaceName = strings.TrimSpace(marketplace.Name)
			}
			entries = append(entries, PluginCatalogEntry{
				Marketplace: marketplace,
				Plugin:      plugin,
				DisplayName: PluginDisplayName(plugin),
			})
		}
	}
	return DedupePluginEntries(entries)
}

func DedupePluginEntries(entries []PluginCatalogEntry) []PluginCatalogEntry {
	deduped := make([]PluginCatalogEntry, 0, len(entries))
	remoteIndexes := map[string]int{}
	for _, entry := range entries {
		remoteID := PluginRemoteIdentity(entry.Plugin)
		if remoteID == "" {
			deduped = append(deduped, entry)
			continue
		}
		if existingIndex, ok := remoteIndexes[remoteID]; ok {
			if PluginEntryPreferred(entry, deduped[existingIndex]) {
				deduped[existingIndex] = entry
			}
			continue
		}
		remoteIndexes[remoteID] = len(deduped)
		deduped = append(deduped, entry)
	}
	return deduped
}

func PluginEntryPreferred(candidate PluginCatalogEntry, existing PluginCatalogEntry) bool {
	if candidate.Plugin.Installed != existing.Plugin.Installed {
		return candidate.Plugin.Installed
	}
	candidateAdminManaged := candidate.Plugin.InstallPolicy == pluginapi.InstallInstalledByDefault
	existingAdminManaged := existing.Plugin.InstallPolicy == pluginapi.InstallInstalledByDefault
	if candidateAdminManaged != existingAdminManaged {
		return candidateAdminManaged
	}
	candidateLocalShare := candidate.Plugin.ShareContext != nil && !pluginSourceIsRemote(candidate.Plugin.Source)
	existingLocalShare := existing.Plugin.ShareContext != nil && !pluginSourceIsRemote(existing.Plugin.Source)
	if candidateLocalShare != existingLocalShare {
		return candidateLocalShare
	}
	return !pluginSourceIsRemote(candidate.Plugin.Source) && pluginSourceIsRemote(existing.Plugin.Source)
}

func PreferredLocalPluginSources(marketplaces []pluginapi.PluginMarketplaceEntry) map[string]PreferredLocalPluginSource {
	sources := map[string]PreferredLocalPluginSource{}
	for _, marketplace := range marketplaces {
		if marketplace.Path == nil || strings.TrimSpace(*marketplace.Path) == "" {
			continue
		}
		for _, plugin := range marketplace.Plugins {
			if pluginSourceIsRemote(plugin.Source) || plugin.ShareContext == nil {
				continue
			}
			remoteID := strings.TrimSpace(plugin.ShareContext.RemotePluginID)
			if remoteID == "" {
				continue
			}
			if _, exists := sources[remoteID]; exists {
				continue
			}
			sources[remoteID] = PreferredLocalPluginSource{
				MarketplacePath: strings.TrimSpace(*marketplace.Path),
				PluginName:      strings.TrimSpace(plugin.Name),
				Installed:       plugin.Installed,
				InstallPolicy:   plugin.InstallPolicy,
			}
		}
	}
	return sources
}

func PluginDetailRequestForEntry(marketplace pluginapi.PluginMarketplaceEntry, plugin pluginapi.PluginSummary, preferredLocalSources map[string]PreferredLocalPluginSource) (PluginDetailRequest, bool) {
	if pluginSourceIsRemote(plugin.Source) {
		remoteID := PluginRemoteIdentity(plugin)
		if remoteID != "" {
			if preferred, ok := preferredLocalSources[remoteID]; ok &&
				preferred.Installed == plugin.Installed &&
				preferred.InstallPolicy == plugin.InstallPolicy {
				return newPluginDetailRequest(
					PluginLocation{Kind: PluginLocationLocal, MarketplacePath: preferred.MarketplacePath},
					preferred.PluginName,
					remoteID,
				), true
			}
		}
	}
	location, ok := PluginLocationForMarketplace(marketplace, plugin)
	if !ok {
		return PluginDetailRequest{}, false
	}
	return newPluginDetailRequest(location, PluginRequestName(plugin), PluginRemoteIdentity(plugin)), true
}

func PluginLocationForMarketplace(marketplace pluginapi.PluginMarketplaceEntry, plugin pluginapi.PluginSummary) (PluginLocation, bool) {
	if marketplace.Path != nil && strings.TrimSpace(*marketplace.Path) != "" {
		return PluginLocation{Kind: PluginLocationLocal, MarketplacePath: strings.TrimSpace(*marketplace.Path)}, true
	}
	if PluginRemoteIdentity(plugin) != "" {
		return PluginLocation{Kind: PluginLocationRemote, MarketplaceName: strings.TrimSpace(marketplace.Name)}, true
	}
	return PluginLocation{}, false
}

func PluginDetailLocation(detail pluginapi.PluginDetail) (PluginLocation, bool) {
	if detail.MarketplacePath != nil && strings.TrimSpace(*detail.MarketplacePath) != "" {
		return PluginLocation{Kind: PluginLocationLocal, MarketplacePath: strings.TrimSpace(*detail.MarketplacePath)}, true
	}
	if PluginRemoteIdentity(detail.Summary) != "" {
		return PluginLocation{Kind: PluginLocationRemote, MarketplaceName: strings.TrimSpace(detail.MarketplaceName)}, true
	}
	return PluginLocation{}, false
}

func PluginRequestName(plugin pluginapi.PluginSummary) string {
	if pluginSourceIsRemote(plugin.Source) {
		if remoteID := PluginRemoteIdentity(plugin); remoteID != "" {
			return remoteID
		}
	}
	return strings.TrimSpace(plugin.Name)
}

func PluginRemoteIdentity(plugin pluginapi.PluginSummary) string {
	if plugin.ShareContext != nil && strings.TrimSpace(plugin.ShareContext.RemotePluginID) != "" {
		return strings.TrimSpace(plugin.ShareContext.RemotePluginID)
	}
	return strings.TrimSpace(plugin.RemotePluginID)
}

func PluginUninstallID(plugin pluginapi.PluginSummary) (string, bool) {
	if pluginSourceIsRemote(plugin.Source) {
		remoteID := PluginRemoteIdentity(plugin)
		return remoteID, remoteID != ""
	}
	id := strings.TrimSpace(plugin.ID)
	return id, id != ""
}

func PluginDisplayName(plugin pluginapi.PluginSummary) string {
	if plugin.Interface != nil && plugin.Interface.DisplayName != nil {
		if value := strings.TrimSpace(*plugin.Interface.DisplayName); value != "" {
			return value
		}
	}
	return strings.TrimSpace(plugin.Name)
}

func PluginDescription(plugin pluginapi.PluginSummary) (string, bool) {
	if plugin.Interface == nil {
		return "", false
	}
	for _, candidate := range []*string{plugin.Interface.ShortDescription, plugin.Interface.LongDescription} {
		if candidate == nil {
			continue
		}
		if value := strings.TrimSpace(*candidate); value != "" {
			return value, true
		}
	}
	return "", false
}

func PluginStatusLabel(plugin pluginapi.PluginSummary) string {
	if plugin.Availability == pluginapi.PluginDisabledByAdmin {
		return "Disabled"
	}
	if !plugin.Installed && plugin.InstallPolicy == pluginapi.InstallInstalledByDefault {
		return "Admin assigned"
	}
	if plugin.Installed {
		if plugin.Enabled {
			return "Installed"
		}
		return "Disabled"
	}
	switch plugin.InstallPolicy {
	case pluginapi.InstallBlocked:
		return "Not installable"
	case pluginapi.InstallInstalledByDefault:
		return "Installed"
	default:
		return "Available"
	}
}

func PluginBriefDescription(plugin pluginapi.PluginSummary, marketplaceLabel string, statusLabelWidth int) string {
	status := padRight(PluginStatusLabel(plugin), statusLabelWidth)
	parts := []string{status}
	if value := strings.TrimSpace(marketplaceLabel); value != "" {
		parts = append(parts, value)
	}
	if description, ok := PluginDescription(plugin); ok {
		parts = append(parts, description)
	}
	return strings.Join(parts, pluginSummarySeparator)
}

func PluginBriefDescriptionWithoutMarketplace(plugin pluginapi.PluginSummary, statusLabelWidth int) string {
	status := padRight(PluginStatusLabel(plugin), statusLabelWidth)
	if description, ok := PluginDescription(plugin); ok {
		return status + pluginSummarySeparator + description
	}
	return status
}

func PluginDetailStatusLabel(plugin pluginapi.PluginSummary) string {
	if plugin.Availability == pluginapi.PluginDisabledByAdmin {
		return "Disabled by admin"
	}
	if plugin.InstallPolicy == pluginapi.InstallInstalledByDefault {
		if plugin.Installed {
			return "Installed by admin"
		}
		return "Enabled by Admin"
	}
	if plugin.Installed {
		if plugin.Enabled {
			return "Installed"
		}
		return "Disabled"
	}
	if plugin.InstallPolicy == pluginapi.InstallBlocked {
		return "Not installable"
	}
	return "Can be installed"
}

func PluginSourceSummary(detail pluginapi.PluginDetail) string {
	source := detail.Summary.Source
	switch strings.ToLower(strings.TrimSpace(source.Type)) {
	case "local", "":
		return "Local"
	case "git":
		if source.RefName != nil && strings.TrimSpace(*source.RefName) != "" {
			return "Git" + pluginSummarySeparator + strings.TrimSpace(source.URL) + "@" + strings.TrimSpace(*source.RefName)
		}
		return "Git" + pluginSummarySeparator + strings.TrimSpace(source.URL)
	case "npm":
		if source.Version != nil && strings.TrimSpace(*source.Version) != "" {
			return "npm" + pluginSummarySeparator + strings.TrimSpace(source.Package) + "@" + strings.TrimSpace(*source.Version)
		}
		return "npm" + pluginSummarySeparator + strings.TrimSpace(source.Package)
	case "remote":
		label := MarketplaceProductFromName(detail.MarketplaceName).Label()
		if label == "" {
			label = strings.TrimSpace(detail.MarketplaceName)
		}
		return "Remote" + pluginSummarySeparator + label
	default:
		return strings.TrimSpace(source.Type)
	}
}

func PluginAuthPolicySummary(authPolicy pluginapi.PluginAuthPolicy) string {
	if authPolicy == pluginapi.AuthOnInstall || authPolicy == pluginapi.AuthRequired {
		return "Auth on install"
	}
	return "Auth on use"
}

func PluginVersionSummary(plugin pluginapi.PluginSummary) (string, bool) {
	parts := []string{}
	if plugin.LocalVersion != nil && strings.TrimSpace(*plugin.LocalVersion) != "" {
		parts = append(parts, "local "+strings.TrimSpace(*plugin.LocalVersion))
	}
	if plugin.ShareContext != nil && plugin.ShareContext.RemoteVersion != nil && strings.TrimSpace(*plugin.ShareContext.RemoteVersion) != "" {
		parts = append(parts, "remote "+strings.TrimSpace(*plugin.ShareContext.RemoteVersion))
	}
	if len(parts) == 0 {
		return "", false
	}
	return strings.Join(parts, pluginSummarySeparator), true
}

func PluginShareContextSummary(context *pluginapi.PluginShareContext) string {
	if context == nil {
		return ""
	}
	parts := []string{}
	if context.Discoverability != nil {
		if label := PluginShareDiscoverabilityLabel(*context.Discoverability); label != "" {
			parts = append(parts, label)
		}
	}
	if creator := PluginShareCreatorSummary(context); creator != "" {
		parts = append(parts, creator)
	}
	if context.SharePrincipals != nil {
		parts = append(parts, PluginSharePrincipalsSummary(context.SharePrincipals))
	}
	if context.ShareURL != nil && strings.TrimSpace(*context.ShareURL) != "" {
		parts = append(parts, strings.TrimSpace(*context.ShareURL))
	}
	if len(parts) == 0 {
		return "Remote ID " + strings.TrimSpace(context.RemotePluginID)
	}
	return strings.Join(parts, pluginSummarySeparator)
}

func PluginShareDiscoverabilityLabel(discoverability string) string {
	switch strings.ToUpper(strings.TrimSpace(discoverability)) {
	case string(pluginapi.PluginShareDiscoverabilityListed):
		return "Listed"
	case string(pluginapi.PluginShareDiscoverabilityUnlisted):
		return "Workspace link"
	case string(pluginapi.PluginShareDiscoverabilityPrivate):
		return "Private"
	default:
		return ""
	}
}

func PluginShareCreatorSummary(context *pluginapi.PluginShareContext) string {
	if context == nil {
		return ""
	}
	name := stringValue(context.CreatorName)
	accountID := stringValue(context.CreatorAccountUserID)
	switch {
	case name != "" && accountID != "":
		return "creator " + name + " (" + accountID + ")"
	case name != "":
		return "creator " + name
	case accountID != "":
		return "creator account " + accountID
	default:
		return ""
	}
}

func PluginSharePrincipalsSummary(principals []pluginapi.PluginSharePrincipal) string {
	switch len(principals) {
	case 0:
		return "No explicit principals"
	case 1:
		return "1 principal: " + strings.TrimSpace(principals[0].Name)
	default:
		return intString(len(principals)) + " principals"
	}
}

func PluginSkillSummary(detail pluginapi.PluginDetail) string {
	if len(detail.Skills) == 0 {
		return "No plugin skills."
	}
	names := make([]string, 0, len(detail.Skills))
	for _, skill := range detail.Skills {
		if name := strings.TrimSpace(skill.Name); name != "" {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return "No plugin skills."
	}
	return strings.Join(names, ", ")
}

func PluginAppSummary(detail pluginapi.PluginDetail) string {
	if len(detail.Apps) == 0 {
		return "No plugin apps."
	}
	names := make([]string, 0, len(detail.Apps))
	for _, app := range detail.Apps {
		if name := strings.TrimSpace(app.Name); name != "" {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return "No plugin apps."
	}
	return strings.Join(names, ", ")
}

func PluginHookSummary(detail pluginapi.PluginDetail) string {
	if len(detail.Hooks) == 0 {
		return "No plugin hooks."
	}
	counts := map[string]int{}
	order := []string{}
	for _, hook := range detail.Hooks {
		eventName := strings.TrimSpace(hook.EventName)
		if eventName == "" {
			eventName = strings.TrimSpace(hook.Key)
		}
		if eventName == "" {
			eventName = "unknown"
		}
		if _, ok := counts[eventName]; !ok {
			order = append(order, eventName)
		}
		counts[eventName]++
	}
	parts := make([]string, 0, len(order))
	for _, eventName := range order {
		parts = append(parts, eventName+" ("+intString(counts[eventName])+")")
	}
	return strings.Join(parts, ", ")
}

func PluginMCPSummary(detail pluginapi.PluginDetail) string {
	if len(detail.MCPServers) == 0 {
		return "No plugin MCP servers."
	}
	return strings.Join(detail.MCPServers, ", ")
}

type PluginInstallAuthFlowState struct {
	PluginDisplayName string
	NextAppIndex      int
}

func PluginInstallAuthPopupView(flow PluginInstallAuthFlowState, appsNeedingAuth []pluginapi.AppSummary, installedAppIDs map[string]bool) (SelectionView, bool) {
	if flow.NextAppIndex < 0 || flow.NextAppIndex >= len(appsNeedingAuth) {
		return SelectionView{}, false
	}
	app := appsNeedingAuth[flow.NextAppIndex]
	current := flow.NextAppIndex + 1
	total := len(appsNeedingAuth)
	isInstalled := installedAppIDs[strings.TrimSpace(app.ID)]
	statusLabel := "Install the required Apps in ChatGPT to continue:"
	if isInstalled {
		statusLabel = "Already installed in this session."
	}
	installName := "Install on ChatGPT"
	if isInstalled {
		installName = "Manage on ChatGPT"
	}
	items := []SelectionItem{}
	if app.InstallURL != nil && strings.TrimSpace(*app.InstallURL) != "" {
		items = append(items, SelectionItem{
			Name:                installName,
			Description:         "Open the ChatGPT app management page",
			SelectedDescription: "Open the app page in your browser.",
			Action:              PluginMenuActionOpenAppInstallURL,
		})
	} else {
		items = append(items, SelectionItem{
			Name:        "ChatGPT apps link unavailable",
			Description: "This app did not provide an install/manage URL.",
			Disabled:    true,
		})
	}
	if isInstalled {
		items = append(items, SelectionItem{
			Name:                "Continue",
			Description:         "This app is already installed.",
			SelectedDescription: "Advance to the next app.",
			Action:              PluginMenuActionAuthFlowAdvance,
			DismissOnSelect:     true,
		})
	} else {
		items = append(items, SelectionItem{
			Name:                "I've installed it",
			Description:         "Trust your confirmation and continue to the next app.",
			SelectedDescription: "Continue without waiting for refresh to complete.",
			Action:              PluginMenuActionAuthFlowAdvance,
			DismissOnSelect:     true,
		})
	}
	items = append(items, SelectionItem{
		Name:                "Skip remaining app setup",
		Description:         "Stop this follow-up flow for this plugin.",
		SelectedDescription: "Abandon remaining required app setup.",
		Action:              PluginMenuActionAuthFlowAbandon,
		DismissOnSelect:     true,
	})
	return SelectionView{
		ViewID:      PluginsSelectionViewID,
		Title:       "Plugins",
		Subtitle:    strings.TrimSpace(flow.PluginDisplayName) + " plugin installed.",
		FooterHint:  PluginDetailHintLine(),
		AllowCancel: true,
		Items: append([]SelectionItem{{
			Name:        "App setup " + intString(current) + "/" + intString(total) + ": " + strings.TrimSpace(app.Name),
			Description: statusLabel,
			Disabled:    true,
		}}, items...),
	}, true
}

func NewPluginCatalogPopupModel(response pluginapi.PluginListResponse, options PluginCatalogPopupOptions) PluginCatalogPopupModel {
	marketplaces := response.Marketplaces
	preferredLocalSources := PreferredLocalPluginSources(marketplaces)
	allEntries := PluginEntriesForMarketplaces(marketplaces)
	total := len(allEntries)
	installed := installedPluginEntryCount(allEntries)
	nameColumnWidth := pluginCatalogNameColumnWidth(allEntries)
	model := PluginCatalogPopupModel{
		ViewID:               PluginsSelectionViewID,
		FooterHint:           PluginsPopupHintLine(false, false),
		TabFooterHints:       map[string]string{},
		InitialSelectedIndex: options.InitialSelectedIndex,
		Searchable:           true,
		SearchPlaceholder:    "Type to search plugins",
		NameColumnWidth:      nameColumnWidth,
	}

	model.Tabs = append(model.Tabs, PluginCatalogTabModel{
		ID:          AllPluginsTabID,
		Label:       "All Plugins",
		HeaderLines: PluginsHeaderLines("Browse plugins from available marketplaces.", "Installed "+intString(installed)+" of "+intString(total)+" available plugins."),
		Items: PluginSelectionItemsForEntries(
			allEntries,
			preferredLocalSources,
			true,
			"No marketplace plugins available",
			"No plugins are available in the discovered marketplaces.",
		),
	})

	installedEntries := make([]PluginCatalogEntry, 0, installed)
	for _, entry := range allEntries {
		if entry.Plugin.Installed {
			installedEntries = append(installedEntries, entry)
		}
	}
	model.Tabs = append(model.Tabs, PluginCatalogTabModel{
		ID:          InstalledPluginsTabID,
		Label:       "Installed (" + intString(installed) + ")",
		HeaderLines: PluginsHeaderLines("Installed plugins.", "Showing "+intString(installed)+" installed plugins."),
		Items: PluginSelectionItemsForEntries(
			installedEntries,
			preferredLocalSources,
			true,
			"No installed plugins",
			"No installed plugins.",
		),
	})

	curatedEntries := pluginEntriesMatchingMarketplaces(marketplaces, func(marketplace pluginapi.PluginMarketplaceEntry) bool {
		return MarketplaceProductFromEntry(marketplace).IsByOpenAI()
	})
	curatedTotal := len(curatedEntries)
	curatedInstalled := installedPluginEntryCount(curatedEntries)
	curatedLoading := options.RemoteSectionsLoading && options.VerticalSectionRequested
	curatedError, hasCuratedError := pluginCatalogSectionError(options.SectionErrors, "vertical")
	curatedEmptyName := "No OpenAI Curated plugins available"
	curatedEmptyDescription := "No OpenAI Curated plugins available."
	if curatedLoading && curatedTotal == 0 {
		curatedEmptyName = "Loading OpenAI Curated plugins..."
		curatedEmptyDescription = PluginCatalogOpenAICuratedLoadingDescription
	} else if hasCuratedError && curatedTotal == 0 {
		curatedEmptyName = "OpenAI Curated unavailable"
		curatedEmptyDescription = curatedError.Message
	}
	curatedItems := PluginSelectionItemsForEntries(
		curatedEntries,
		preferredLocalSources,
		false,
		curatedEmptyName,
		curatedEmptyDescription,
	)
	if curatedLoading && curatedTotal > 0 {
		curatedItems = append(curatedItems, disabledPluginSelectionItem("Loading OpenAI Curated plugins...", PluginCatalogOpenAICuratedLoadingDescription))
	}
	if hasCuratedError && curatedTotal > 0 {
		label := strings.TrimSpace(curatedError.Label)
		if label == "" {
			label = "OpenAI Curated"
		}
		curatedItems = append(curatedItems, disabledPluginSelectionItem(label+" unavailable", curatedError.Message))
	}
	model.Tabs = append(model.Tabs, PluginCatalogTabModel{
		ID:          OpenAICuratedTabID,
		Label:       "OpenAI Curated",
		HeaderLines: PluginsHeaderLines("OpenAI Curated marketplace.", "Installed "+intString(curatedInstalled)+" of "+intString(curatedTotal)+" OpenAI Curated plugins."),
		Items:       curatedItems,
	})

	additionalMarketplaces := make([]pluginapi.PluginMarketplaceEntry, 0, len(marketplaces))
	for _, marketplace := range marketplaces {
		if !MarketplaceProductFromEntry(marketplace).IsByOpenAI() {
			additionalMarketplaces = append(additionalMarketplaces, marketplace)
		}
	}
	sort.SliceStable(additionalMarketplaces, func(i int, j int) bool {
		leftLabel := MarketplaceDisplayName(additionalMarketplaces[i])
		rightLabel := MarketplaceDisplayName(additionalMarketplaces[j])
		leftProduct := MarketplaceProductFromEntry(additionalMarketplaces[i])
		rightProduct := MarketplaceProductFromEntry(additionalMarketplaces[j])
		if leftProduct.TabOrder() != rightProduct.TabOrder() {
			return leftProduct.TabOrder() < rightProduct.TabOrder()
		}
		if strings.ToLower(leftLabel) != strings.ToLower(rightLabel) {
			return strings.ToLower(leftLabel) < strings.ToLower(rightLabel)
		}
		if leftLabel != rightLabel {
			return leftLabel < rightLabel
		}
		return additionalMarketplaces[i].Name < additionalMarketplaces[j].Name
	})

	additionalTabs := make([]PluginCatalogTabModel, 0, len(additionalMarketplaces)+len(pluginCatalogRemoteSections))
	for _, section := range pluginCatalogRemoteSections {
		if tab, ok := section.fallbackTab(marketplaces, PluginCatalogTabsOptions{
			RemoteSectionsLoading: options.RemoteSectionsLoading,
			RemoteSectionsLoaded:  options.RemoteSectionsLoaded,
			SectionErrors:         options.SectionErrors,
		}); ok {
			additionalTabs = append(additionalTabs, pluginCatalogTabModelFromFallback(tab, section))
		}
	}

	labels := make([]string, 0, len(additionalMarketplaces))
	for _, marketplace := range additionalMarketplaces {
		labels = append(labels, MarketplaceDisplayName(marketplace))
	}
	labels = DisambiguateDuplicateTabLabels(labels)
	for i, marketplace := range additionalMarketplaces {
		label := labels[i]
		entries := PluginEntriesForMarketplaces([]pluginapi.PluginMarketplaceEntry{marketplace})
		marketplaceTotal := len(entries)
		marketplaceInstalled := installedPluginEntryCount(entries)
		tabID := MarketplaceTabID(marketplace)
		canRemove := options.CanRemoveMarketplaces[strings.TrimSpace(marketplace.Name)]
		canUpgrade := options.CanUpgradeMarketplaces[strings.TrimSpace(marketplace.Name)] && marketplace.Path != nil
		if canRemove || canUpgrade {
			model.TabFooterHints[tabID] = PluginsPopupHintLine(canRemove, canUpgrade)
		}
		header := PluginsHeaderLines(label+".", "Installed "+intString(marketplaceInstalled)+" of "+intString(marketplaceTotal)+" "+label+" plugins.")
		if options.NewlyInstalledMarketplaceTabID == tabID {
			header = PluginsHeaderLines(
				label+" installed successfully.",
				"Select the plugins you want to use and press Enter to install or view details.",
			)
		}
		additionalTabs = append(additionalTabs, PluginCatalogTabModel{
			ID:          tabID,
			Label:       label,
			Order:       MarketplaceProductFromEntry(marketplace).TabOrder(),
			HeaderLines: header,
			FooterHint:  model.TabFooterHints[tabID],
			Items: PluginSelectionItemsForEntries(
				entries,
				preferredLocalSources,
				false,
				"No plugins available in this marketplace",
				"No plugins available in this marketplace.",
			),
		})
	}
	sort.SliceStable(additionalTabs, func(i int, j int) bool {
		left := additionalTabs[i].Order
		right := additionalTabs[j].Order
		if left != right {
			return left < right
		}
		return i < j
	})
	model.Tabs = append(model.Tabs, additionalTabs...)
	model.Tabs = append(model.Tabs, PluginCatalogAddMarketplaceTabModel())

	if tabID, ok := PluginCatalogPopupTabMatchingSavedID(options.ActiveTabID, model.Tabs); ok {
		model.InitialTabID = tabID
	}
	return model
}

func PluginCatalogPopupTabMatchingSavedID(savedTabID string, tabs []PluginCatalogTabModel) (string, bool) {
	savedTabID = strings.TrimSpace(savedTabID)
	if savedTabID == "" {
		return "", false
	}
	for _, tab := range tabs {
		if tab.ID == savedTabID {
			return tab.ID, true
		}
	}
	for _, section := range pluginCatalogRemoteSections {
		if !section.containsTabID(savedTabID) {
			continue
		}
		for _, tab := range tabs {
			if section.containsTabID(tab.ID) {
				return tab.ID, true
			}
		}
	}
	return "", false
}

func PluginCatalogAddMarketplaceTabModel() PluginCatalogTabModel {
	return PluginCatalogTabModel{
		ID:          AddMarketplaceTabID,
		Label:       "Add Marketplace",
		HeaderLines: PluginsHeaderLines("Add a marketplace from a Git repo or local root.", "Enter a source to make its plugins available in this menu."),
		Items: []PluginSelectionItemModel{{
			SelectionItem: SelectionItem{
				Name:                "Add marketplace",
				Description:         "Enter owner/repo, a Git URL, or a local marketplace path.",
				SelectedDescription: "Press Enter to enter a marketplace source.",
				Action:              PluginMenuActionAddMarketplace,
			},
		}},
	}
}

func PluginSelectionItemsForEntries(entries []PluginCatalogEntry, preferredLocalSources map[string]PreferredLocalPluginSource, includeMarketplaceNames bool, emptyName string, emptyDescription string) []PluginSelectionItemModel {
	sort.SliceStable(entries, func(i int, j int) bool {
		if entries[i].Plugin.Installed != entries[j].Plugin.Installed {
			return entries[i].Plugin.Installed
		}
		left := strings.ToLower(entries[i].DisplayName)
		right := strings.ToLower(entries[j].DisplayName)
		if left != right {
			return left < right
		}
		if entries[i].DisplayName != entries[j].DisplayName {
			return entries[i].DisplayName < entries[j].DisplayName
		}
		if entries[i].Plugin.Name != entries[j].Plugin.Name {
			return entries[i].Plugin.Name < entries[j].Plugin.Name
		}
		return entries[i].Plugin.ID < entries[j].Plugin.ID
	})
	statusWidth := 0
	for _, entry := range entries {
		if width := len([]rune(PluginStatusLabel(entry.Plugin))); width > statusWidth {
			statusWidth = width
		}
	}
	items := make([]PluginSelectionItemModel, 0, len(entries))
	for _, entry := range entries {
		plugin := entry.Plugin
		displayName := entry.DisplayName
		marketplaceLabel := MarketplaceDisplayName(entry.Marketplace)
		statusLabel := PluginStatusLabel(plugin)
		description := PluginBriefDescriptionWithoutMarketplace(plugin, statusWidth)
		if includeMarketplaceNames {
			description = PluginBriefDescription(plugin, marketplaceLabel, statusWidth)
		}
		detailRequest, canViewDetails := PluginDetailRequestForEntry(entry.Marketplace, plugin, preferredLocalSources)
		disabledByAdmin := plugin.Availability == pluginapi.PluginDisabledByAdmin
		canToggle := plugin.Installed && plugin.InstallPolicy != pluginapi.InstallInstalledByDefault && !disabledByAdmin
		selectedDescription := pluginSelectionSelectedDescription(statusLabel, statusWidth, plugin, canToggle, canViewDetails, disabledByAdmin)
		searchValue := strings.Join([]string{
			displayName,
			plugin.ID,
			plugin.Name,
			marketplaceLabel,
			pluginDescriptionOrEmpty(plugin),
			strings.Join(plugin.Keywords, " "),
		}, " ")
		item := PluginSelectionItemModel{
			SelectionItem: SelectionItem{
				ID:                  firstNonEmptyRequestID(strings.TrimSpace(plugin.ID), strings.TrimSpace(plugin.Name)),
				Name:                displayName,
				Description:         description,
				SelectedDescription: selectedDescription,
				SearchValue:         searchValue,
				Action:              PluginMenuActionOpenDetails,
				Disabled:            !canViewDetails && !plugin.Installed,
			},
			CanViewDetails: canViewDetails,
			CanToggle:      canToggle,
		}
		if item.Disabled {
			item.DisabledReason = "plugin details are unavailable"
		}
		if canViewDetails {
			requestCopy := detailRequest
			item.DetailRequest = &requestCopy
		}
		if canToggle {
			item.Toggle = &PluginSelectionToggle{IsOn: plugin.Enabled, PluginID: strings.TrimSpace(plugin.ID)}
		} else if disabledByAdmin {
			item.TogglePlaceholder = "blocked"
		} else {
			item.TogglePlaceholder = "unavailable"
		}
		items = append(items, item)
	}
	if len(items) == 0 {
		items = append(items, disabledPluginSelectionItem(emptyName, emptyDescription))
	}
	return items
}

func PluginsPopupHintLine(canRemoveMarketplace bool, canUpgradeMarketplace bool) string {
	switch {
	case canRemoveMarketplace && canUpgradeMarketplace:
		return "ctrl + u upgrade" + pluginSummarySeparator + "ctrl + r remove" + pluginSummarySeparator + "space toggle" + pluginSummarySeparator + "left/right tabs" + pluginSummarySeparator + "enter details" + pluginSummarySeparator + "esc close"
	case canRemoveMarketplace:
		return "ctrl + r remove" + pluginSummarySeparator + "space toggle" + pluginSummarySeparator + "left/right tabs" + pluginSummarySeparator + "enter details" + pluginSummarySeparator + "esc close"
	case canUpgradeMarketplace:
		return "ctrl + u upgrade" + pluginSummarySeparator + "space toggle" + pluginSummarySeparator + "left/right tabs" + pluginSummarySeparator + "enter details" + pluginSummarySeparator + "esc close"
	default:
		return "space enable/disable" + pluginSummarySeparator + "left/right select marketplace" + pluginSummarySeparator + "enter view details" + pluginSummarySeparator + "esc close"
	}
}

func PluginsHeaderLines(subtitle string, countLine string) []string {
	lines := []string{"Plugins"}
	if subtitle = strings.TrimSpace(subtitle); subtitle != "" {
		lines = append(lines, subtitle)
	}
	if countLine = strings.TrimSpace(countLine); countLine != "" {
		lines = append(lines, countLine)
	}
	return lines
}

func PluginMetadataItems(detail pluginapi.PluginDetail) []SelectionItem {
	items := []SelectionItem{
		{Name: "Source", Description: PluginSourceSummary(detail), Disabled: true},
		{Name: "Auth", Description: PluginAuthPolicySummary(detail.Summary.AuthPolicy), Disabled: true},
	}
	if version, ok := PluginVersionSummary(detail.Summary); ok {
		items = append(items, SelectionItem{Name: "Version", Description: version, Disabled: true})
	}
	if detail.Summary.ShareContext != nil {
		items = append(items, SelectionItem{Name: "Sharing", Description: PluginShareContextSummary(detail.Summary.ShareContext), Disabled: true})
	}
	return items
}

func PluginDetailDescription(detail pluginapi.PluginDetail) (string, bool) {
	if detail.Description != nil {
		if value := strings.TrimSpace(*detail.Description); value != "" {
			return value, true
		}
	}
	if detail.Summary.Interface != nil {
		if detail.Summary.Interface.LongDescription != nil {
			if value := strings.TrimSpace(*detail.Summary.Interface.LongDescription); value != "" {
				return value, true
			}
		}
		if detail.Summary.Interface.ShortDescription != nil {
			if value := strings.TrimSpace(*detail.Summary.Interface.ShortDescription); value != "" {
				return value, true
			}
		}
	}
	return "", false
}

func NewPluginDetailView(detail pluginapi.PluginDetail) SelectionView {
	marketplaceLabel := MarketplaceProductFromParts(detail.MarketplaceName, detail.MarketplacePath).Label()
	if marketplaceLabel == "" {
		marketplaceLabel = strings.TrimSpace(detail.MarketplaceName)
	}
	displayName := PluginDisplayName(detail.Summary)
	header := []string{
		"Plugins",
		displayName + pluginSummarySeparator + PluginDetailStatusLabel(detail.Summary) + pluginSummarySeparator + marketplaceLabel,
	}
	if !detail.Summary.Installed {
		header = append(header, "Data shared with this app is subject to the app's terms of service and privacy policy. Learn more: "+PluginCatalogAppsHelpURL)
	}
	if description, ok := PluginDetailDescription(detail); ok {
		header = append(header, description)
	}

	items := []SelectionItem{{
		Name:                "Back to plugins",
		Description:         "Return to the plugin list.",
		SelectedDescription: "Return to the plugin list.",
		Action:              PluginMenuActionBackToPlugins,
	}}
	items = append(items, pluginDetailPrimaryActionItem(detail))
	items = append(items, PluginMetadataItems(detail)...)
	items = append(items,
		SelectionItem{Name: "Skills", Description: PluginSkillSummary(detail), Disabled: true},
		SelectionItem{Name: "Hooks", Description: PluginHookSummary(detail), Disabled: true},
		SelectionItem{Name: "Apps", Description: PluginAppSummary(detail), Disabled: true},
		SelectionItem{Name: "MCP Servers", Description: PluginMCPSummary(detail), Disabled: true},
	)

	return SelectionView{
		ViewID:      PluginsSelectionViewID,
		Title:       "Plugins",
		HeaderLines: header,
		FooterHint:  PluginDetailHintLine(),
		AllowCancel: true,
		Items:       items,
	}
}

func DisambiguateDuplicateTabLabels(labels []string) []string {
	counts := map[string]int{}
	for _, label := range labels {
		counts[label]++
	}
	seen := map[string]int{}
	out := make([]string, 0, len(labels))
	for _, label := range labels {
		if counts[label] == 1 {
			out = append(out, label)
			continue
		}
		seen[label]++
		out = append(out, label+" ("+intString(seen[label])+"/"+intString(counts[label])+")")
	}
	return out
}

func PluginsLoadingView() SelectionView {
	return PluginsLoadingPopupView()
}

func PluginsLoadingPopupView() SelectionView {
	return SelectionView{
		ViewID:      PluginsSelectionViewID,
		Title:       "Plugins",
		Subtitle:    "Loading available plugins...",
		HeaderLines: []string{"This updates when the marketplace list is ready."},
		AllowCancel: true,
		Items: []SelectionItem{{
			Name:        "Loading plugins...",
			Description: "This updates when the marketplace list is ready.",
			Disabled:    true,
		}},
	}
}

func MarketplaceAddLoadingPopupView() SelectionView {
	return SelectionView{
		ViewID:      PluginsSelectionViewID,
		Title:       "Plugins",
		Subtitle:    "Adding marketplace...",
		AllowCancel: false,
		Items: []SelectionItem{{
			Name:        "Adding marketplace...",
			Description: "This updates when marketplace installation completes.",
			Disabled:    true,
		}},
	}
}

func MarketplaceRemoveLoadingPopupView(marketplaceDisplayName string) SelectionView {
	return SelectionView{
		ViewID:      PluginsSelectionViewID,
		Title:       "Plugins",
		Subtitle:    "Removing " + strings.TrimSpace(marketplaceDisplayName) + "...",
		AllowCancel: false,
		Items: []SelectionItem{{
			Name:        "Removing marketplace...",
			Description: "This updates when marketplace removal completes.",
			Disabled:    true,
		}},
	}
}

func MarketplaceUpgradeLoadingPopupView(marketplaceName string) SelectionView {
	loadingText := "Upgrading marketplaces..."
	if marketplaceName = strings.TrimSpace(marketplaceName); marketplaceName != "" {
		loadingText = "Upgrading " + marketplaceName + " marketplace..."
	}
	return SelectionView{
		ViewID:      PluginsSelectionViewID,
		Title:       "Plugins",
		Subtitle:    loadingText,
		AllowCancel: false,
		Items: []SelectionItem{{
			Name:        loadingText,
			Description: "This updates when marketplace upgrade completes.",
			Disabled:    true,
		}},
	}
}

func PluginDetailLoadingView(pluginDisplayName string) SelectionView {
	return PluginDetailLoadingPopupView(pluginDisplayName)
}

func PluginDetailLoadingPopupView(pluginDisplayName string) SelectionView {
	return SelectionView{
		ViewID:      PluginsSelectionViewID,
		Title:       "Plugins",
		Subtitle:    "Loading details for " + strings.TrimSpace(pluginDisplayName) + "...",
		AllowCancel: false,
		Items: []SelectionItem{{
			Name:        "Loading plugin details...",
			Description: "This updates when plugin details load.",
			Disabled:    true,
		}},
	}
}

func PluginInstallLoadingView(pluginDisplayName string) SelectionView {
	return PluginInstallLoadingPopupView(pluginDisplayName)
}

func PluginInstallLoadingPopupView(pluginDisplayName string) SelectionView {
	return SelectionView{
		ViewID:      PluginsSelectionViewID,
		Title:       "Plugins",
		Subtitle:    "Installing " + strings.TrimSpace(pluginDisplayName) + "...",
		AllowCancel: false,
		Items: []SelectionItem{{
			Name:        "Installing plugin...",
			Description: "This updates when plugin installation completes.",
			Disabled:    true,
		}},
	}
}

func PluginUninstallLoadingView(pluginDisplayName string) SelectionView {
	return PluginUninstallLoadingPopupView(pluginDisplayName)
}

func PluginUninstallLoadingPopupView(pluginDisplayName string) SelectionView {
	return SelectionView{
		ViewID:      PluginsSelectionViewID,
		Title:       "Plugins",
		Subtitle:    "Uninstalling " + strings.TrimSpace(pluginDisplayName) + "...",
		AllowCancel: false,
		Items: []SelectionItem{{
			Name:        "Uninstalling plugin...",
			Description: "This updates when the plugin removal completes.",
			Disabled:    true,
		}},
	}
}

func PluginErrorView(message string, canBack bool) SelectionView {
	items := []SelectionItem{{
		Name:            "Close",
		DismissOnSelect: true,
	}}
	if canBack {
		items = append([]SelectionItem{{
			Name:            "Back to plugins",
			Action:          PluginMenuActionBackToPlugins,
			DismissOnSelect: true,
		}}, items...)
	}
	return SelectionView{
		ViewID:      PluginsSelectionViewID,
		Title:       "Plugins",
		Subtitle:    strings.TrimSpace(message),
		FooterHint:  PluginDetailHintLine(),
		AllowCancel: true,
		Items:       items,
	}
}

func PluginsErrorPopupView(message string) SelectionView {
	return SelectionView{
		ViewID:      PluginsSelectionViewID,
		Title:       "Plugins",
		Subtitle:    "Failed to load plugins.",
		AllowCancel: true,
		Items: []SelectionItem{{
			Name:        "Plugin marketplace unavailable",
			Description: strings.TrimSpace(message),
			Disabled:    true,
		}},
	}
}

func MarketplaceAddErrorPopupView(canBack bool) SelectionView {
	items := []SelectionItem{
		{
			Name:        "Marketplace add failed",
			Description: "Failed to add marketplace from the provided source.",
			Disabled:    true,
		},
		{
			Name:                "Try again",
			Description:         "Enter a marketplace source.",
			SelectedDescription: "Enter a marketplace source.",
			Action:              PluginMenuActionAddMarketplace,
		},
	}
	if canBack {
		items = append(items, SelectionItem{
			Name:                "Back to plugins",
			Description:         "Return to the plugin list.",
			SelectedDescription: "Return to the plugin list.",
			Action:              PluginMenuActionBackToPlugins,
		})
	}
	return SelectionView{
		ViewID:      PluginsSelectionViewID,
		Title:       "Plugins",
		Subtitle:    "Failed to add marketplace.",
		FooterHint:  PluginDetailHintLine(),
		AllowCancel: true,
		Items:       items,
	}
}

func MarketplaceRemoveConfirmationView(marketplaceName string, marketplaceDisplayName string) SelectionView {
	return SelectionView{
		ViewID:      PluginsSelectionViewID,
		Title:       "Plugins",
		HeaderLines: []string{"Remove " + strings.TrimSpace(marketplaceDisplayName) + " marketplace?", "This removes the configured marketplace from Codex."},
		FooterHint:  "Enter select" + pluginSummarySeparator + "esc close",
		AllowCancel: true,
		Items: []SelectionItem{
			{
				ID:                  strings.TrimSpace(marketplaceName),
				Name:                "Remove marketplace",
				Description:         "Remove this marketplace from the available plugin list.",
				SelectedDescription: "Remove this marketplace from the available plugin list.",
				Action:              PluginMenuActionRemoveMarketplace,
			},
			{
				Name:                "Back to plugins",
				Description:         "Keep this marketplace installed.",
				SelectedDescription: "Keep this marketplace installed.",
				Action:              PluginMenuActionBackToPlugins,
			},
		},
	}
}

func MarketplaceRemoveErrorPopupView(marketplaceName string, marketplaceDisplayName string, canBack bool) SelectionView {
	items := []SelectionItem{
		{
			Name:        "Marketplace removal failed",
			Description: "Failed to remove the selected marketplace.",
			Disabled:    true,
		},
		{
			ID:                  strings.TrimSpace(marketplaceName),
			Name:                "Try again",
			Description:         "Review the confirmation prompt again.",
			SelectedDescription: "Review the confirmation prompt again.",
			Action:              PluginMenuActionRemoveMarketplace,
		},
	}
	if canBack {
		items = append(items, SelectionItem{
			Name:                "Back to plugins",
			Description:         "Return to the plugin list.",
			SelectedDescription: "Return to the plugin list.",
			Action:              PluginMenuActionBackToPlugins,
		})
	}
	return SelectionView{
		ViewID:      PluginsSelectionViewID,
		Title:       "Plugins",
		Subtitle:    "Failed to remove marketplace.",
		FooterHint:  PluginDetailHintLine(),
		AllowCancel: true,
		Items:       items,
	}
}

func PluginDetailErrorPopupView(message string, canBack bool) SelectionView {
	items := []SelectionItem{{
		Name:        "Plugin detail unavailable",
		Description: strings.TrimSpace(message),
		Disabled:    true,
	}}
	if canBack {
		items = append(items, SelectionItem{
			Name:                "Back to plugins",
			Description:         "Return to the plugin list.",
			SelectedDescription: "Return to the plugin list.",
			Action:              PluginMenuActionBackToPlugins,
		})
	}
	return SelectionView{
		ViewID:      PluginsSelectionViewID,
		Title:       "Plugins",
		Subtitle:    "Failed to load plugin details.",
		FooterHint:  PluginDetailHintLine(),
		AllowCancel: true,
		Items:       items,
	}
}

func PluginDetailHintLine() string {
	return "Press esc to close."
}

func InstalledPluginCountLine(plugins []pluginapi.PluginSummary) string {
	available := 0
	installed := 0
	for _, plugin := range plugins {
		if plugin.InstallPolicy != pluginapi.InstallBlocked && plugin.Availability != pluginapi.PluginDisabledByAdmin {
			available++
		}
		if plugin.Installed {
			installed++
		}
	}
	return "Installed " + intString(installed) + " of " + intString(available) + " available plugins."
}

func NewPluginsCatalogView(response pluginapi.PluginListResponse, savedTabID string) SelectionView {
	entries := SortedPluginCatalogEntries(response.Marketplaces)
	plugins := make([]pluginapi.PluginSummary, 0, len(entries))
	for _, entry := range entries {
		plugins = append(plugins, entry.Plugin)
	}
	statusWidth := pluginStatusLabelWidth(plugins)
	marketplaceLabels := pluginMarketplaceLabels(response.Marketplaces)
	items := make([]SelectionItem, 0, len(plugins))
	for _, entry := range entries {
		plugin := entry.Plugin
		id := firstNonEmptyRequestID(strings.TrimSpace(plugin.ID), strings.TrimSpace(plugin.Name))
		if id == "" {
			continue
		}
		marketplaceLabel := marketplaceLabels[strings.TrimSpace(plugin.MarketplaceName)]
		if marketplaceLabel == "" {
			marketplaceLabel = strings.TrimSpace(plugin.MarketplaceName)
		}
		description := PluginBriefDescription(plugin, marketplaceLabel, statusWidth)
		if version, ok := PluginVersionSummary(plugin); ok {
			description += "\n" + version
		}
		if share := PluginShareContextSummary(plugin.ShareContext); share != "" {
			description += "\n" + share
		}
		items = append(items, SelectionItem{
			ID:          id,
			Name:        PluginDisplayName(plugin),
			Description: description,
			SearchValue: strings.Join([]string{
				plugin.ID,
				plugin.Name,
				PluginDisplayName(plugin),
				strings.Join(plugin.Keywords, " "),
				marketplaceLabel,
				description,
			}, " "),
			Action: PluginMenuActionOpenDetails,
		})
	}
	if len(items) == 0 {
		items = append(items, SelectionItem{Name: "No plugins found", Disabled: true})
	}
	header := []string{InstalledPluginCountLine(plugins)}
	for _, loadErr := range response.MarketplaceLoadErrors {
		message := strings.TrimSpace(loadErr.Message)
		path := strings.TrimSpace(loadErr.MarketplacePath)
		if message == "" && path == "" {
			continue
		}
		if path != "" && message != "" {
			header = append(header, "Failed to load "+path+": "+message)
		} else if message != "" {
			header = append(header, "Failed to load marketplace: "+message)
		} else {
			header = append(header, "Failed to load "+path)
		}
	}
	selectedTab := ""
	if tabID, ok := (PluginCatalogViewState{Marketplaces: response.Marketplaces, SavedTabID: savedTabID}).SelectedTabID(); ok {
		selectedTab = tabID
	}
	if selectedTab != "" {
		header = append(header, "Selected tab: "+selectedTab)
	}
	return SelectionView{
		ViewID:            PluginsSelectionViewID,
		Title:             "Plugins",
		HeaderLines:       header,
		FooterHint:        standardPopupHintLine,
		AllowCancel:       true,
		Searchable:        true,
		SearchPlaceholder: "Type to search plugins",
		Items:             items,
	}
}

func SortedPluginEntries(marketplaces []pluginapi.PluginMarketplaceEntry) []pluginapi.PluginSummary {
	entries := SortedPluginCatalogEntries(marketplaces)
	out := make([]pluginapi.PluginSummary, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry.Plugin)
	}
	return out
}

func SortedPluginCatalogEntries(marketplaces []pluginapi.PluginMarketplaceEntry) []PluginCatalogEntry {
	out := PluginEntriesForMarketplaces(marketplaces)
	sort.SliceStable(out, func(i int, j int) bool {
		if out[i].Plugin.Installed != out[j].Plugin.Installed {
			return out[i].Plugin.Installed
		}
		left := strings.ToLower(out[i].DisplayName)
		right := strings.ToLower(out[j].DisplayName)
		if left != right {
			return left < right
		}
		if out[i].DisplayName != out[j].DisplayName {
			return out[i].DisplayName < out[j].DisplayName
		}
		if out[i].Plugin.Name != out[j].Plugin.Name {
			return out[i].Plugin.Name < out[j].Plugin.Name
		}
		return out[i].Plugin.ID < out[j].Plugin.ID
	})
	return out
}

func pluginStatusLabelWidth(plugins []pluginapi.PluginSummary) int {
	width := len("Available")
	for _, plugin := range plugins {
		if length := len([]rune(PluginStatusLabel(plugin))); length > width {
			width = length
		}
	}
	return width
}

func pluginMarketplaceLabels(marketplaces []pluginapi.PluginMarketplaceEntry) map[string]string {
	out := map[string]string{}
	for _, marketplace := range marketplaces {
		name := strings.TrimSpace(marketplace.Name)
		if name == "" {
			continue
		}
		out[name] = MarketplaceDisplayName(marketplace)
	}
	return out
}

func pluginLoadingView(subtitle string) SelectionView {
	return SelectionView{
		ViewID:      PluginsSelectionViewID,
		Title:       "Plugins",
		Subtitle:    strings.TrimSpace(subtitle),
		AllowCancel: false,
		Items: []SelectionItem{{
			Name:     "Loading...",
			Disabled: true,
		}},
	}
}

func installedPluginEntryCount(entries []PluginCatalogEntry) int {
	count := 0
	for _, entry := range entries {
		if entry.Plugin.Installed {
			count++
		}
	}
	return count
}

func pluginCatalogNameColumnWidth(entries []PluginCatalogEntry) int {
	width := len([]rune("Add marketplace"))
	for _, entry := range entries {
		if candidate := PluginRowPrefixWidth + len([]rune(entry.DisplayName)); candidate > width {
			width = candidate
		}
	}
	return width
}

func pluginEntriesMatchingMarketplaces(marketplaces []pluginapi.PluginMarketplaceEntry, include func(pluginapi.PluginMarketplaceEntry) bool) []PluginCatalogEntry {
	selected := make([]pluginapi.PluginMarketplaceEntry, 0, len(marketplaces))
	for _, marketplace := range marketplaces {
		if include(marketplace) {
			selected = append(selected, marketplace)
		}
	}
	return PluginEntriesForMarketplaces(selected)
}

func pluginCatalogTabModelFromFallback(tab PluginCatalogTab, section pluginCatalogRemoteSection) PluginCatalogTabModel {
	headerSubtitle := section.Label + "."
	headerCount := "This section loaded successfully."
	switch tab.Kind {
	case PluginCatalogTabLoading:
		headerSubtitle = "Loading " + section.Label + " plugins."
		headerCount = "Local plugin functionality is already available."
	case PluginCatalogTabError:
		headerSubtitle = tab.Label + " unavailable."
		headerCount = "Local plugin functionality is still available."
	}
	return PluginCatalogTabModel{
		ID:          tab.ID,
		Label:       tab.Label,
		Order:       section.TabOrder,
		HeaderLines: PluginsHeaderLines(headerSubtitle, headerCount),
		Items: []PluginSelectionItemModel{{
			SelectionItem: tab.Item,
		}},
	}
}

func disabledPluginSelectionItem(name string, description string) PluginSelectionItemModel {
	return PluginSelectionItemModel{
		SelectionItem: SelectionItem{
			Name:        strings.TrimSpace(name),
			Description: strings.TrimSpace(description),
			Disabled:    true,
		},
	}
}

func pluginSelectionSelectedDescription(statusLabel string, statusWidth int, plugin pluginapi.PluginSummary, canToggle bool, canViewDetails bool, disabledByAdmin bool) string {
	selectedStatus := padRight(statusLabel, statusWidth)
	switch {
	case canToggle && canViewDetails:
		action := "enable"
		if plugin.Enabled {
			action = "disable"
		}
		return selectedStatus + "   Space to " + action + "; Enter view details."
	case canToggle:
		action := "enable"
		if plugin.Enabled {
			action = "disable"
		}
		return selectedStatus + "   Space to " + action + "."
	case disabledByAdmin && canViewDetails:
		return selectedStatus + "   Press Enter to view plugin details."
	case disabledByAdmin:
		return selectedStatus + "   Plugin details are unavailable."
	case plugin.Installed && canViewDetails:
		return selectedStatus + "   Press Enter to view plugin details."
	case plugin.Installed:
		return selectedStatus + "   Plugin details are unavailable."
	case canViewDetails:
		return selectedStatus + "   Press Enter to install or view plugin details."
	default:
		return selectedStatus + "   Remote plugin details are not available yet."
	}
}

func pluginDescriptionOrEmpty(plugin pluginapi.PluginSummary) string {
	description, _ := PluginDescription(plugin)
	return description
}

func pluginDetailPrimaryActionItem(detail pluginapi.PluginDetail) SelectionItem {
	plugin := detail.Summary
	switch {
	case plugin.Installed && plugin.InstallPolicy == pluginapi.InstallInstalledByDefault:
		return SelectionItem{
			Name:        "Installed by admin",
			Description: "This plugin is installed by your workspace admin.",
			Disabled:    true,
		}
	case plugin.Installed:
		if pluginID, ok := PluginUninstallID(plugin); ok {
			return SelectionItem{
				Name:                "Uninstall plugin",
				Description:         "Remove this plugin now.",
				SelectedDescription: "Remove this plugin now.",
				Action:              PluginMenuActionUninstall,
				ID:                  pluginID,
			}
		}
		return SelectionItem{
			Name:        "Uninstall plugin",
			Description: "This remote plugin did not provide an uninstall identity.",
			Disabled:    true,
		}
	case plugin.Availability == pluginapi.PluginDisabledByAdmin:
		return SelectionItem{
			Name:        "Install plugin",
			Description: "This plugin is disabled by your workspace admin.",
			Disabled:    true,
		}
	case plugin.InstallPolicy == pluginapi.InstallBlocked:
		return SelectionItem{
			Name:        "Install plugin",
			Description: "This plugin is not installable from this marketplace.",
			Disabled:    true,
		}
	default:
		if location, ok := PluginDetailLocation(detail); ok {
			params := newPluginDetailRequest(location, PluginRequestName(plugin), PluginRemoteIdentity(plugin)).ReadParams
			return SelectionItem{
				Name:                "Install plugin",
				Description:         "Install this plugin now.",
				SelectedDescription: "Install this plugin now.",
				Action:              PluginMenuActionInstall,
				ID:                  firstNonEmptyRequestID(params.PluginName, plugin.ID, plugin.Name),
			}
		}
		return SelectionItem{
			Name:        "Install plugin",
			Description: "This plugin did not provide an install location.",
			Disabled:    true,
		}
	}
}

func marketplaceInterfaceDisplayName(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case pluginapi.MarketplaceInterface:
		return stringValue(typed.DisplayName)
	case *pluginapi.MarketplaceInterface:
		if typed == nil {
			return ""
		}
		return stringValue(typed.DisplayName)
	case map[string]any:
		if value, ok := typed["displayName"].(string); ok {
			return strings.TrimSpace(value)
		}
	case map[string]string:
		return strings.TrimSpace(typed["displayName"])
	}
	return ""
}

func remoteMarketplaceSectionName(name string) bool {
	switch strings.TrimSpace(name) {
	case RemoteGlobalMarketplaceName,
		RemoteWorkspaceMarketplace,
		RemoteWorkspaceSharedWithMe,
		RemoteWorkspaceSharedPrivate,
		RemoteWorkspaceSharedUnlisted,
		RemoteCreatedByMeMarketplace:
		return true
	default:
		return false
	}
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func padRight(value string, width int) string {
	value = strings.TrimSpace(value)
	if width <= len([]rune(value)) {
		return value
	}
	return value + strings.Repeat(" ", width-len([]rune(value)))
}

func intString(value int) string {
	return formatInt64(int64(value))
}

func pluginSourceIsRemote(source pluginapi.PluginSource) bool {
	return strings.EqualFold(strings.TrimSpace(source.Type), "remote")
}

func newPluginDetailRequest(location PluginLocation, pluginName string, remotePluginID string) PluginDetailRequest {
	pluginName = strings.TrimSpace(pluginName)
	request := PluginDetailRequest{
		Location:   location,
		PluginName: pluginName,
	}
	switch location.Kind {
	case PluginLocationLocal:
		request.ReadParams.MarketplacePath = strings.TrimSpace(location.MarketplacePath)
	case PluginLocationRemote:
		request.ReadParams.RemoteMarketplaceName = strings.TrimSpace(location.MarketplaceName)
	}
	request.ReadParams.PluginName = pluginName
	request.ReadParams.RemotePluginID = strings.TrimSpace(remotePluginID)
	return request
}
