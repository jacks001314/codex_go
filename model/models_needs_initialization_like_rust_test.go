package model

import "testing"

// Mirrors Rust #62627 ModelsManager::needs_initialization: an unfetched remote
// catalog needs work, a fetched one does not, a disabled API-key discovery has
// nothing to schedule, and command auth always defers to credential resolution.
func TestRemoteModelsManagerNeedsInitializationLikeRust(t *testing.T) {
	endpoint := &recordingModelsEndpoint{
		responses: []*ModelsEndpointResponse{{Models: []ModelInfo{{Slug: "gpt-5.4"}}}},
	}
	manager := NewRemoteModelsManagerWithOptions(&RemoteModelsManagerOptions{
		Endpoint:             endpoint,
		SupportsAPIKeyModels: true,
		APIKeyAuth:           true,
		Identity:             "identity-1",
	})
	SetAPIKeyModelDiscoveryEnabled(manager, true)
	if !manager.NeedsInitialization() {
		t.Fatal("an unfetched catalog must need initialization")
	}
	manager.RawModelCatalog(RefreshOnline)
	if manager.NeedsInitialization() {
		t.Fatal("a fetched catalog must not need initialization")
	}

	disabled := NewRemoteModelsManagerWithOptions(&RemoteModelsManagerOptions{
		Endpoint:               endpoint,
		SupportsAPIKeyModels:   true,
		APIKeyAuth:             true,
		DisableAPIKeyDiscovery: true,
	})
	if disabled.NeedsInitialization() {
		t.Fatal("disabled API-key discovery must not schedule a fetch")
	}

	commandAuth := NewRemoteModelsManagerWithOptions(&RemoteModelsManagerOptions{
		Endpoint:    endpoint,
		CommandAuth: true,
	})
	if !commandAuth.NeedsInitialization() {
		t.Fatal("command auth must defer to credential resolution")
	}

	if NewStaticModelsManager(BundledModelsResponse()).NeedsInitialization() {
		t.Fatal("a static catalog must never need initialization")
	}
}

// Mirrors Rust #62627: a catalog fetched for a different identity is still
// uninitialized for the current credentials.
func TestRemoteModelsManagerNeedsInitializationOnIdentityChangeLikeRust(t *testing.T) {
	endpoint := &recordingModelsEndpoint{
		responses: []*ModelsEndpointResponse{{Models: []ModelInfo{{Slug: "gpt-5.4"}}}},
	}
	manager := NewRemoteModelsManagerWithOptions(&RemoteModelsManagerOptions{
		Endpoint:             endpoint,
		SupportsAPIKeyModels: true,
		APIKeyAuth:           true,
		Identity:             "identity-1",
	})
	SetAPIKeyModelDiscoveryEnabled(manager, true)
	manager.RawModelCatalog(RefreshOnline)
	if manager.NeedsInitialization() {
		t.Fatal("a catalog for the current identity must be initialized")
	}
	manager.mu.Lock()
	manager.identity = "identity-2"
	manager.mu.Unlock()
	if !manager.NeedsInitialization() {
		t.Fatal("a catalog belonging to another identity must need initialization")
	}
}
