package appserver

import (
	"errors"
	"strings"
	"testing"
)

const openAICatalogRequirements = `
model_provider = "openai"
[model_providers.openai]
model_catalog_url = "https://chatgpt.com/backend-api/codex/model-catalog"
`

func TestStartupOpenAICatalogOverrideSatisfiesManagedRequirements(t *testing.T) {
	home := t.TempDir()
	writeManagedProviderRequirements(t, home, openAICatalogRequirements)
	router := newManagedProviderRouter(t, home)
	router.captureStartupModelProviderRoute()
	if !router.startupModelProviderRouteSet {
		t.Fatal("startup provider route was not captured")
	}
	if err := router.checkStartupModelProvider(); err != nil {
		t.Fatalf("checkStartupModelProvider() error = %v", err)
	}
}

func TestChangedOpenAICatalogRequirementRejectsRetainedRoute(t *testing.T) {
	home := t.TempDir()
	writeManagedProviderRequirements(t, home, openAICatalogRequirements)
	router := newManagedProviderRouter(t, home)
	router.captureStartupModelProviderRoute()
	if !router.startupModelProviderRouteSet {
		t.Fatal("startup provider route was not captured")
	}

	writeManagedProviderRequirements(t, home, strings.Replace(
		openAICatalogRequirements,
		"https://chatgpt.com/backend-api/codex/model-catalog",
		"https://chatgpt-staging.com/backend-api/codex/model-catalog",
		1,
	))
	// Rust #57840 replaces the restart-guidance text with a structured policy
	// conflict naming the provider.
	err := router.checkStartupModelProvider()
	var providerErr *ModelProviderError
	if !errors.As(err, &providerErr) || providerErr.Kind != ModelProviderPolicyConflict || providerErr.ModelProvider != "openai" {
		t.Fatalf("checkStartupModelProvider() error = %v, want a policy conflict for openai", err)
	}
}
