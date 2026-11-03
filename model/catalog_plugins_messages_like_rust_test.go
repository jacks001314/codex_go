package model

import (
	"encoding/json"
	"testing"
)

// TestModelMessagesPluginsParseLikeRust pins the catalog surface added by Rust
// #58894: model_messages.plugins.generic and .recommendations decode into the
// typed overrides.
func TestModelMessagesPluginsParseLikeRust(t *testing.T) {
	var messages ModelMessages
	raw := `{"plugins":{"generic":{"intro":"Catalog intro.","usage_instructions":"- Catalog usage."},"recommendations":{"intro":"Catalog recommendations."}}}`
	if err := json.Unmarshal([]byte(raw), &messages); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if messages.Plugins == nil || messages.Plugins.Generic == nil || messages.Plugins.Recommendations == nil {
		t.Fatalf("plugins = %#v", messages.Plugins)
	}
	generic := messages.Plugins.Generic
	if generic.Intro == nil || *generic.Intro != "Catalog intro." ||
		generic.UsageInstructions == nil || *generic.UsageInstructions != "- Catalog usage." {
		t.Fatalf("generic = %#v", generic)
	}
	if messages.Plugins.Recommendations.Intro == nil || *messages.Plugins.Recommendations.Intro != "Catalog recommendations." {
		t.Fatalf("recommendations = %#v", messages.Plugins.Recommendations)
	}
}
