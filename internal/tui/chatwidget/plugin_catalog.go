package chatwidget

import (
	"sort"
	"strings"

	pluginapi "codex_go/internal/plugin"
)

const (
	PluginCatalogOpenAICuratedLoadingDescription = "This updates when OpenAI Curated plugins finish loading."
)

type PluginCatalogViewState struct {
	Marketplaces []pluginapi.PluginMarketplaceEntry
	SavedTabID   string
}

type PluginCatalogTabKind string

const (
	PluginCatalogTabMarketplace PluginCatalogTabKind = "marketplace"
	PluginCatalogTabLoading     PluginCatalogTabKind = "loading"
	PluginCatalogTabEmpty       PluginCatalogTabKind = "empty"
	PluginCatalogTabError       PluginCatalogTabKind = "error"
	PluginCatalogTabAll         PluginCatalogTabKind = "all"
	PluginCatalogTabInstalled   PluginCatalogTabKind = "installed"
	PluginCatalogTabAdd         PluginCatalogTabKind = "add_marketplace"
)

type PluginCatalogTab struct {
	ID          string
	Label       string
	Kind        PluginCatalogTabKind
	Order       int
	Marketplace string
	Item        SelectionItem
}

type PluginCatalogRemoteSectionError struct {
	SectionID string
	Label     string
	Message   string
}

type PluginCatalogTabsOptions struct {
	RemoteSectionsLoading bool
	RemoteSectionsLoaded  bool
	SectionErrors         []PluginCatalogRemoteSectionError
	IncludeAll            bool
	IncludeInstalled      bool
	IncludeAddMarketplace bool
}

type pluginCatalogRemoteSection struct {
	ID                     string
	Label                  string
	LoadingTabID           string
	LoadingItemDescription string
	MarketplaceNames       []string
	ShowEmptyTab           bool
	EmptyItemName          string
	EmptyItemDescription   string
	TabOrder               int
}

var pluginCatalogRemoteSections = []pluginCatalogRemoteSection{
	{
		ID:                     "workspace",
		Label:                  "Workspace",
		LoadingTabID:           "workspace-loading",
		LoadingItemDescription: "This updates when workspace plugins finish loading.",
		MarketplaceNames:       []string{RemoteWorkspaceMarketplace},
		ShowEmptyTab:           true,
		EmptyItemName:          "No workspace plugins available",
		EmptyItemDescription:   "No workspace directory plugins are available.",
		TabOrder:               MarketplaceProductWorkspace.TabOrder(),
	},
	{
		ID:                     "shared-with-me",
		Label:                  "Shared with me",
		LoadingTabID:           "shared-with-me-loading",
		LoadingItemDescription: "This updates when shared plugins finish loading.",
		MarketplaceNames:       []string{RemoteWorkspaceSharedWithMe, RemoteWorkspaceSharedPrivate, RemoteWorkspaceSharedUnlisted},
		ShowEmptyTab:           false,
		EmptyItemName:          "No shared plugins available",
		EmptyItemDescription:   "No plugins have been shared with you.",
		TabOrder:               MarketplaceProductSharedWithMe.TabOrder(),
	},
}

func (s PluginCatalogViewState) SelectedTabID() (string, bool) {
	return MarketplaceTabIDMatchingSavedID(s.SavedTabID, s.Marketplaces)
}

func PluginCatalogMarketplaceDisplayName(entry pluginapi.PluginMarketplaceEntry) string {
	return MarketplaceDisplayName(entry)
}

func PluginCatalogTabs(response pluginapi.PluginListResponse, options PluginCatalogTabsOptions) []PluginCatalogTab {
	tabs := make([]PluginCatalogTab, 0, len(response.Marketplaces)+4)
	if options.IncludeAll {
		tabs = append(tabs, PluginCatalogTab{ID: AllPluginsTabID, Label: "All", Kind: PluginCatalogTabAll, Order: -30})
	}
	if options.IncludeInstalled {
		tabs = append(tabs, PluginCatalogTab{ID: InstalledPluginsTabID, Label: "Installed", Kind: PluginCatalogTabInstalled, Order: -20})
	}

	for _, marketplace := range response.Marketplaces {
		tab := PluginCatalogMarketplaceTab(marketplace)
		if tab.ID != "" {
			tabs = append(tabs, tab)
		}
	}
	for _, section := range pluginCatalogRemoteSections {
		if tab, ok := section.fallbackTab(response.Marketplaces, options); ok {
			tabs = append(tabs, tab)
		}
	}
	if options.IncludeAddMarketplace {
		tabs = append(tabs, PluginCatalogTab{ID: AddMarketplaceTabID, Label: "Add marketplace", Kind: PluginCatalogTabAdd, Order: 1000})
	}

	sort.SliceStable(tabs, func(i int, j int) bool {
		if tabs[i].Order != tabs[j].Order {
			return tabs[i].Order < tabs[j].Order
		}
		if strings.ToLower(tabs[i].Label) != strings.ToLower(tabs[j].Label) {
			return strings.ToLower(tabs[i].Label) < strings.ToLower(tabs[j].Label)
		}
		return tabs[i].ID < tabs[j].ID
	})
	return tabs
}

func PluginCatalogMarketplaceTab(marketplace pluginapi.PluginMarketplaceEntry) PluginCatalogTab {
	id := MarketplaceTabID(marketplace)
	label := MarketplaceDisplayName(marketplace)
	if label == "" {
		label = strings.TrimSpace(marketplace.Name)
	}
	if id == MarketplaceTabIDPrefix {
		return PluginCatalogTab{}
	}
	return PluginCatalogTab{
		ID:          id,
		Label:       label,
		Kind:        PluginCatalogTabMarketplace,
		Order:       MarketplaceProductFromEntry(marketplace).TabOrder(),
		Marketplace: strings.TrimSpace(marketplace.Name),
	}
}

func PluginCatalogTabMatchingSavedID(savedTabID string, tabs []PluginCatalogTab) (string, bool) {
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

func (s pluginCatalogRemoteSection) fallbackTab(marketplaces []pluginapi.PluginMarketplaceEntry, options PluginCatalogTabsOptions) (PluginCatalogTab, bool) {
	for _, marketplace := range marketplaces {
		if s.containsMarketplace(marketplace.Name) {
			return PluginCatalogTab{}, false
		}
	}
	switch {
	case options.RemoteSectionsLoading:
		return PluginCatalogTab{
			ID:    RemoteLoadingTabIDPrefix + s.LoadingTabID,
			Label: s.Label,
			Kind:  PluginCatalogTabLoading,
			Order: s.TabOrder,
			Item: SelectionItem{
				Name:        "Loading " + strings.ToLower(s.Label) + " plugins...",
				Description: s.LoadingItemDescription,
				Disabled:    true,
			},
		}, true
	case options.RemoteSectionsLoaded:
		if sectionError, ok := pluginCatalogSectionError(options.SectionErrors, s.ID); ok {
			return PluginCatalogTab{
				ID:    RemoteErrorTabIDPrefix + s.ID,
				Label: s.Label,
				Kind:  PluginCatalogTabError,
				Order: s.TabOrder,
				Item: SelectionItem{
					Name:        s.Label + " plugins unavailable",
					Description: sectionError.Message,
					Disabled:    true,
				},
			}, true
		}
		if !s.ShowEmptyTab {
			return PluginCatalogTab{}, false
		}
		return PluginCatalogTab{
			ID:    RemoteEmptyTabIDPrefix + s.ID,
			Label: s.Label,
			Kind:  PluginCatalogTabEmpty,
			Order: s.TabOrder,
			Item: SelectionItem{
				Name:        s.EmptyItemName,
				Description: s.EmptyItemDescription,
				Disabled:    true,
			},
		}, true
	default:
		return PluginCatalogTab{}, false
	}
}

func (s pluginCatalogRemoteSection) containsMarketplace(marketplaceName string) bool {
	marketplaceName = strings.TrimSpace(marketplaceName)
	for _, candidate := range s.MarketplaceNames {
		if marketplaceName == candidate {
			return true
		}
	}
	return false
}

func (s pluginCatalogRemoteSection) containsTabID(tabID string) bool {
	tabID = strings.TrimSpace(tabID)
	if strings.TrimPrefix(tabID, RemoteLoadingTabIDPrefix) == s.LoadingTabID ||
		strings.TrimPrefix(tabID, RemoteEmptyTabIDPrefix) == s.ID ||
		strings.TrimPrefix(tabID, RemoteErrorTabIDPrefix) == s.ID {
		return true
	}
	marketplaceName := strings.TrimPrefix(tabID, MarketplaceTabIDPrefix)
	return marketplaceName != tabID && s.containsMarketplace(marketplaceName)
}

func pluginCatalogSectionError(errors []PluginCatalogRemoteSectionError, sectionID string) (PluginCatalogRemoteSectionError, bool) {
	sectionID = strings.TrimSpace(sectionID)
	for _, err := range errors {
		if strings.TrimSpace(err.SectionID) == sectionID {
			err.Message = strings.TrimSpace(err.Message)
			if err.Message == "" {
				err.Message = "Couldn't load this plugin section."
			}
			return err, true
		}
	}
	return PluginCatalogRemoteSectionError{}, false
}
