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
	// Rust #59823: runtime auth binds to the provider's selected credential
	// source. A provider that selects an explicit key or command credential must
	// not reuse (or fall back to) the saved login runtime.
	if providerUsesExplicitCredentials(options.Values) {
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

// providerUsesExplicitCredentials reports whether the selected model provider
// supplies its own credential (env_key, experimental bearer token, or a command
// auth block), which the saved-login runtime must never shadow (Rust #59823).
func providerUsesExplicitCredentials(values map[string]any) bool {
	providerID, _ := values["model_provider"].(string)
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		providerID = "openai"
	}
	if token, _ := values["experimental_bearer_token"].(string); strings.TrimSpace(token) != "" {
		return true
	}
	providers, _ := values["model_providers"].(map[string]any)
	if providers == nil {
		return false
	}
	direct, _ := providers[providerID].(map[string]any)
	if direct == nil {
		return false
	}
	if key, _ := direct["env_key"].(string); strings.TrimSpace(key) != "" {
		return true
	}
	if token, _ := direct["experimental_bearer_token"].(string); strings.TrimSpace(token) != "" {
		return true
	}
	if auth, ok := direct["auth"].(map[string]any); ok && len(auth) > 0 {
		return true
	}
	return false
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
