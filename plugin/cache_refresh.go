package plugin

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
)

// refreshMarketplacePluginVersion downloads to staging and preserves the active
// tree when the publisher changes content without changing its version.
func (s *PluginService) refreshMarketplacePluginVersion(detail *PluginDetail) (bool, error) {
	return s.refreshMarketplacePluginVersionChecked(detail, false, false)
}
func (s *PluginService) refreshMarketplacePluginVersionChecked(detail *PluginDetail, requireCurrent bool, force bool, reloads ...MarketplaceConfigReload) (bool, error) {
	if detail == nil || !pluginDetailNeedsMarketplaceMaterialization(detail) {
		return false, nil
	}
	source, subdir, err := parsedMarketplacePluginSource(&detail.Summary.Source)
	if err != nil {
		return false, err
	}
	s.mu.Lock()
	root, materializer := s.marketplaceInstallRoot, s.marketplacePluginMaterializer
	policy := s.marketplacePolicy
	codexHome := s.codexHome
	s.mu.Unlock()
	if materializer == nil {
		return false, fmt.Errorf("marketplace plugin materializer is not configured")
	}
	normalized, err := policy.ValidateGitSource(source.URL, source.RefName)
	if err != nil {
		return false, err
	}
	if normalized != nil {
		source = normalized
	}
	if git, ok := materializer.(*GitMarketplaceMaterializer); ok {
		isolated := *git
		isolated.Automatic = true
		isolated.CodexHome = codexHome
		materializer = &isolated
	}
	destination := marketplacePluginInstallDestination(root, detail.Summary.MarketplaceName, detail.Summary.Name)
	subdir = marketplacePluginSubdirForMaterialization(subdir, destination)
	current := s.readPluginManifestForRootCached(materializedMarketplacePluginRoot(destination, subdir))
	staged, err := newMarketplaceStagingDir(root)
	if err != nil {
		return false, err
	}
	defer staged.Close()
	candidate := filepath.Join(staged.Path(), "candidate")
	if err := materializer.MaterializeMarketplacePlugin(source, candidate); err != nil {
		return false, err
	}
	manifest := readPluginManifestForRoot(materializedMarketplacePluginRoot(candidate, subdir), disabledManifestCache())
	if manifest == nil {
		return false, fmt.Errorf("refreshed plugin %q has no supported manifest", detail.Summary.ID)
	}
	if !force && current != nil && current.Version == manifest.Version {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		return false, err
	}
	for _, reload := range reloads {
		if reload != nil {
			if err := ensureConfiguredPluginUnchanged(reload, *detail); err != nil {
				return false, err
			}
		}
	}
	if requireCurrent {
		s.mu.Lock()
		defer s.mu.Unlock()
		active, ok := s.plugins[detail.Summary.ID]
		if !ok || !active.Summary.Installed || !reflect.DeepEqual(active.Summary.Source, detail.Summary.Source) {
			return false, fmt.Errorf("plugin source changed while refresh was pending")
		}
	}
	if err := replaceMarketplaceDestination(candidate, destination); err != nil {
		return false, err
	}
	return true, nil
}

// StartNonCuratedCacheRefresh checks each installed configured source once per
// service lifetime. Duplicate list calls share the pending work.
func (s *PluginService) StartNonCuratedCacheRefresh(onChanged func()) bool {
	return s.StartNonCuratedCacheRefreshWithMode(false, onChanged)
}

// StartNonCuratedCacheRefreshWithMode forces same-version reinstall when requested.
func (s *PluginService) StartNonCuratedCacheRefreshWithMode(force bool, onChanged func()) bool {
	return s.startNonCuratedCacheRefresh(force, nil, nil, onChanged)
}

// StartConfiguredPluginCacheRefresh derives refresh requests from the effective
// config, including installations not yet loaded into the service catalog.
func (s *PluginService) StartConfiguredPluginCacheRefresh(values map[string]any, force bool, reload MarketplaceConfigReload, onChanged func()) bool {
	if reload == nil {
		return false
	}
	s.mu.Lock()
	policy, home := s.marketplacePolicy, s.codexHome
	s.mu.Unlock()
	filter := func(values map[string]any) map[string]any {
		if policy != nil {
			if filtered, ok := policy.FilterPluginConfig(home, values); ok {
				return filtered
			}
		}
		return values
	}
	originalReload := reload
	reload = func() (map[string]any, error) {
		current, err := originalReload()
		if err != nil {
			return nil, err
		}
		return filter(current), nil
	}
	details := configuredPluginsFromConfig(filter(values), disabledManifestCache())
	if details == nil {
		details = []PluginDetail{}
	}
	return s.startNonCuratedCacheRefresh(force, details, reload, onChanged)
}

func (s *PluginService) startNonCuratedCacheRefresh(force bool, configured []PluginDetail, reload MarketplaceConfigReload, onChanged func()) bool {
	s.mu.Lock()
	var pending []PluginDetail
	if s.cacheRefreshPending == nil {
		s.cacheRefreshPending = map[string]bool{}
	}
	if s.cacheRefreshChecked == nil {
		s.cacheRefreshChecked = map[string]bool{}
	}
	candidates := configured
	if candidates == nil {
		for _, detail := range s.plugins {
			candidates = append(candidates, cloneDetail(detail))
		}
	}
	for _, detail := range candidates {
		if !detail.Summary.Installed || IsReservedMarketplaceName(detail.Summary.MarketplaceName) || !pluginDetailNeedsMarketplaceMaterialization(&detail) {
			continue
		}
		raw, _ := json.Marshal(detail.Summary.Source)
		key := detail.Summary.ID + string(raw)
		if _, running := s.cacheRefreshPending[key]; running {
			// Preserve a forced request that arrived after this download began.
			s.cacheRefreshPending[key] = s.cacheRefreshPending[key] || force
			continue
		}
		if !force && s.cacheRefreshChecked[key] {
			continue
		}
		s.cacheRefreshPending[key] = false
		pending = append(pending, cloneDetail(detail))
	}
	s.mu.Unlock()
	if len(pending) == 0 {
		return false
	}
	go func() {
		changed := false
		for _, detail := range pending {
			raw, _ := json.Marshal(detail.Summary.Source)
			key := detail.Summary.ID + string(raw)
			refreshForce := force
			for {
				updated, err := s.refreshMarketplacePluginVersionChecked(&detail, reload == nil, refreshForce, reload)
				if err == nil && updated {
					refreshed, loadErr := s.materializeMarketplacePluginDetail(&detail)
					if loadErr != nil {
						err = loadErr
					} else {
						s.mu.Lock()
						active, ok := s.plugins[detail.Summary.ID]
						if reload != nil {
							active = detail
							ok = true
						}
						if ok && reflect.DeepEqual(active.Summary.Source, detail.Summary.Source) {
							refreshed.Summary.Installed = active.Summary.Installed
							refreshed.Summary.Enabled = active.Summary.Enabled
							refreshed.Summary.ID = active.Summary.ID
							refreshed.Summary.Source = active.Summary.Source
							s.plugins[active.Summary.ID] = *refreshed
						}
						s.mu.Unlock()
					}
				}
				s.mu.Lock()
				rerun := s.cacheRefreshPending[key]
				if rerun {
					s.cacheRefreshPending[key] = false
				} else {
					delete(s.cacheRefreshPending, key)
				}
				if err == nil {
					s.cacheRefreshChecked[key] = true
				}
				s.mu.Unlock()
				if err != nil {
					slog.Warn("failed to refresh plugin cache", "plugin", detail.Summary.ID, "error", err)
				}
				changed = changed || (err == nil && updated)
				if !rerun {
					break
				}
				refreshForce = true
			}
		}
		if changed && onChanged != nil {
			onChanged()
		}
	}()
	return true
}

// ensureConfiguredPluginUnchanged rejects removed installations and source edits
// after a background download, before its staging tree becomes active.
func ensureConfiguredPluginUnchanged(reload MarketplaceConfigReload, expected PluginDetail) error {
	values, err := reload()
	if err != nil {
		return fmt.Errorf("reload plugin configuration: %w", err)
	}
	for _, current := range configuredPluginsFromConfig(values, disabledManifestCache()) {
		if current.Summary.ID == expected.Summary.ID && reflect.DeepEqual(current.Summary.Source, expected.Summary.Source) {
			return nil
		}
	}
	return fmt.Errorf("configured plugin %q changed while refresh was pending", expected.Summary.ID)
}
