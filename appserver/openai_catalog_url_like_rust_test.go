package appserver

import (
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
	err := router.checkStartupModelProvider()
	if err == nil || !strings.Contains(err.Error(), errModelProviderRequirementsChanged.Error()) {
		t.Fatalf("checkStartupModelProvider() error = %v", err)
	}
}
