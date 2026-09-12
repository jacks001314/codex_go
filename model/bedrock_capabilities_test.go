package model

import "testing"

// TestAmazonBedrockCapabilitiesLikeRust mirrors Rust
// AmazonBedrockModelProvider::capabilities: namespace tools are available and
// image generation is not on either endpoint, while web search is available on
// the Mantle endpoint only.
func TestAmazonBedrockCapabilitiesLikeRust(t *testing.T) {
	mantle := &AmazonBedrockProvider{info: CreateAmazonBedrockProvider(nil)}
	if got := mantle.Capabilities(); got != (ProviderCapabilities{NamespaceTools: true, ImageGeneration: false, WebSearch: true}) {
		t.Fatalf("mantle capabilities = %+v", got)
	}
	runtimeProvider := &AmazonBedrockProvider{info: CreateAmazonBedrockRuntimeProvider(nil)}
	if got := runtimeProvider.Capabilities(); got != (ProviderCapabilities{NamespaceTools: true, ImageGeneration: false, WebSearch: false}) {
		t.Fatalf("runtime capabilities = %+v", got)
	}
}
