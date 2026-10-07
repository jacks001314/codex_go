package plugin

import (
	"net/http"
	"testing"
)

// Rust #49100 (core-plugins/src/plugins_config_input_tests.rs
// `repeated_service_configs_and_early_clones_share_one_lazy_pool`): one lazily
// created pool is shared by repeated service configurations and by an early
// clone of the owner, while a newly constructed owner keeps its own pool.
func TestRemotePluginProviderSharesLazyPoolLikeRust(t *testing.T) {
	service := NewPluginService()
	// Rust clones the config input; the clone shares the same Arc<OnceLock>.
	clone := &PluginService{remoteHTTPPool: service.RemotePluginHTTPPool()}
	if service.RemotePluginHTTPPool().Initialized() {
		t.Fatal("the pool must not be created before the first service configuration")
	}

	first := service.NewRemotePluginProvider("https://chatgpt.com/backend-api", "header.e30.first", "first-account", nil)
	if !service.RemotePluginHTTPPool().Initialized() {
		t.Fatal("the first service configuration must create the shared pool")
	}
	if got := first.httpClient(); got == HTTPDoer(http.DefaultClient) {
		t.Fatal("the provider must use the owner's pooled client, not the process default")
	}
	// Repeated configurations and the early clone reuse the pool.
	for i := 0; i < 220; i++ {
		reused := clone.NewRemotePluginProvider("https://chatgpt.com/backend-api", "header.e30.first", "first-account", nil)
		if reused.httpClient() != first.httpClient() {
			t.Fatalf("configuration %d did not reuse the shared pool", i)
		}
	}
	// A newly constructed service owns a separate pool.
	other := NewPluginService()
	otherProvider := other.NewRemotePluginProvider("https://chatgpt.com/backend-api", "header.e30.first", "first-account", nil)
	if otherProvider.httpClient() == first.httpClient() {
		t.Fatal("a new service must not share another service's pool")
	}
}

// Rust #49100 (`reused_pool_uses_current_endpoint_product_and_authentication`):
// a reused pool still carries the current endpoint, product SKU and
// authentication, and an explicitly supplied doer still wins.
func TestRemotePluginProviderUsesCurrentEndpointAndAuthLikeRust(t *testing.T) {
	pool := NewRemotePluginHTTPPool()
	first := NewHTTPSuggestedPluginProviderWithPool("https://first.example/backend-api", "header.e30.first", "first-account", nil, pool)
	second := NewHTTPSuggestedPluginProviderWithPool("https://second.example/backend-api", "header.e30.second", "second-account", nil, pool)
	if first.httpClient() != second.httpClient() {
		t.Fatal("both service configurations must share the connection pool")
	}
	if first.httpClient() == HTTPDoer(http.DefaultClient) {
		t.Fatal("the shared pool must not be the process default")
	}
	if first.productSKU() != CODEXProductSKU || second.productSKU() != CODEXProductSKU {
		t.Fatalf("default product SKU = %q / %q", first.productSKU(), second.productSKU())
	}
	second.ProductSKU = "updated"
	if second.productSKU() != "updated" {
		t.Fatalf("updated product SKU = %q", second.productSKU())
	}
	if suggestedPluginsURL(first.BaseURL) == suggestedPluginsURL(second.BaseURL) {
		t.Fatal("the endpoint must stay per configuration")
	}
	if first.AccessToken == second.AccessToken || first.AccountID == second.AccountID {
		t.Fatal("authentication must stay per configuration")
	}
	explicit := NewHTTPSuggestedPluginProviderWithPool("https://first.example/backend-api", "t", "", http.DefaultClient, pool)
	if explicit.httpClient() != HTTPDoer(http.DefaultClient) {
		t.Fatal("an explicitly supplied doer must win over the pool")
	}
}

// Rust #49100: a service configuration with neither a doer nor a pool keeps the
// process default, and the bundled SKU is "codex".
func TestRemotePluginProviderFallsBackWithoutAPoolLikeRust(t *testing.T) {
	provider := NewHTTPSuggestedPluginProvider("https://chatgpt.com/backend-api", "t", "", nil)
	if provider.httpClient() != HTTPDoer(http.DefaultClient) {
		t.Fatal("a provider without a pool must fall back to the process default")
	}
	if CODEXProductSKU != "codex" || provider.productSKU() != "codex" {
		t.Fatalf("bundled product SKU = %q / %q", CODEXProductSKU, provider.productSKU())
	}
}
