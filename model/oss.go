package model

import (
	"context"
	"fmt"
	"net/http"
)

func DefaultModelForProvider(providerID string) (string, bool) {
	switch providerID {
	case LMStudioOSSProviderID:
		return LMStudioDefaultOSSModel, true
	case OllamaOSSProviderID:
		return OllamaDefaultOSSModel, true
	default:
		return "", false
	}
}

func IsOSSProviderID(providerID string) bool {
	switch providerID {
	case LMStudioOSSProviderID, OllamaOSSProviderID:
		return true
	default:
		return false
	}
}

func OSSModelCatalog(providerID string) ModelsResponse {
	model, ok := DefaultModelForProvider(providerID)
	if !ok {
		return ModelsResponse{}
	}
	displayName := model
	switch providerID {
	case LMStudioOSSProviderID:
		displayName = "LM Studio " + model
	case OllamaOSSProviderID:
		displayName = "Ollama " + model
	}
	return ModelsResponse{Models: []ModelInfo{{
		Slug:                          model,
		DisplayName:                   displayName,
		Visibility:                    VisibilityVisible,
		SupportedInAPI:                true,
		Priority:                      0,
		BaseInstructions:              BaseInstructions,
		TruncationPolicy:              TruncationPolicy{Mode: TruncationModeBytes, Limit: 10000},
		ContextWindow:                 128000,
		MaxContextWindow:              128000,
		EffectiveContextWindowPercent: 95,
		InputModalities:               []string{"text"},
	}}}
}

type ReadyConfig struct {
	LMStudioClient *LMStudioClient
	OllamaClient   *OllamaClient
	HTTPClient     *http.Client
	Model          string
}

func EnsureProviderReady(ctx context.Context, providerID string, config ReadyConfig) error {
	switch providerID {
	case LMStudioOSSProviderID:
		if config.LMStudioClient == nil {
			config.LMStudioClient = NewLMStudioClient("")
		}
		return wrapSetupError(EnsureLMStudioOSSReady(ctx, config.LMStudioClient, config.Model))
	case OllamaOSSProviderID:
		if config.OllamaClient == nil {
			config.OllamaClient = NewOllamaClientWithHTTPClient("", config.HTTPClient)
		}
		if err := EnsureOllamaResponsesSupported(ctx, config.OllamaClient); err != nil {
			return err
		}
		return wrapSetupError(EnsureOllamaOSSReady(ctx, config.OllamaClient, config.Model))
	default:
		return nil
	}
}

func wrapSetupError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("OSS setup failed: %w", err)
}
