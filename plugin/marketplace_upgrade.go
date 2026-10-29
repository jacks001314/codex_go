package plugin

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// MarketplaceConfigReload reloads the effective merged configuration with the
// initiating operation's loader and overrides. It is called on the blocking
// upgrade worker immediately before a downloaded snapshot is activated.
type MarketplaceConfigReload func() (map[string]any, error)

// UpgradeConfiguredMarketplaces upgrades the Git marketplaces declared by the
// effective merged configuration (Rust #42149). Unlike UpgradeMarketplace it
// does not depend on the user config.toml table: system, managed, project, and
// CLI layers all contribute. reload re-reads the same configuration before
// activation; a changed definition, a removed marketplace, or a reload failure
// rolls the snapshot back instead of activating it. reload may be nil when the
// caller has no reloadable configuration, which skips the post-download check.
func (s *PluginService) UpgradeConfiguredMarketplaces(values map[string]any, marketplaceName string, reload MarketplaceConfigReload) (*MarketplaceUpgradeResponse, error) {
	return s.upgradeConfiguredMarketplaces(values, marketplaceName, reload, false)
}

// StartConfiguredMarketplaceAutoUpgrade runs the startup synchronization of
// Git marketplaces selected from the effective merged configuration (Rust
// #42149). It mirrors Rust's automatic mode: one upgrade runs at a time,
// background Git operations ignore repository-scoped configuration, and a
// successful activation refreshes derived plugin state.
func (s *PluginService) StartConfiguredMarketplaceAutoUpgrade(values map[string]any, reload MarketplaceConfigReload, onChanged func()) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	if s.marketplaceStartupUpgradeBusy {
		s.mu.Unlock()
		return false
	}
	s.marketplaceStartupUpgradeBusy = true
	s.mu.Unlock()
	go func() {
		defer func() {
			s.mu.Lock()
			s.marketplaceStartupUpgradeBusy = false
			s.mu.Unlock()
		}()
		outcome, err := s.upgradeConfiguredMarketplaces(values, "", reload, true)
		if err != nil {
			slog.Warn("failed to auto-upgrade configured marketplaces", "error", err)
			return
		}
		for _, item := range outcome.Errors {
			slog.Warn("failed to auto-upgrade configured marketplace", "marketplace", item.MarketplaceName, "error", item.Message)
		}
		if len(outcome.UpgradedRoots) > 0 && onChanged != nil {
			onChanged()
		}
	}()
	return true
}

func (s *PluginService) upgradeConfiguredMarketplaces(values map[string]any, marketplaceName string, reload MarketplaceConfigReload, automatic bool) (*MarketplaceUpgradeResponse, error) {
	if s == nil {
		return &MarketplaceUpgradeResponse{SelectedMarketplaces: []string{}, UpgradedRoots: []string{}, Errors: []MarketplaceUpgradeErrorInfo{}}, nil
	}
	s.mu.Lock()
	installRoot := s.marketplaceInstallRoot
	materializer := s.marketplaceMaterializer
	resolver := s.marketplaceRevision
	now := s.now
	s.mu.Unlock()
	if automatic {
		if gitMaterializer, ok := materializer.(*GitMarketplaceMaterializer); ok {
			isolated := *gitMaterializer
			isolated.Automatic = true
			materializer = &isolated
		}
		if gitResolver, ok := resolver.(*GitMarketplaceRevisionResolver); ok {
			isolated := *gitResolver
			isolated.Automatic = true
			resolver = &isolated
		}
	}

	entries := configuredMarketplacesFromConfig(values, installRoot, now)
	target := strings.TrimSpace(marketplaceName)
	selected := make([]string, 0, len(entries))
	roots := make([]string, 0, len(entries))
	var upgradeErrors []MarketplaceUpgradeErrorInfo
	updatedMarketplaces := map[string]Marketplace{}

	for _, marketplace := range entries {
		name := strings.TrimSpace(marketplace.Name)
		if target != "" && target != name {
			continue
		}
		if marketplace.SourceType != string(MarketplaceSourceGit) {
			continue
		}
		selected = append(selected, name)
		if IsReservedMarketplaceName(name) {
			upgradeErrors = append(upgradeErrors, MarketplaceUpgradeErrorInfo{
				MarketplaceName: name,
				Message:         fmt.Sprintf("marketplace `%s` is reserved and cannot be upgraded from this source", name),
			})
			continue
		}
		if _, err := safeMarketplaceDirName(name); err != nil {
			upgradeErrors = append(upgradeErrors, MarketplaceUpgradeErrorInfo{MarketplaceName: name, Message: err.Error()})
			continue
		}
		source := &ParsedMarketplaceSource{
			Kind:    MarketplaceSourceGit,
			URL:     marketplaceConfigSource(&marketplace),
			RefName: cloneStringPtr(marketplace.RefName),
		}
		if strings.TrimSpace(source.URL) == "" {
			upgradeErrors = append(upgradeErrors, MarketplaceUpgradeErrorInfo{
				MarketplaceName: name,
				Message:         "configured Git marketplace is missing source",
			})
			continue
		}
		revision := ""
		if resolver != nil {
			resolved, err := resolver.MarketplaceRevision(source)
			if err != nil {
				upgradeErrors = append(upgradeErrors, MarketplaceUpgradeErrorInfo{MarketplaceName: name, Message: err.Error()})
				continue
			}
			revision = strings.TrimSpace(resolved)
		}
		destination := marketplace.RootPath
		if revision != "" && installedMarketplaceRevision(destination) == revision {
			continue
		}
		if err := upgradeConfiguredGitMarketplace(installRoot, destination, materializer, source, marketplace.SparsePaths, revision, reload, marketplace, automatic); err != nil {
			upgradeErrors = append(upgradeErrors, MarketplaceUpgradeErrorInfo{MarketplaceName: name, Message: err.Error()})
			continue
		}
		updatedMarketplaces[name] = marketplace
		roots = append(roots, destination)
	}
	if target != "" && len(selected) == 0 {
		return nil, fmt.Errorf("%w: marketplace `%s` is not configured as a Git marketplace", ErrInvalidPluginRequest, target)
	}
	sort.Strings(selected)
	sort.Strings(roots)
	if len(updatedMarketplaces) > 0 {
		s.mu.Lock()
		for name, marketplace := range updatedMarketplaces {
			s.marketplaces[name] = marketplace
		}
		s.mu.Unlock()
	}
	upgradeErrors = append(upgradeErrors, s.clearUninstalledMaterializedPluginsAfterMarketplaceUpgrade(updatedMarketplaces)...)
	upgradeErrors = append(upgradeErrors, s.refreshInstalledPluginsAfterMarketplaceUpgrade(updatedMarketplaces)...)
	sort.SliceStable(upgradeErrors, func(i int, j int) bool {
		return upgradeErrors[i].MarketplaceName < upgradeErrors[j].MarketplaceName
	})
	if roots == nil {
		roots = []string{}
	}
	if selected == nil {
		selected = []string{}
	}
	return &MarketplaceUpgradeResponse{SelectedMarketplaces: selected, UpgradedRoots: roots, Errors: upgradeErrors}, nil
}

// upgradeConfiguredGitMarketplace downloads one configured marketplace into a
// staging directory, verifies its definition against reloaded configuration,
// and only then swaps it into place. A verification failure leaves the previous
// snapshot untouched.
func upgradeConfiguredGitMarketplace(
	installRoot string,
	destination string,
	materializer MarketplaceMaterializer,
	source *ParsedMarketplaceSource,
	sparsePaths []string,
	revision string,
	reload MarketplaceConfigReload,
	expected Marketplace,
	automatic bool,
) error {
	if strings.TrimSpace(destination) == "" {
		return fmt.Errorf("marketplace %q has no install destination", expected.Name)
	}
	parent := filepath.Dir(destination)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("failed to create marketplace install directory %s: %w", parent, err)
	}
	cleanupMarketplaceStaging(installRoot, time.Now())
	staged, err := newMarketplaceStagingDir(installRoot)
	if err != nil {
		return fmt.Errorf("failed to create temporary marketplace upgrade directory in %s: %w", installRoot, err)
	}
	defer staged.Close()
	switch {
	case isGitMarketplaceMaterializer(materializer):
		// The configured upgrade owns staging and activation, so the built-in
		// Git materializer must not create a second staging tree underneath it.
		if err := cloneGitMarketplaceSource(source.URL, source.RefName, sparsePaths, staged.Path(), automatic); err != nil {
			return err
		}
	case materializer != nil:
		if upgrader, ok := materializer.(MarketplaceUpgrader); ok {
			if err := upgrader.UpgradeMarketplace(source, sparsePaths, staged.Path()); err != nil {
				return err
			}
		} else if err := materializer.MaterializeMarketplace(source, sparsePaths, staged.Path()); err != nil {
			return err
		}
	default:
		if err := cloneGitMarketplaceSource(source.URL, source.RefName, sparsePaths, staged.Path(), automatic); err != nil {
			return err
		}
	}
	if err := writeMarketplaceInstallState(staged.Path(), revision); err != nil {
		return err
	}
	if reload != nil {
		if err := ensureConfiguredMarketplaceUnchanged(reload, expected); err != nil {
			return err
		}
	}
	if err := replaceMarketplaceDestination(staged.Path(), destination); err != nil {
		return err
	}
	return nil
}

func isGitMarketplaceMaterializer(materializer MarketplaceMaterializer) bool {
	_, ok := materializer.(*GitMarketplaceMaterializer)
	return ok
}

// ensureConfiguredMarketplaceUnchanged mirrors Rust's
// ensure_configured_git_marketplace_unchanged: reload with the operation's
// loader and compare the marketplace's source, ref, and sparse paths.
func ensureConfiguredMarketplaceUnchanged(reload MarketplaceConfigReload, expected Marketplace) error {
	values, err := reload()
	if err != nil {
		return fmt.Errorf("failed to reload config while checking marketplace upgrade: %w", err)
	}
	current, found, err := configuredGitMarketplaceByName(values, expected.Name)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("configured marketplace `%s` was removed or is no longer a Git marketplace", expected.Name)
	}
	if !configuredMarketplaceEqual(current, expected) {
		return fmt.Errorf("configured marketplace `%s` changed while auto-upgrade was in flight", expected.Name)
	}
	return nil
}

func configuredGitMarketplaceByName(values map[string]any, name string) (Marketplace, bool, error) {
	table, ok := values["marketplaces"].(map[string]any)
	if !ok {
		return Marketplace{}, false, nil
	}
	raw, ok := table[name].(map[string]any)
	if !ok {
		return Marketplace{}, false, nil
	}
	if stringConfigValue(raw["source_type"]) != string(MarketplaceSourceGit) {
		return Marketplace{}, false, nil
	}
	entries := configuredMarketplacesFromConfig(map[string]any{"marketplaces": map[string]any{name: raw}}, "", nil)
	if len(entries) == 0 {
		return Marketplace{}, false, fmt.Errorf("invalid configured Git marketplace `%s`", name)
	}
	return entries[0], true, nil
}

func configuredMarketplaceEqual(left, right Marketplace) bool {
	return strings.TrimSpace(left.Name) == strings.TrimSpace(right.Name) &&
		left.SourceType == right.SourceType &&
		strings.TrimSpace(left.SourceURL) == strings.TrimSpace(right.SourceURL) &&
		stringPtrValue(left.RefName) == stringPtrValue(right.RefName) &&
		strings.Join(left.SparsePaths, "\x00") == strings.Join(right.SparsePaths, "\x00")
}

func stringPtrValue(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}
