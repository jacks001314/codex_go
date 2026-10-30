package appserver

import (
	"sync"
	"testing"
	"time"

	"codex_go/model"
)

type countingModelsManager struct {
	mu         sync.Mutex
	strategies []model.RefreshStrategy
	calls      chan model.RefreshStrategy
}

func (m *countingModelsManager) record(strategy model.RefreshStrategy) {
	m.mu.Lock()
	m.strategies = append(m.strategies, strategy)
	m.mu.Unlock()
	select {
	case m.calls <- strategy:
	default:
	}
}

func (m *countingModelsManager) ListModels(strategy model.RefreshStrategy) []model.ModelPreset {
	m.record(strategy)
	return nil
}

func (m *countingModelsManager) RawModelCatalog(strategy model.RefreshStrategy) model.ModelsResponse {
	m.record(strategy)
	return model.ModelsResponse{}
}

func (m *countingModelsManager) GetRemoteModels() []model.ModelInfo { return nil }

func (m *countingModelsManager) ModelCatalogWithOrigins(model.RefreshStrategy) []model.CatalogModel {
	return nil
}

func (m *countingModelsManager) GetDefaultModel(string, bool, model.RefreshStrategy) string {
	return ""
}

func (m *countingModelsManager) GetModelInfo(string, *model.ModelsManagerConfig) model.ModelInfo {
	return model.ModelInfo{}
}

func (m *countingModelsManager) GetCatalogModel(slug string, config *model.ModelsManagerConfig) model.CatalogModel {
	return model.ConstructCatalogModelFromCandidates(slug, m.GetRemoteModels(), model.ModelOriginBundled, config)
}

func (m *countingModelsManager) RefreshIfNewETag(string) {}
func (m *countingModelsManager) RefreshAfterAuthChange() {}
func (m *countingModelsManager) CatalogIdentity() string { return "" }

// Rust #40498: the app-server refreshes the online catalog immediately and
// then on the 4m30s interval until shutdown.
func TestModelsRefreshWorkerRefreshesOnlineCatalogLikeRust(t *testing.T) {
	if modelsRefreshInterval != 4*time.Minute+30*time.Second {
		t.Fatalf("modelsRefreshInterval = %v, want 4m30s", modelsRefreshInterval)
	}
	manager := &countingModelsManager{calls: make(chan model.RefreshStrategy, 8)}
	router := NewRuntimeRouter(RuntimeServices{Models: model.NewModelService(manager)})
	t.Cleanup(func() { _ = router.Close() })
	worker := router.spawnModelsRefreshWorker(10 * time.Millisecond)
	for index := 0; index < 2; index++ {
		select {
		case strategy := <-manager.calls:
			if strategy != model.RefreshOnline {
				t.Fatalf("refresh strategy = %q, want online", strategy)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("worker produced only %d refresh(es)", index)
		}
	}
	worker.shutdown()
	worker.wait()
	for len(manager.calls) > 0 {
		<-manager.calls
	}
	time.Sleep(30 * time.Millisecond)
	select {
	case <-manager.calls:
		t.Fatal("worker refreshed after shutdown")
	default:
	}
}
