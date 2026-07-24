package mcp

import (
	"net/http"
	"os"
	"strings"

	"golang.org/x/net/http/httpguts"
)

var buildVersion = "0.0.0"

func mcpUserAgent() string {
	version := strings.TrimSpace(buildVersion)
	if version == "" {
		version = "0.0.0"
	}
	return "codex-mcp-client/" + version
}

func headerValue(values map[string]string, name string) string {
	for key, value := range values {
		if strings.EqualFold(strings.TrimSpace(key), name) {
			return value
		}
	}
	return ""
}

type mcpHeaderTransport struct {
	base                http.RoundTripper
	configuredUserAgent string
	userAgentEnvVar     string
}

func (t *mcpHeaderTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	cloned := request.Clone(request.Context())
	cloned.Header = request.Header.Clone()
	if cloned.Header.Get("User-Agent") == "" {
		cloned.Header.Set("User-Agent", mcpUserAgent())
	}
	if value := strings.TrimSpace(t.configuredUserAgent); value != "" && httpguts.ValidHeaderFieldValue(value) {
		cloned.Header.Set("User-Agent", value)
	}
	if envVar := strings.TrimSpace(t.userAgentEnvVar); envVar != "" {
		if value := strings.TrimSpace(os.Getenv(envVar)); value != "" && httpguts.ValidHeaderFieldValue(value) {
			cloned.Header.Set("User-Agent", value)
		}
	}
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(cloned)
}

func mcpHTTPClientWithDefaultHeaders(client *http.Client, config *ServerConfig) *http.Client {
	if client == nil {
		client = http.DefaultClient
	}
	cloned := *client
	transport := &mcpHeaderTransport{base: client.Transport}
	if config != nil {
		transport.configuredUserAgent = headerValue(config.HTTPHeaders, "User-Agent")
		transport.userAgentEnvVar = headerValue(config.EnvHTTPHeaders, "User-Agent")
	}
	cloned.Transport = transport
	return &cloned
}

func applyMCPHTTPHeaders(request *http.Request, httpHeaders map[string]string, envHTTPHeaders map[string]string) {
	if request == nil {
		return
	}
	for name, value := range httpHeaders {
		if httpguts.ValidHeaderFieldName(name) && httpguts.ValidHeaderFieldValue(value) {
			request.Header.Set(name, value)
		}
	}
	for name, envVar := range envHTTPHeaders {
		if !httpguts.ValidHeaderFieldName(name) || strings.TrimSpace(envVar) == "" {
			continue
		}
		if value := os.Getenv(strings.TrimSpace(envVar)); strings.TrimSpace(value) != "" && httpguts.ValidHeaderFieldValue(value) {
			request.Header.Set(name, value)
		}
	}
}
