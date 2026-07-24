package appserver

import (
	"testing"

	"codex_go/auth"
	"codex_go/model"
)

func TestAppImageGenerationStandaloneDisabledForFreePlan(t *testing.T) {
	provider := model.ProviderInfo{Name: model.OpenAIProviderName, RequiresOpenAIAuth: true}
	capabilities := model.ProviderCapabilities{NamespaceTools: true, ImageGeneration: true}
	info := &model.ModelInfo{InputModalities: []string{"text", "image"}}
	features := map[string]bool{"image_generation": true}

	free := string(auth.PlanFree)
	freeSnapshot := auth.FromChatGPTAuthTokens("free-token", "account-free", &free)
	if appImageGenerationStandaloneEnabled(provider, capabilities, info, &freeSnapshot, features) {
		t.Fatal("Free-plan account should not receive standalone image generation")
	}

	pro := string(auth.PlanPro)
	proSnapshot := auth.FromChatGPTAuthTokens("pro-token", "account-pro", &pro)
	if !appImageGenerationStandaloneEnabled(provider, capabilities, info, &proSnapshot, features) {
		t.Fatal("Pro account should retain standalone image generation")
	}
}
