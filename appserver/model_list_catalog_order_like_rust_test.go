package appserver

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"codex_go/config"
	"codex_go/model"
)

// Rust #60525: a first-party provider catalog supplies model/list rows in
// catalog display order, while the recommended model stays the default.
func TestModelListUsesFirstPartyCatalogDisplayOrderLikeRust(t *testing.T) {
	home := t.TempDir()
	writeFirstPartyCatalogConfig(t, home)

	rows := func(origin model.ModelOrigin) []string {
		catalog := model.ModelsResponse{Models: []model.ModelInfo{
			{Slug: "z-model", Priority: 3, Visibility: model.VisibilityList, SupportedInAPI: true},
			{Slug: "a-model", Priority: 2, Visibility: model.VisibilityList, SupportedInAPI: true},
			{Slug: "recommended", Priority: 1, Visibility: model.VisibilityList, SupportedInAPI: true},
		}}
		service := model.NewModelService(model.NewStaticModelsManagerWithOrigin(catalog, origin))
		router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(home), Models: service})
		t.Cleanup(func() { _ = router.Close() })
		response := router.Handle(requestWithParams(t, IntID(1), MethodModelList, model.ModelListParams{}))
		if response.Error != nil {
			t.Fatalf("model/list error: %+v", response.Error)
		}
		listed := response.Result.(*model.ModelListResponse).Data
		out := make([]string, 0, len(listed))
		for _, row := range listed {
			out = append(out, fmt.Sprintf("%s:%t", row.ID, row.IsDefault))
		}
		return out
	}

	got := rows(model.ModelOriginProvider)
	want := []string{"z-model:false", "a-model:false", "recommended:true"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("first-party model/list rows = %v, want catalog display order %v", got, want)
	}

	// Bundled entries keep the manager's priority order even when the provider
	// points at a first-party catalog URL.
	got = rows(model.ModelOriginBundled)
	want = []string{"recommended:true", "a-model:false", "z-model:false"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("bundled model/list rows = %v, want priority order %v", got, want)
	}
}

// Rust #60525 keeps priority ordering for custom providers: a reversed catalog
// response must not reorder the picker.
func TestModelListKeepsPriorityOrderForCustomProviderLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(`model_provider = "custom"
[model_providers.custom]
name = "Custom"
base_url = "https://example.com/v1"
`), 0o600); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}
	catalog := model.ModelsResponse{Models: []model.ModelInfo{
		{Slug: "z-model", Priority: 3, Visibility: model.VisibilityList, SupportedInAPI: true},
		{Slug: "a-model", Priority: 2, Visibility: model.VisibilityList, SupportedInAPI: true},
		{Slug: "recommended", Priority: 1, Visibility: model.VisibilityList, SupportedInAPI: true},
	}}
	service := model.NewModelService(model.NewStaticModelsManagerWithOrigin(catalog, model.ModelOriginProvider))
	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(home), Models: service})
	t.Cleanup(func() { _ = router.Close() })
	response := router.Handle(requestWithParams(t, IntID(1), MethodModelList, model.ModelListParams{}))
	if response.Error != nil {
		t.Fatalf("model/list error: %+v", response.Error)
	}
	listed := response.Result.(*model.ModelListResponse).Data
	got := make([]string, 0, len(listed))
	for _, row := range listed {
		got = append(got, fmt.Sprintf("%s:%t", row.ID, row.IsDefault))
	}
	want := []string{"recommended:true", "a-model:false", "z-model:false"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("custom provider model/list rows = %v, want priority order %v", got, want)
	}
}

func writeFirstPartyCatalogConfig(t *testing.T, home string) {
	t.Helper()
	body := `model_provider = "openai"
[model_providers.openai]
model_catalog_url = "https://chatgpt.com/backend-api/codex/model-catalog"
`
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}
}
