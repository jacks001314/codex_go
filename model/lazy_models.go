package model

import "sync"

// LazyModelsManager defers building the underlying ModelsManager until its
// first use, caching the successful result. It is used to feed an
// account-scoped model catalog into a long-lived service (for example the
// app-server Models service) whose auth is resolved lazily after construction.
// If the builder fails or returns nil, it falls back to the bundled static
// catalog (Rust #41467 / app-server account-scoped catalog refresh).
type LazyModelsManager struct {
	build func() (ModelsManager, error)

	mu                          sync.Mutex
	cached                      ModelsManager
	done                        bool
	apiKeyModelDiscoverySet     bool
	apiKeyModelDiscoveryEnabled bool
}

func NewLazyModelsManager(build func() (ModelsManager, error)) *LazyModelsManager {
	return &LazyModelsManager{build: build}
}

func (m *LazyModelsManager) resolve() ModelsManager {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.done {
		if m.cached != nil {
			return m.cached
		}
		return NewStaticModelsManager(BundledModelsResponse())
	}
	m.done = true
	manager := ModelsManager(nil)
	if m.build != nil {
		built, err := m.build()
		if err == nil {
			manager = built
		}
	}
	if manager == nil {
		manager = NewStaticModelsManager(BundledModelsResponse())
	}
	if m.apiKeyModelDiscoverySet {
		SetAPIKeyModelDiscoveryEnabled(manager, m.apiKeyModelDiscoveryEnabled)
	}
	m.cached = manager
	return manager
}

// SetAPIKeyModelDiscoveryEnabled forwards the startup API-key discovery policy to
// the resolved manager, remembering it until the manager is first built (Rust
// #44392).
func (m *LazyModelsManager) SetAPIKeyModelDiscoveryEnabled(enabled bool) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.apiKeyModelDiscoverySet = true
	m.apiKeyModelDiscoveryEnabled = enabled
	cached := m.cached
	m.mu.Unlock()
	if cached != nil {
		SetAPIKeyModelDiscoveryEnabled(cached, enabled)
	}
}

func (m *LazyModelsManager) ListModels(strategy RefreshStrategy) []ModelPreset {
	return m.resolve().ListModels(strategy)
}

func (m *LazyModelsManager) RawModelCatalog(strategy RefreshStrategy) ModelsResponse {
	return m.resolve().RawModelCatalog(strategy)
}

func (m *LazyModelsManager) GetRemoteModels() []ModelInfo {
	return m.resolve().GetRemoteModels()
}

func (m *LazyModelsManager) GetDefaultModel(model string, allowProviderModelFallback bool, strategy RefreshStrategy) string {
	return m.resolve().GetDefaultModel(model, allowProviderModelFallback, strategy)
}

func (m *LazyModelsManager) GetModelInfo(model string, config *ModelsManagerConfig) ModelInfo {
	return m.resolve().GetModelInfo(model, config)
}

func (m *LazyModelsManager) RefreshIfNewETag(etag string) {
	m.resolve().RefreshIfNewETag(etag)
}

// RefreshAfterAuthChange forwards Rust #46508's best-effort catalog refresh to
// the resolved manager.
func (m *LazyModelsManager) RefreshAfterAuthChange() {
	if m == nil {
		return
	}
	m.resolve().RefreshAfterAuthChange()
}

// CatalogIdentity forwards the running catalog's identity (Rust #46508).
func (m *LazyModelsManager) CatalogIdentity() string {
	if m == nil {
		return ""
	}
	return m.resolve().CatalogIdentity()
}
