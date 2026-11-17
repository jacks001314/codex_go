package network

import (
	"context"
	"net/http"
	"net/url"
	"strings"
)

const (
	// ToolCallAttributionHeader is the executor-internal header an upstream proxy
	// consumes and strips (#61860).
	ToolCallAttributionHeader = "x-openai-internal-tool-call-id"
	// ToolCallAttributionProxyEnv names the upstream proxy allowed to receive the
	// attribution header. Unset keeps attribution disabled.
	ToolCallAttributionProxyEnv = "CODEX_TOOL_CALL_ATTRIBUTION_PROXY"
)

// toolCallAttribution mirrors Rust `network-proxy/src/upstream.rs`
// ToolCallAttribution (#61860): one endpoint plus the controller's tool-call id.
type toolCallAttribution struct {
	endpoint ProxyAddress
	callID   string
}

func newToolCallAttribution(env map[string]string, toolCallID string) *toolCallAttribution {
	callID := strings.TrimSpace(toolCallID)
	if callID == "" {
		return nil
	}
	// Rust keeps attribution only when `HeaderValue::from_str` accepts the id;
	// a control character would make the header unrepresentable, so the whole
	// feature stays disabled.
	if !validToolCallAttributionValue(callID) {
		return nil
	}
	endpoint := ReadProxyAddressEnv(env, []string{ToolCallAttributionProxyEnv})
	if endpoint == nil || strings.TrimSpace(endpoint.Host) == "" {
		return nil
	}
	return &toolCallAttribution{endpoint: *endpoint, callID: callID}
}

// validToolCallAttributionValue mirrors the visible-ASCII/obs-text check behind
// Rust `HeaderValue::from_str`: control characters and DEL are rejected.
func validToolCallAttributionValue(value string) bool {
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// headerValueFor reports the attribution value only for the designated
// upstream. Direct destinations and every other upstream get no attribution.
func (a *toolCallAttribution) headerValueFor(upstream *ProxyAddress) string {
	if a == nil || upstream == nil {
		return ""
	}
	if !sameProxyEndpoint(a.endpoint, *upstream) {
		return ""
	}
	return a.callID
}

// proxyEndpointFromURL converts a transport proxy URL into the same shape the
// environment parser produces so both sides compare with one rule.
func proxyEndpointFromURL(value *url.URL) *ProxyAddress {
	if value == nil {
		return nil
	}
	host := strings.TrimSpace(value.Hostname())
	if host == "" {
		return nil
	}
	return &ProxyAddress{Host: host, Port: value.Port(), Scheme: strings.ToLower(value.Scheme)}
}

func sameProxyEndpoint(left, right ProxyAddress) bool {
	leftScheme := strings.ToLower(strings.TrimSpace(left.Scheme))
	rightScheme := strings.ToLower(strings.TrimSpace(right.Scheme))
	if leftScheme == "" {
		leftScheme = "http"
	}
	if rightScheme == "" {
		rightScheme = "http"
	}
	if leftScheme != rightScheme {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(left.Host), strings.TrimSpace(right.Host)) {
		return false
	}
	return proxyEndpointPort(left.Port) == proxyEndpointPort(right.Port)
}

func proxyEndpointPort(port string) string {
	port = strings.TrimSpace(port)
	if port == "" {
		return "80"
	}
	return port
}

// toolCallAttribution reads the executor's launch-local attribution. Rust reads
// `state.process_log_metadata.tool_call_id`, which is fixed for the proxy
// instance rather than per request.
func (s *ProxyServer) toolCallAttribution() *toolCallAttribution {
	if s == nil {
		return nil
	}
	return newToolCallAttribution(s.baseEnv, s.auditMetadata.ProcessLog.ToolCallID)
}

// upstreamAttributionHeaderForRequest mirrors the plaintext-HTTP branch of Rust
// `UpstreamClient::call`: attribution is attached only when this request is
// routed through the designated upstream.
func (s *ProxyServer) upstreamAttributionHeaderForRequest(request *http.Request) string {
	attribution := s.toolCallAttribution()
	if attribution == nil || request == nil {
		return ""
	}
	policy := s.runtimePolicy()
	if policy == nil || !policy.settings.AllowUpstreamProxy || !s.requestMayUseUpstreamProxy(request) {
		return ""
	}
	secure := request.URL != nil && strings.EqualFold(request.URL.Scheme, "https")
	config := ProxyUpstreamConfigFromEnv(s.baseEnv)
	return attribution.headerValueFor((&config).ProxyForProtocol(secure))
}

// proxyConnectAttributionHeader mirrors Rust's outer HTTPS CONNECT branch: the
// header rides on the CONNECT this proxy sends to its upstream.
func (s *ProxyServer) proxyConnectAttributionHeader(upstream *url.URL) string {
	return s.toolCallAttribution().headerValueFor(proxyEndpointFromURL(upstream))
}

// upstreamProxyConnectHeader installs the attribution header on the CONNECT the
// HTTP transport opens for an MITM'd HTTPS request. The transport only calls
// this when a proxy is actually selected, so direct destinations stay clean.
func (s *ProxyServer) upstreamProxyConnectHeader(_ context.Context, proxyURL *url.URL, _ string) (http.Header, error) {
	if value := s.proxyConnectAttributionHeader(proxyURL); value != "" {
		return http.Header{ToolCallAttributionHeader: []string{value}}, nil
	}
	return nil, nil
}
