package network

import (
	"net/http"
)

// BaseTransport returns the concrete *http.Transport behind a round tripper
// built by NewHTTPClient. Callers that mutate transport-wide settings (custom
// dialer, TLS roots) must use ReplaceBaseTransport so the proxy-auth wrapper is
// preserved. It returns nil for an unknown custom round tripper.
func BaseTransport(roundTripper http.RoundTripper) *http.Transport {
	switch transport := roundTripper.(type) {
	case nil:
		return http.DefaultTransport.(*http.Transport)
	case *http.Transport:
		return transport
	case *proxyAuthTransport:
		return transport.base
	case *PolicyRoundTripper:
		return BaseTransport(transport.Next)
	default:
		return nil
	}
}

// ReplaceBaseTransport rebuilds a round tripper after its underlying
// *http.Transport changed. It preserves an enclosing application-policy
// wrapper and re-creates the proxy-auth wrapper when one was present.
func ReplaceBaseTransport(roundTripper http.RoundTripper, base *http.Transport) http.RoundTripper {
	switch transport := roundTripper.(type) {
	case nil, *http.Transport:
		return base
	case *proxyAuthTransport:
		return newProxyAuthTransport(base)
	case *PolicyRoundTripper:
		clone := *transport
		clone.Next = ReplaceBaseTransport(transport.Next, base)
		return &clone
	default:
		return roundTripper
	}
}

// WithoutSystemProxy returns a round tripper that never consults system proxy
// settings. It unwraps the proxy-auth transport so an authenticated CONNECT is
// not attempted for a direct-only request, and reports false for an unknown
// custom round tripper that cannot be made direct safely.
func WithoutSystemProxy(roundTripper http.RoundTripper) (http.RoundTripper, bool) {
	switch transport := roundTripper.(type) {
	case nil:
		direct := http.DefaultTransport.(*http.Transport).Clone()
		direct.Proxy = nil
		return direct, true
	case *http.Transport:
		direct := transport.Clone()
		direct.Proxy = nil
		return direct, true
	case *proxyAuthTransport:
		direct := transport.base.Clone()
		direct.Proxy = nil
		return direct, true
	case *PolicyRoundTripper:
		next, ok := WithoutSystemProxy(transport.Next)
		if !ok {
			return nil, false
		}
		clone := *transport
		clone.Next = next
		return &clone, true
	default:
		return nil, false
	}
}
