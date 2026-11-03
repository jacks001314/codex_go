package appserver

import (
	"strings"
	"unicode"

	contextfrag "codex_go/context"
	"codex_go/model"
)

const (
	maxPluginWordingBytes = 500
	maxPluginUsageBytes   = 2000
)

// pluginGuidanceForModel resolves the catalog's plugin wording overrides (Rust
// #58894). Each field falls back independently to the bundled wording when it
// is missing, blank, oversized, contains control characters, or embeds the
// fragment markers that delimit the guidance.
func pluginGuidanceForModel(modelInfo *model.ModelInfo) (intro string, usage string, recommendationsIntro string) {
	intro = contextfrag.PluginGuidanceIntroDefault
	usage = contextfrag.PluginGuidanceUsageDefault
	recommendationsIntro = contextfrag.PluginRecommendationsIntroDefault
	if modelInfo == nil || modelInfo.ModelMessages == nil || modelInfo.ModelMessages.Plugins == nil {
		return intro, usage, recommendationsIntro
	}
	plugins := modelInfo.ModelMessages.Plugins
	if generic := plugins.Generic; generic != nil {
		intro = pluginWordingOverride(generic.Intro, intro, maxPluginWordingBytes)
		usage = pluginWordingOverride(generic.UsageInstructions, usage, maxPluginUsageBytes)
	}
	if recommendations := plugins.Recommendations; recommendations != nil {
		recommendationsIntro = pluginWordingOverride(recommendations.Intro, recommendationsIntro, maxPluginWordingBytes)
	}
	return intro, usage, recommendationsIntro
}

func pluginWordingOverride(value *string, bundled string, maxBytes int) string {
	if value == nil {
		return bundled
	}
	text := *value
	if strings.TrimSpace(text) == "" || len(text) > maxBytes {
		return bundled
	}
	for _, char := range text {
		if unicode.IsControl(char) && char != '\n' && char != '\r' && char != '\t' {
			return bundled
		}
	}
	for _, marker := range []string{"<plugins_instructions>", "</plugins_instructions>", "<recommended_plugins>", "</recommended_plugins>"} {
		if strings.Contains(text, marker) {
			return bundled
		}
	}
	return text
}
