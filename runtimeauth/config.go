package runtimeauth

import (
	"strings"
	"sync"

	"codex_go/features"
	"codex_go/install"
)

// FeatureEnabled reports whether runtime agent instances are enabled for the
// resolved feature settings.
func FeatureEnabled(settings map[string]bool) bool {
	return features.Enabled(settings, "runtime_agent_instances")
}

var sharedManagers sync.Map

// SharedManager returns the harness-wide manager for one Codex home so
// analytics delivery, turn-cost queries, cloud requests, and plugin traffic
// reuse the same runtime identity (Rust RuntimeAuthSlot).
func SharedManager(codexHome string, installationID string, metadata Metadata) (*Manager, error) {
	key := strings.TrimSpace(codexHome) + "|" + strings.TrimSpace(installationID)
	if cached, ok := sharedManagers.Load(key); ok {
		return cached.(*Manager), nil
	}
	manager, err := NewStoreManager(codexHome, installationID, metadata)
	if err != nil {
		return nil, err
	}
	actual, _ := sharedManagers.LoadOrStore(key, manager)
	return actual.(*Manager), nil
}

// ProviderOptions describe a config-derived runtime provider.
type ProviderOptions struct {
	Values          map[string]any
	FeatureSettings map[string]bool
	CodexHome       string
	InstallationID  string
	Selection       Selection
	Harness         string
	HarnessVersion  string
}

// ProviderForConfig returns the runtime provider for the current configuration,
// or nil when the feature is disabled or the host has no installation id.
func ProviderForConfig(options ProviderOptions) *Provider {
	if !FeatureEnabled(options.FeatureSettings) {
		return nil
	}
	if strings.TrimSpace(options.InstallationID) == "" || strings.TrimSpace(options.CodexHome) == "" {
		return nil
	}
	manager, err := SharedManager(options.CodexHome, options.InstallationID, Metadata{
		Harness:        options.Harness,
		HarnessVersion: options.HarnessVersion,
	})
	if err != nil {
		return nil
	}
	return &Provider{
		Manager:   manager,
		Mode:      ModeFromConfigValues(options.Values),
		Selection: options.Selection,
	}
}

// ProviderForHarness is ProviderForConfig plus the installer's host id. It
// returns nil for destinations that must keep their own authentication.
func ProviderForHarness(codexHome string, values map[string]any, settings map[string]bool, baseURL string, harness string) *Provider {
	if ClassifyOpenAIDestination(baseURL) != DestinationSupported {
		return nil
	}
	installationID, err := install.ResolveInstallationID(codexHome)
	if err != nil {
		return nil
	}
	return ProviderForConfig(ProviderOptions{
		Values:          values,
		FeatureSettings: settings,
		CodexHome:       codexHome,
		InstallationID:  installationID,
		Harness:         harness,
	})
}
