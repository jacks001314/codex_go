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

	mu     sync.Mutex
	cached ModelsManager
	done   bool
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
	if m.build == nil {
		return NewStaticModelsManager(BundledModelsResponse())
	}
	manager, err := m.build()
	if err != nil || manager == nil {
		return NewStaticModelsManager(BundledModelsResponse())
	}
	m.cached = manager
	return manager
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
