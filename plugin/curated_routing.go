package plugin

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	OpenAICuratedMarketplaceName       = "openai-curated"
	OpenAIAPICuratedMarketplaceName    = "openai-api-curated"
	OpenAIRemoteCuratedMarketplaceName = "openai-curated-remote"
	AmazonBedrockModelProviderID       = "amazon-bedrock"
)

type TargetCuratedMarketplace string

const (
	TargetCuratedOpenAI           TargetCuratedMarketplace = "openai"
	TargetCuratedOpenAIWithRemote TargetCuratedMarketplace = "openai-with-remote"
	TargetCuratedOpenAIAPI        TargetCuratedMarketplace = "openai-api"
)

func TargetCuratedMarketplaceForRuntime(authMode string, modelProviderID string) TargetCuratedMarketplace {
	authMode = strings.TrimSpace(authMode)
	if authMode != "" {
		if authModeUsesCodexBackend(authMode) {
			return TargetCuratedOpenAIWithRemote
		}
		return TargetCuratedOpenAIAPI
	}
	// Rust #38429 selects the curated catalog from authentication alone. An
	// absent auth mode routes to the API-key curated marketplace regardless of
	// the resolved model provider.
	return TargetCuratedOpenAIAPI
}

func authModeUsesCodexBackend(mode string) bool {
	switch strings.TrimSpace(mode) {
	case "chatgpt", "chatgptAuthTokens", "agent-identity", "personal-access-token":
		return true
	default:
		return false
	}
}

func (s *PluginService) SetRuntimeRoute(authMode string, modelProviderID string) bool {
	if s == nil {
		return false
	}
	target := TargetCuratedMarketplaceForRuntime(authMode, modelProviderID)
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := s.targetCuratedMarketplace != target
	s.authMode = strings.TrimSpace(authMode)
	s.modelProviderID = strings.TrimSpace(modelProviderID)
	s.targetCuratedMarketplace = target
	return changed
}

func (s *PluginService) TargetCuratedMarketplace() TargetCuratedMarketplace {
	if s == nil {
		return TargetCuratedOpenAI
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.targetCuratedMarketplace == "" {
		return TargetCuratedOpenAI
	}
	return s.targetCuratedMarketplace
}

func pluginEligibleForCuratedTarget(id string, marketplaceName string, target TargetCuratedMarketplace) bool {
	marketplaceName = strings.TrimSpace(marketplaceName)
	if marketplaceName == "" {
		marketplaceName = pluginMarketplaceFromID(id)
	}
	switch target {
	case TargetCuratedOpenAIWithRemote:
		return marketplaceName != OpenAIAPICuratedMarketplaceName
	case TargetCuratedOpenAIAPI:
		return marketplaceName != OpenAICuratedMarketplaceName && marketplaceName != OpenAIRemoteCuratedMarketplaceName
	default:
		return marketplaceName != OpenAIAPICuratedMarketplaceName && marketplaceName != OpenAIRemoteCuratedMarketplaceName
	}
}

func (s *PluginService) pluginEligibleForRuntimeLocked(detail PluginDetail) bool {
	if !pluginEligibleForCuratedTarget(detail.Summary.ID, detail.Summary.MarketplaceName, s.targetCuratedMarketplace) {
		return false
	}
	if s.targetCuratedMarketplace != TargetCuratedOpenAIWithRemote || detail.Summary.MarketplaceName != OpenAICuratedMarketplaceName {
		return true
	}
	for _, candidate := range s.plugins {
		if candidate.Summary.MarketplaceName == OpenAIRemoteCuratedMarketplaceName && candidate.Summary.Name == detail.Summary.Name {
			return false
		}
	}
	return true
}

func routePluginDetails(details []PluginDetail, target TargetCuratedMarketplace) []PluginDetail {
	remoteNames := map[string]bool{}
	if target == TargetCuratedOpenAIWithRemote {
		for i := range details {
			if details[i].Summary.MarketplaceName == OpenAIRemoteCuratedMarketplaceName {
				remoteNames[details[i].Summary.Name] = true
			}
		}
	}
	out := make([]PluginDetail, 0, len(details))
	for _, detail := range details {
		if !pluginEligibleForCuratedTarget(detail.Summary.ID, detail.Summary.MarketplaceName, target) {
			continue
		}
		if target == TargetCuratedOpenAIWithRemote && detail.Summary.MarketplaceName == OpenAICuratedMarketplaceName && remoteNames[detail.Summary.Name] {
			continue
		}
		out = append(out, detail)
	}
	return out
}

func routePluginSummaries(plugins []PluginSummary, target TargetCuratedMarketplace) []PluginSummary {
	details := make([]PluginDetail, 0, len(plugins))
	for _, summary := range plugins {
		details = append(details, PluginDetail{Summary: summary})
	}
	routed := routePluginDetails(details, target)
	out := make([]PluginSummary, 0, len(routed))
	for _, detail := range routed {
		out = append(out, detail.Summary)
	}
	return out
}

func (s *PluginService) enabledPluginDetailsSnapshot() []PluginDetail {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	stored := make([]PluginDetail, 0, len(s.plugins))
	for _, detail := range s.plugins {
		stored = append(stored, cloneDetail(detail))
	}
	marketplaces := s.marketplaceListLocked()
	target := s.targetCuratedMarketplace
	s.mu.Unlock()
	discovered, _ := loadMarketplacePlugins(marketplaces)
	details := routePluginDetails(mergePluginDetails(stored, discovered), target)
	out := make([]PluginDetail, 0, len(details))
	for _, detail := range details {
		if detail.Summary.Installed && detail.Summary.Enabled {
			out = append(out, detail)
		}
	}
	return out
}

func routeMarketplaces(marketplaces []Marketplace, target TargetCuratedMarketplace) []Marketplace {
	out := make([]Marketplace, 0, len(marketplaces))
	for _, marketplace := range marketplaces {
		if pluginEligibleForCuratedTarget("", marketplace.Name, target) {
			out = append(out, marketplace)
		}
	}
	return out
}

func (s *PluginService) implicitCuratedMarketplaceLocked() *Marketplace {
	codexHome := strings.TrimSpace(s.codexHome)
	if codexHome == "" {
		return nil
	}
	root := filepath.Join(codexHome, ".tmp", "plugins")
	name := OpenAICuratedMarketplaceName
	if s.targetCuratedMarketplace == TargetCuratedOpenAIAPI {
		root = filepath.Join(root, ".agents", "plugins", "api_marketplace.json")
		name = OpenAIAPICuratedMarketplaceName
	}
	if info, err := os.Stat(root); err != nil || info.IsDir() == (name == OpenAIAPICuratedMarketplaceName) {
		return nil
	}
	return &Marketplace{Name: name, SourceType: string(MarketplaceSourceLocal), SourceURL: root, RootPath: filepath.Clean(root)}
}

func appendImplicitCuratedMarketplace(marketplaces []Marketplace, curated *Marketplace) []Marketplace {
	if curated == nil {
		return marketplaces
	}
	for _, marketplace := range marketplaces {
		if marketplace.Name == curated.Name || filepath.Clean(marketplace.RootPath) == filepath.Clean(curated.RootPath) {
			return marketplaces
		}
	}
	marketplaces = append(marketplaces, *curated)
	sort.SliceStable(marketplaces, func(i int, j int) bool { return marketplaces[i].Name < marketplaces[j].Name })
	return marketplaces
}
