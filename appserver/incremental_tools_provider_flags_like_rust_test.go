package appserver

import (
	"testing"

	"codex_go/config"
	"codex_go/model"
)

// TestIncrementalToolProviderFlagsLikeRust mirrors Rust #60848
// (`scenarios_incremental_tools`): Vertex AI publishes incremental tool
// declarations by default behind `incremental_tools_legacy_gcp`, while the two
// bundled Amazon Bedrock providers stay off until `incremental_tools_legacy`
// is enabled. The flags are independent.
func TestIncrementalToolProviderFlagsLikeRust(t *testing.T) {
	cases := []struct {
		name       string
		providerID string
		features   map[string]any
		want       bool
	}{
		{name: "responses_lite", providerID: model.OpenAIProviderID, features: map[string]any{"incremental_tools": true}, want: true},
		{name: "mantle_default", providerID: model.AmazonBedrockProviderID, want: false},
		{name: "mantle_enabled", providerID: model.AmazonBedrockProviderID, features: map[string]any{"incremental_tools_legacy": true}, want: true},
		{name: "runtime_default", providerID: model.AmazonBedrockRuntimeProviderID, want: false},
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
