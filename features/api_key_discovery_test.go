package features

import "testing"

// Mirrors Rust #44392: api_key_model_discovery is exposed through app-server
// experimental feature enablement.
func TestAPIKeyModelDiscoveryIsSettableExperimentalFeature(t *testing.T) {
	service := NewFeatureService(nil)
	response, err := service.SetEnablement(&FeatureEnablementSetParams{
		Enablement: map[string]bool{"api_key_model_discovery": true},
	})
	if err != nil {
		t.Fatalf("SetEnablement() error = %v", err)
	}
	if !response.Enablement["api_key_model_discovery"] {
		t.Fatalf("enablement = %#v, want api_key_model_discovery applied", response.Enablement)
	}
}
