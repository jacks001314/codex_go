package appserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/config"
	"codex_go/model"
	"codex_go/session"
)

// catalogHome writes a config home whose model catalog ships one custom model
// and whose context-window override must apply to the resolved entry.
func catalogHome(t *testing.T, catalog string, extra string) string {
	t.Helper()
	home := t.TempDir()
	path := filepath.Join(home, "catalog.json")
	if err := os.WriteFile(path, []byte(catalog), 0o600); err != nil {
		t.Fatal(err)
	}
	body := "model_catalog_json = \"" + strings.ReplaceAll(path, `\`, `\\`) + "\"\n" + extra
	if err := os.WriteFile(config.ConfigPath(home), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return home
}

const providerDefaultCatalog = `{"models":[{"slug":"provider-default","display_name":"Provider Default","priority":0,"visibility":"list","supported_in_api":true,"context_window":200000,"default_reasoning_level":"medium","supported_reasoning_levels":["low","medium","high"]}]}`

// TestThreadStartModelUsesCreationCatalogLikeRust covers Rust #59987: the start
// model and the thread catalog entry come from the thread creation options, and
// thread/read reports the same entry.
func TestThreadStartModelUsesCreationCatalogLikeRust(t *testing.T) {
	home := catalogHome(t, providerDefaultCatalog, "model_context_window = 32000\n")
	store := session.NewStore(filepath.Join(home, "sessions"))
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
		ThreadStatus: NewThreadStatusManager(),
	})
	start := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: t.TempDir()}))
	if start.Error != nil {
		t.Fatalf("thread start error: %+v", start.Error)
	}
	result := start.Result.(*ThreadStartResponse)
	if result.Model != "provider-default" {
		t.Fatalf("start model = %q, want the configured catalog default", result.Model)
	}
	info := result.Thread.SelectedModelInfo
	if info == nil || info.Slug != "provider-default" || info.DisplayName != "Provider Default" {
		t.Fatalf("start selected model info = %#v", info)
	}
	read := router.Handle(requestWithParams(t, IntID(2), MethodThreadRead, ThreadReadParams{ThreadID: result.Thread.ID}))
	if read.Error != nil {
		t.Fatalf("thread read error: %+v", read.Error)
	}
	readInfo := read.Result.(*ThreadReadResponse).Thread.SelectedModelInfo
	if readInfo == nil || *readInfo != *info {
		t.Fatalf("thread/read info = %#v, want the start resolution %#v", readInfo, info)
	}

	// The resolved catalog entry also carries the configured context window
	// override (Rust compares selected.context_window with the startup value).
	cfg, err := config.LoadWithOptions(home, &config.LoadOptions{CWD: t.TempDir()})
	if err != nil {
		t.Fatalf("LoadWithOptions() error = %v", err)
	}
	modelID, catalog := router.resolveThreadStartModel(cfg, &ThreadStartParams{}, "", "", model.OpenAIProviderID)
	if modelID != "provider-default" || catalog == nil || catalog.Model.ContextWindow != 32_000 {
		t.Fatalf("resolved = %q, %#v, want the catalog model with the 32,000-token override", modelID, catalog)
	}
}

// TestThreadStartModelHonorsProviderFallbackLikeRust covers Rust #59987: the
// provider fallback setting decides whether an unavailable slug resolves to the
// catalog default.
func TestThreadStartModelHonorsProviderFallbackLikeRust(t *testing.T) {
	home := catalogHome(t, providerDefaultCatalog, "")
	store := session.NewStore(filepath.Join(home, "sessions"))
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
		ThreadStatus: NewThreadStatusManager(),
	})
	for index, tc := range []struct {
		allowFallback bool
		want          string
	}{
		{allowFallback: false, want: "unavailable-model"},
		{allowFallback: true, want: "provider-default"},
	} {
		start := router.Handle(requestWithParams(t, IntID(int64(index+1)), MethodThreadStart, ThreadStartParams{
			CWD:                        t.TempDir(),
			Model:                      "unavailable-model",
			AllowProviderModelFallback: tc.allowFallback,
		}))
		if start.Error != nil {
			t.Fatalf("thread start (fallback=%t) error: %+v", tc.allowFallback, start.Error)
		}
		result := start.Result.(*ThreadStartResponse)
		if result.Model != tc.want {
			t.Fatalf("fallback=%t model = %q, want %q", tc.allowFallback, result.Model, tc.want)
		}
		if info := result.Thread.SelectedModelInfo; info == nil || info.Slug != tc.want {
			t.Fatalf("fallback=%t info = %#v", tc.allowFallback, info)
		}
	}
}

// TestThreadStartBedrockModelCarriesCatalogEntryLikeRust covers Rust #59987 for
// the Bedrock provider: the fallback slug and its catalog entry come from the
// provider's own static catalog, so the start response names the model and
// thread/read reports the same entry.
func TestThreadStartBedrockModelCarriesCatalogEntryLikeRust(t *testing.T) {
	home := t.TempDir()
	body := "model_provider = \"amazon-bedrock\"\nmodel = \"gpt-5.4-mini\"\n"
	if err := os.WriteFile(config.ConfigPath(home), []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile config returned error: %v", err)
	}
	store := session.NewStore(filepath.Join(home, "sessions"))
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
		ThreadStatus: NewThreadStatusManager(),
	})
	start := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:                        t.TempDir(),
		AllowProviderModelFallback: true,
	}))
	if start.Error != nil {
		t.Fatalf("thread start error: %+v", start.Error)
	}
	result := start.Result.(*ThreadStartResponse)
	if result.Model != model.AmazonBedrockGPT62SolModelID {
		t.Fatalf("start model = %q, want the Bedrock catalog default", result.Model)
	}
	info := result.Thread.SelectedModelInfo
	if info == nil || info.Slug != model.AmazonBedrockGPT62SolModelID || info.DisplayName != "GPT-6.2 Sol" {
		t.Fatalf("start selected model info = %#v", info)
	}
	read := router.Handle(requestWithParams(t, IntID(2), MethodThreadRead, ThreadReadParams{ThreadID: result.Thread.ID}))
	if read.Error != nil {
		t.Fatalf("thread read error: %+v", read.Error)
	}
	readInfo := read.Result.(*ThreadReadResponse).Thread.SelectedModelInfo
	if readInfo == nil || *readInfo != *info {
		t.Fatalf("thread/read info = %#v, want the start resolution %#v", readInfo, info)
	}
}
