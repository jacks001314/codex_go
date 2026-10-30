package model

import (
	"encoding/json"
	"testing"
)

// TestBedrockCatalogTierDefinitionsMatchRust mirrors Rust #50472
// (codex-rs/model-provider/src/amazon_bedrock/catalog.rs and
// app-server/tests/suite/v2/bedrock_service_tier_tests.rs): the Mantle GPT-6
// Astra entry and the Runtime `global.`/`us.` variants advertise `ultrafast`
// with the catalog's display metadata, keep the implicit default tier, and
// every other Bedrock entry advertises no tier.
func TestBedrockCatalogTierDefinitionsMatchRust(t *testing.T) {
	mantle := AmazonBedrockCatalogForProviderID(AmazonBedrockProviderID)
	runtimeCatalog := AmazonBedrockCatalogForProviderID(AmazonBedrockRuntimeProviderID)
	cases := []struct {
		name      string
		catalog   ModelsResponse
		slugs     []string
		plainSlug string
	}{
		{
			name:      "mantle",
			catalog:   mantle,
			slugs:     []string{AmazonBedrockGPT6AstraModelID, AmazonBedrockGPT61SolModelID},
			plainSlug: AmazonBedrockGPT6SolModelID,
		},
		{
			name:    "runtime",
			catalog: runtimeCatalog,
			slugs: []string{
				bedrockRuntimeGlobalSlugPrefix + AmazonBedrockGPT6AstraModelID,
				bedrockRuntimeUSSlugPrefix + AmazonBedrockGPT6AstraModelID,
				bedrockRuntimeGlobalSlugPrefix + AmazonBedrockGPT61SolModelID,
				bedrockRuntimeUSSlugPrefix + AmazonBedrockGPT61SolModelID,
			},
			plainSlug: bedrockRuntimeGlobalSlugPrefix + AmazonBedrockGPT6SolModelID,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, slug := range tc.slugs {
				info := modelInfoBySlug(t, tc.catalog, slug)
				definitions := ServiceTierDefinitionsFor(&info)
				if len(definitions) != 1 || definitions[0] != bedrockUltrafastServiceTierDefinition {
					t.Fatalf("%s tier definitions = %#v, want the Rust Ultrafast definition", slug, definitions)
				}
				if info.DefaultServiceTier != "" {
					t.Fatalf("%s default tier = %q, want the implicit default", slug, info.DefaultServiceTier)
				}
				if len(info.AdditionalSpeedTiers) != 0 {
					t.Fatalf("%s additional speed tiers = %#v, want none", slug, info.AdditionalSpeedTiers)
				}
			}
			plain := modelInfoBySlug(t, tc.catalog, tc.plainSlug)
			if len(plain.ServiceTiers) != 0 || len(ServiceTierDefinitionsFor(&plain)) != 0 {
				t.Fatalf("%s advertises tiers %#v/%#v, want none", tc.plainSlug, plain.ServiceTiers, ServiceTierDefinitionsFor(&plain))
			}
		})
	}

	// The bundled OpenAI catalog keeps the tier metadata models.json gives it
	// ("Fast" / "2x speed, increased usage") so model/list and the TUI commands
	// report catalog names instead of raw IDs.
	bundled := modelInfoBySlug(t, BundledModelsResponse(), "gpt-6.1-sol")
	definitions := ServiceTierDefinitionsFor(&bundled)
	if len(definitions) != 1 || definitions[0].ID != "priority" || definitions[0].Name != "Fast" || definitions[0].Description != "2x speed, increased usage" {
		t.Fatalf("bundled definitions = %#v, want the Rust Fast tier", definitions)
	}
}

// TestCustomBedrockCatalogPreservesServiceTierMetadataLikeRust mirrors Rust
// #50472's `bedrock_model_list_preserves_custom_service_tiers`: a custom Bedrock
// catalog owns its speed tiers, service-tier definitions and default tier, while
// only the web-search wire compatibility field is normalized.
func TestCustomBedrockCatalogPreservesServiceTierMetadataLikeRust(t *testing.T) {
	const catalogJSON = `{"models":[{
		"slug":"openai.gpt-6-astra",
		"display_name":"GPT-6-Astra",
		"visibility":"list",
		"supported_in_api":true,
		"additional_speed_tiers":["fast"],
		"service_tiers":[{"id":"ultrafast","name":"Express","description":"My custom Bedrock tier."}],
		"default_service_tier":"ultrafast",
		"web_search_tool_type":"text_and_image"
	}]}`
	var catalog ModelsResponse
	if err := json.Unmarshal([]byte(catalogJSON), &catalog); err != nil {
		t.Fatalf("parse custom catalog: %v", err)
	}
	normalized := normalizeBedrockCatalog(catalog)
	info := normalized.Models[0]
	if info.WebSearchToolType != "text" {
		t.Fatalf("web search tool type = %q, want text", info.WebSearchToolType)
	}
	if len(info.AdditionalSpeedTiers) != 1 || info.AdditionalSpeedTiers[0] != "fast" {
		t.Fatalf("additional speed tiers = %#v, want [fast]", info.AdditionalSpeedTiers)
	}
	if info.DefaultServiceTier != "ultrafast" {
		t.Fatalf("default tier = %q, want ultrafast", info.DefaultServiceTier)
	}
	want := ModelServiceTier{ID: "ultrafast", Name: "Express", Description: "My custom Bedrock tier."}
	definitions := ServiceTierDefinitionsFor(&info)
	if len(definitions) != 1 || definitions[0] != want {
		t.Fatalf("service tier definitions = %#v, want %#v", definitions, want)
	}

	// model/list reports the catalog metadata rather than the raw ID.
	summary := summaryFromModel(info, false)
	if len(summary.ServiceTiers) != 1 || summary.ServiceTiers[0] != want {
		t.Fatalf("model/list service tiers = %#v, want %#v", summary.ServiceTiers, want)
	}
	if summary.DefaultServiceTier == nil || *summary.DefaultServiceTier != "ultrafast" {
		t.Fatalf("model/list default tier = %v, want ultrafast", summary.DefaultServiceTier)
	}

	// The TUI preset keeps the same metadata for its slash commands.
	presets := NewStaticModelsManager(normalized).ListModels(RefreshOffline)
	if len(presets) != 1 {
		t.Fatalf("presets = %#v, want one", presets)
	}
	if len(presets[0].ServiceTierDefinitions) != 1 || presets[0].ServiceTierDefinitions[0] != want || presets[0].DefaultServiceTier != "ultrafast" {
		t.Fatalf("preset tier metadata = %#v default %q", presets[0].ServiceTierDefinitions, presets[0].DefaultServiceTier)
	}
}

func modelInfoBySlug(t *testing.T, catalog ModelsResponse, slug string) ModelInfo {
	t.Helper()
	for _, info := range catalog.Models {
		if info.Slug == slug {
			return info
		}
	}
	t.Fatalf("catalog has no model %q", slug)
	return ModelInfo{}
}
