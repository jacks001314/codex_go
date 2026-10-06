package model

import "testing"

// TestAmazonBedrockCapabilitiesLikeRust mirrors Rust
// AmazonBedrockModelProvider::capabilities: image generation is not available
// on either endpoint, web search is available on the Mantle endpoint only,
// live external web access is disallowed, and remote compaction v2 is
// supported.
func TestAmazonBedrockCapabilitiesLikeRust(t *testing.T) {
	mantle := &AmazonBedrockProvider{info: CreateAmazonBedrockProvider(nil)}
	if got := mantle.Capabilities(); got != (ProviderCapabilities{ImageGeneration: false, WebSearch: true, ExternalWebAccess: false, RemoteCompaction: RemoteCompactionV2}) {
		t.Fatalf("mantle capabilities = %+v", got)
	}
	runtimeProvider := &AmazonBedrockProvider{info: CreateAmazonBedrockRuntimeProvider(nil)}
	if got := runtimeProvider.Capabilities(); got != (ProviderCapabilities{ImageGeneration: false, WebSearch: false, ExternalWebAccess: false, RemoteCompaction: RemoteCompactionV2}) {
		t.Fatalf("runtime capabilities = %+v", got)
	}
}
