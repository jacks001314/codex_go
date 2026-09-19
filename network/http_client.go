package network

import (
	"net/http"
	"time"
)

func NewHTTPClient(respectSystemProxy bool, timeout time.Duration) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if !respectSystemProxy {
		transport.Proxy = nil
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
	}
}

// NewSystemProxyHTTPClient builds a client that honors the host system proxy
// settings. It is the fallback route for bootstrap requests whose primary client
// deliberately ignores the system proxy (Rust
// OutboundProxyPolicy::RespectSystemProxy, #46562).
func NewSystemProxyHTTPClient(timeout time.Duration) *http.Client {
	return NewHTTPClient(true, timeout)
}
