package model

import "testing"

func TestDefaultModelForProvider(t *testing.T) {
	if got, ok := DefaultModelForProvider(LMStudioOSSProviderID); !ok || got != LMStudioDefaultOSSModel {
		t.Fatalf("lmstudio default = %q %v", got, ok)
	}
	if got, ok := DefaultModelForProvider(OllamaOSSProviderID); !ok || got != OllamaDefaultOSSModel {
		t.Fatalf("ollama default = %q %v", got, ok)
	}
	if _, ok := DefaultModelForProvider("unknown"); ok {
		t.Fatalf("unknown provider should not have default")
	}
}

func TestOSSRuntimeProviderUsesStaticCatalog(t *testing.T) {
	provider := CreateRuntimeProviderForID(OllamaOSSProviderID, CreateOSSProvider(DefaultOllamaPort, WireAPIResponses), nil)
	manager := provider.ModelsManager(nil)
	if _, ok := manager.(*StaticModelsManager); !ok {
		t.Fatalf("manager type = %T, want *StaticModelsManager", manager)
	}
	if got := manager.GetDefaultModel("", true, RefreshOffline); got != OllamaDefaultOSSModel {
		t.Fatalf("default model = %q", got)
	}
}
