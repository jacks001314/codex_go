package plugin

import (
	"fmt"
	"path/filepath"
	"strings"
)

func (s *PluginService) marketplacePolicySnapshotLocked() (*MarketplacePolicy, map[string]any, string) {
	if s == nil {
		return nil, nil, ""
	}
	return s.marketplacePolicy, s.marketplacePolicyConfig, s.codexHome
}

func (s *PluginService) filterMarketplacesByPolicyLocked(marketplaces []Marketplace) []Marketplace {
	policy, config, codexHome := s.marketplacePolicySnapshotLocked()
	if policy == nil {
		return marketplaces
	}
	out := make([]Marketplace, 0, len(marketplaces))
	for _, marketplace := range marketplaces {
		if marketplaceAllowedByPolicy(policy, config, codexHome, marketplace) {
			out = append(out, marketplace)
		}
	}
	return out
}

func marketplaceAllowedByPolicy(policy *MarketplacePolicy, configValues map[string]any, codexHome string, marketplace Marketplace) bool {
	if marketplace.Name == OpenAIRemoteCuratedMarketplaceName {
		return true
	}
	if policy == nil {
		return true
	}
	path := findMarketplaceManifestPath(marketplace.RootPath)
	if path == "" {
		path = filepath.Clean(marketplace.RootPath)
	}
	if path == "" || path == "." {
		return !policy.IsRestricted()
	}
	return policy.ValidateInstall(codexHome, path, marketplace.Name, configValues) == nil
}

func (s *PluginService) filterPluginDetailsForPolicyLocked(details []PluginDetail, marketplaces []Marketplace) []PluginDetail {
	policy, _, _ := s.marketplacePolicySnapshotLocked()
	return filterPluginDetailsByMarketplacePolicy(details, marketplaces, policy)
}

func (s *PluginService) validatePluginInstallPolicyLocked(detail PluginDetail) error {
	policy, configValues, codexHome := s.marketplacePolicySnapshotLocked()
	if policy == nil {
		return nil
	}
	marketplaceName := firstNonEmpty(detail.Summary.MarketplaceName, pluginMarketplaceFromID(detail.Summary.ID), detail.MarketplaceName)
	if isRemoteMarketplaceName(marketplaceName) {
		return nil
	}
	root := strings.TrimSpace(detail.MarketplaceRoot)
	if root == "" {
		if marketplace, ok := s.marketplaces[marketplaceName]; ok {
			root = marketplace.RootPath
		}
	}
	path := ""
	if detail.MarketplacePath != nil {
		path = strings.TrimSpace(*detail.MarketplacePath)
	}
	if path == "" {
		path = findMarketplaceManifestPath(root)
	}
	if path == "" {
		path = filepath.Clean(root)
	}
	if path == "" || path == "." {
		if !policy.IsRestricted() && !IsReservedMarketplaceName(marketplaceName) {
			return nil
		}
		return fmt.Errorf("marketplace `%s` does not have a usable root", marketplaceName)
	}
	return policy.ValidateInstall(codexHome, path, marketplaceName, configValues)
}

func filterPluginDetailsByMarketplacePolicy(details []PluginDetail, marketplaces []Marketplace, policy *MarketplacePolicy) []PluginDetail {
	if policy == nil {
		return details
	}
	allowedMarketplaces := make(map[string]bool, len(marketplaces))
	for _, marketplace := range marketplaces {
		allowedMarketplaces[marketplace.Name] = true
	}
	out := make([]PluginDetail, 0, len(details))
	for _, detail := range details {
		if pluginDetailAllowedByMarketplacePolicy(policy, allowedMarketplaces, detail) {
			out = append(out, detail)
		}
	}
	return out
}

func pluginDetailAllowedByMarketplacePolicy(policy *MarketplacePolicy, allowedMarketplaces map[string]bool, detail PluginDetail) bool {
	marketplaceName := firstNonEmpty(detail.Summary.MarketplaceName, pluginMarketplaceFromID(detail.Summary.ID), detail.MarketplaceName)
	if isRemoteMarketplaceName(marketplaceName) {
		return true
	}
	if allowedMarketplaces[marketplaceName] {
		return true
	}
	if policy == nil {
		return true
	}
	switch marketplaceName {
	case OpenAIBundledMarketplaceName, "openai-bundled-alpha", "openai-primary-runtime":
		return true
	case OpenAICuratedMarketplaceName, OpenAIAPICuratedMarketplaceName:
		_, err := policy.ValidateGitSource(OpenAIPluginsGitURL, nil)
		return err == nil
	default:
		return !policy.IsRestricted() && !IsReservedMarketplaceName(strings.TrimSpace(marketplaceName))
	}
}

func isRemoteMarketplaceName(name string) bool {
	_, ok := RemoteCatalogScopeForMarketplace(name)
	return ok
}

func (s *PluginService) recordPolicyMarketplaceLocked(marketplace Marketplace) {
	if s == nil || s.marketplacePolicyConfig == nil {
		return
	}
	table, ok := configuredMarketplacesTable(s.marketplacePolicyConfig)
	if !ok {
		table = map[string]any{}
		s.marketplacePolicyConfig["marketplaces"] = table
	}
	entry := map[string]any{
		"source_type": marketplace.SourceType,
		"source":      marketplaceConfigSource(&marketplace),
	}
	if marketplace.RefName != nil && strings.TrimSpace(*marketplace.RefName) != "" {
		entry["ref"] = strings.TrimSpace(*marketplace.RefName)
	}
	table[marketplace.Name] = entry
}
