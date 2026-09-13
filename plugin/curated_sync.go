package plugin

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

const OpenAIPluginsGitURL = "https://github.com/openai/plugins.git"

// CuratedSyncMetrics reports one curated plugin startup-sync attempt (Rust's
// emit_curated_plugins_startup_sync_counter): the transport ("git") and whether
// it succeeded, with Final marking the final-metric sample.
type CuratedSyncMetrics struct {
	Transport string
	Status    string
	Final     bool
}

// CuratedSyncMetricsObserver receives every curated-sync metric sample. The
// plugin package cannot import codex_go/telemetry (telemetry reaches back here
// through tool), so the app layer installs the sink.
type CuratedSyncMetricsObserver func(CuratedSyncMetrics)

// SetCuratedSyncMetricsObserver installs the observer used to record curated
// plugin startup-sync metrics. A nil observer disables recording.
func (s *PluginService) SetCuratedSyncMetricsObserver(observer CuratedSyncMetricsObserver) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.curatedSyncMetricsObserver = observer
}

func (s *PluginService) recordCuratedSyncMetrics(transport string, status string, final bool) {
	if s == nil {
		return
	}
	s.mu.Lock()
	observer := s.curatedSyncMetricsObserver
	s.mu.Unlock()
	if observer == nil {
		return
	}
	observer(CuratedSyncMetrics{Transport: transport, Status: status, Final: final})
}

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
		if err != nil {
			// Rust falls back to its GitHub HTTP and export-archive transports
			// here; Go has only the git transport, so it records the failed
			// attempt and no final sample (Rust's final names a fallback
			// transport).
			s.recordCuratedSyncMetrics("git", "failure", false)
			slog.Warn("curated plugin sync failed", "error", err)
			return
		}
		s.recordCuratedSyncMetrics("git", "success", false)
		s.recordCuratedSyncMetrics("git", "success", true)
		if onChanged != nil {
			onChanged()
		}
	}()
	return true
}
