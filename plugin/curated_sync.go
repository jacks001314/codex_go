package plugin

import (
	"os"
	"path/filepath"
	"strings"
)

const OpenAIPluginsGitURL = "https://github.com/openai/plugins.git"

func (s *PluginService) HasConfiguredCuratedPlugins() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, detail := range s.plugins {
		marketplace := firstNonEmpty(detail.Summary.MarketplaceName, pluginMarketplaceFromID(id))
		if marketplace == OpenAICuratedMarketplaceName || marketplace == OpenAIAPICuratedMarketplaceName {
			return true
		}
	}
	return false
}

// StartCuratedRepoSync materializes the shared OpenAI plugin repository once per
// service at a time. Completion is reported only after the new catalog is on disk.
func (s *PluginService) StartCuratedRepoSync(onChanged func()) bool {
	if s == nil || s.TargetCuratedMarketplace() == TargetCuratedOpenAIWithRemote {
		return false
	}
	s.mu.Lock()
	if s.curatedSyncInFlight || strings.TrimSpace(s.codexHome) == "" {
		s.mu.Unlock()
		return false
	}
	s.curatedSyncInFlight = true
	codexHome := s.codexHome
	materializer := s.marketplaceMaterializer
	if gitMaterializer, ok := materializer.(*GitMarketplaceMaterializer); ok {
		// Rust #39520: background curated sync must not inherit repository or
		// command-scoped Git configuration.
		isolated := *gitMaterializer
		isolated.Automatic = true
		materializer = &isolated
	}
	s.mu.Unlock()

	go func() {
		destination := filepath.Join(codexHome, ".tmp", "plugins")
		source := &ParsedMarketplaceSource{Kind: MarketplaceSourceGit, URL: OpenAIPluginsGitURL}
		var err error
		if info, statErr := os.Stat(destination); statErr == nil && info.IsDir() {
			if upgrader, ok := materializer.(MarketplaceUpgrader); ok {
				err = upgrader.UpgradeMarketplace(source, nil, destination)
			}
		} else if statErr == nil || os.IsNotExist(statErr) {
			if materializer != nil {
				err = materializer.MaterializeMarketplace(source, nil, destination)
			}
		} else {
			err = statErr
		}
		s.mu.Lock()
		s.curatedSyncInFlight = false
		s.mu.Unlock()
		if err == nil && onChanged != nil {
			onChanged()
		}
	}()
	return true
}
