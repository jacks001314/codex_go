package appserver

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"codex_go/config"
	"codex_go/model"
	"codex_go/session"
	"codex_go/turn"
)

// Mirrors Rust #49135: an explicit provider catalog with no models and no
// configured model rejects thread startup instead of falling back to bundled
// metadata.
func TestThreadStartRequiresModelFromEmptyExplicitCatalogLikeRust(t *testing.T) {
	catalog := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"models":[]}`))
	}))
	defer catalog.Close()

	home := t.TempDir()
	body := `model_provider = "catalog-test"

[model_providers.catalog-test]
name = "Catalog Test"
base_url = "` + catalog.URL + `/v1"
model_catalog_url = "` + catalog.URL + `/codex/models"
wire_api = "responses"
requires_openai_auth = false
`
	if err := os.WriteFile(config.ConfigPath(home), []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}
	manager := model.NewRemoteModelsManagerWithOptions(&model.RemoteModelsManagerOptions{
		Endpoint:                &model.HTTPModelsEndpoint{CatalogURL: catalog.URL + "/codex/models"},
		ExplicitProviderCatalog: true,
	})
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(session.NewStore(t.TempDir())),
		Config:       config.NewConfigService(home),
		Turns:        turn.NewTurnService(),
		Agent:        newRecordingRuntimeAgent("must not run"),
		ThreadStatus: NewThreadStatusManager(),
		Models:       model.NewModelService(manager),
	})
	t.Cleanup(func() { _ = router.Close() })

	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{}))
	if response.Error == nil || response.Error.Message != "No models are available. Set `model` explicitly or check your model catalog configuration." {
		t.Fatalf("thread/start error = %+v", response.Error)
	}
	if strings.Contains(response.Error.Message, "bundled") {
		t.Fatalf("explicit catalog fell back to bundled models: %+v", response.Error)
	}
}
