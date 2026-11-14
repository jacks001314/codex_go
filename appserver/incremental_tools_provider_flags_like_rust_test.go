package appserver

import (
	"testing"

	"codex_go/config"
	"codex_go/model"
)

// TestIncrementalToolProviderFlagsLikeRust mirrors Rust #60848/#61426
// (`scenarios_incremental_tools`): Vertex AI publishes incremental tool
// declarations by default behind `incremental_tools_legacy_gcp`, and both
// bundled Amazon Bedrock providers now publish them through
// `incremental_tools_legacy`, which is enabled by default and can be disabled
// explicitly. The flags are independent.
func TestIncrementalToolProviderFlagsLikeRust(t *testing.T) {
	cases := []struct {
		name       string
		providerID string
		features   map[string]any
		want       bool
	}{
		{name: "responses_lite", providerID: model.OpenAIProviderID, features: map[string]any{"incremental_tools": true}, want: true},
		{name: "mantle_default", providerID: model.AmazonBedrockProviderID, want: true},
		{name: "mantle_opt_out", providerID: model.AmazonBedrockProviderID, features: map[string]any{"incremental_tools_legacy": false}, want: false},
		{name: "runtime_default", providerID: model.AmazonBedrockRuntimeProviderID, want: true},
		{name: "runtime_opt_out", providerID: model.AmazonBedrockRuntimeProviderID, features: map[string]any{"incremental_tools_legacy": false}, want: false},
		{name: "gcp_default", providerID: model.GCPProviderID, want: true},
		{name: "gcp_opt_out", providerID: model.GCPProviderID, features: map[string]any{"incremental_tools_legacy_gcp": false, "incremental_tools_legacy": true}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{Values: map[string]any{}}
			if tc.features != nil {
				cfg.Values["features"] = tc.features
			}
			info := &model.ModelInfo{UseResponsesLite: tc.name == "responses_lite"}
			if got := incrementalToolsEnabledForTurn(cfg, info, tc.providerID); got != tc.want {
				t.Fatalf("incrementalToolsEnabledForTurn(%s) = %v, want %v", tc.providerID, got, tc.want)
			}
		})
	}
}
