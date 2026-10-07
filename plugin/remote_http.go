package plugin

import (
	"net/http"
	"sync"
)

// CODEXProductSKU is Rust remote::CODEX_PRODUCT_SKU (#49100): the product SKU
// stamped on remote plugin requests when a service configuration does not
// supply one.
const CODEXProductSKU = "codex"

// RemotePluginHTTPPool is a lazily created HTTP connection pool shared by every
// remote plugin service configuration built from one owner (Rust #49100,
// core-plugins/src/manager.rs `PluginsConfigInput::remote_http_clients`). The
// pool is created on first use (Rust `OnceLock::get_or_init`) and reused for
// the lifetime of its owner, so repeated service configurations reuse
// connections; independently constructed owners keep separate pools.
//
// Go has no route-aware client selector, so the pooled client wraps a clone of
// http.DefaultTransport rather than Rust's RouteAwareClientPool.
type RemotePluginHTTPPool struct {
	mu     sync.Mutex
	client *http.Client
}

// NewRemotePluginHTTPPool creates an uninitialized pool (Rust
// `Arc::new(OnceLock::new())`).
func NewRemotePluginHTTPPool() *RemotePluginHTTPPool {
	return &RemotePluginHTTPPool{}
}

// Client returns the shared pooled client, creating it on first use. A nil pool
// falls back to the process default, matching the disabled variant.
func (p *RemotePluginHTTPPool) Client() *http.Client {
	if p == nil {
		return http.DefaultClient
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.client == nil {
		p.client = &http.Client{Transport: http.DefaultTransport.(*http.Transport).Clone()}
	}
	return p.client
}

// Initialized reports whether the pool has been created, without forcing
// creation (Rust `OnceLock::get`).
func (p *RemotePluginHTTPPool) Initialized() bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.client != nil
}
