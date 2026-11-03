package appserver

import (
	"strings"
	"testing"

	contextfrag "codex_go/context"
	"codex_go/model"
)

func pluginMessagesModel(genericIntro, genericUsage, recommendationsIntro *string) *model.ModelInfo {
	return &model.ModelInfo{ModelMessages: &model.ModelMessages{Plugins: &model.PluginMessages{
		Generic:         &model.GenericPluginMessages{Intro: genericIntro, UsageInstructions: genericUsage},
		Recommendations: &model.PluginRecommendationMessages{Intro: recommendationsIntro},
	}}}
}

func stringPointerForPluginTest(value string) *string { return &value }

// TestPluginGuidanceOverridesLikeRust covers Rust #58894: valid catalog wording
// replaces the bundled text, while invalid, blank or oversized fields fall back
// independently.
func TestPluginGuidanceOverridesLikeRust(t *testing.T) {
	intro, usage, recommendations := pluginGuidanceForModel(nil)
	if intro != contextfrag.PluginGuidanceIntroDefault || usage != contextfrag.PluginGuidanceUsageDefault ||
		recommendations != contextfrag.PluginRecommendationsIntroDefault {
		t.Fatalf("default guidance = %q / %q / %q", intro, usage, recommendations)
	}

	intro, usage, recommendations = pluginGuidanceForModel(pluginMessagesModel(
		stringPointerForPluginTest("Custom intro."),
		stringPointerForPluginTest("- Custom usage."),
		stringPointerForPluginTest("Custom recommendations."),
	))
	if intro != "Custom intro." || usage != "- Custom usage." || recommendations != "Custom recommendations." {
		t.Fatalf("catalog guidance = %q / %q / %q", intro, usage, recommendations)
	}

	oversizedUsage := strings.Repeat("x", maxPluginUsageBytes+1)
	intro, usage, recommendations = pluginGuidanceForModel(pluginMessagesModel(
		stringPointerForPluginTest("  "),
		stringPointerForPluginTest(oversizedUsage),
		stringPointerForPluginTest("<recommended_plugins>bad</recommended_plugins>"),
	))
	if intro != contextfrag.PluginGuidanceIntroDefault || usage != contextfrag.PluginGuidanceUsageDefault ||
		recommendations != contextfrag.PluginRecommendationsIntroDefault {
		t.Fatalf("invalid guidance did not fall back: %q / %q / %q", intro, usage, recommendations)
	}

	controlUsage := "usage\x00text"
	_, usage, _ = pluginGuidanceForModel(pluginMessagesModel(nil, &controlUsage, nil))
	if usage != contextfrag.PluginGuidanceUsageDefault {
		t.Fatalf("control-character usage = %q, want the bundled text", usage)
	}
}

// TestAvailablePluginsInstructionsRenderCatalogGuidance checks that the
// resolved wording reaches the rendered fragment while the bundled body stays
// intact around it.
func TestAvailablePluginsInstructionsRenderCatalogGuidance(t *testing.T) {
	rendered := contextfrag.Render(contextfrag.NewAvailablePluginsInstructionsWithGuidance(
		[]contextfrag.PluginSummary{{ConfigName: "docs", DisplayName: "Docs"}},
		"Catalog intro.",
		"- Catalog usage.",
	))
	if rendered == nil {
		t.Fatal("fragment was not rendered")
	}
	if !strings.Contains(rendered.Content, "Catalog intro.") || !strings.Contains(rendered.Content, "- Catalog usage.") {
		t.Fatalf("catalog guidance missing from fragment:\n%s", rendered.Content)
	}
	if strings.Contains(rendered.Content, contextfrag.PluginGuidanceIntroDefault) {
		t.Fatalf("bundled intro leaked into the override:\n%s", rendered.Content)
	}

	recommended := contextfrag.Render(contextfrag.NewRecommendedPluginsInstructionsWithIntro(
		[]contextfrag.RecommendedPlugin{{ID: "docs@market", Name: "Docs"}},
		"Catalog recommendations.",
	))
	if recommended == nil || !strings.Contains(recommended.Content, "Catalog recommendations.") {
		t.Fatalf("recommendation override missing: %#v", recommended)
	}
}
