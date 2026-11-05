package runtimeauth

import (
	"context"
	"net/http"
	"net/url"
	"strings"
)

// Destination classifies an API URL without treating an unsupported OpenAI
// origin as another provider (Rust OpenAiDestination).
type Destination int

const (
	// DestinationOther is a non-OpenAI host, such as a local or custom backend.
	DestinationOther Destination = iota
	// DestinationSupported is a first-party OpenAI API origin that may receive
	// runtime credentials.
	DestinationSupported
	// DestinationUnsupported is an OpenAI-owned origin that is not a valid
	// runtime destination.
	DestinationUnsupported
)

var openAIOwnedDomains = []string{
	"openai.com",
	"openai.org",
	"openai-staging.com",
	"chatgpt.com",
	"chatgpt-staging.com",
}

var supportedOpenAIHosts = map[string]bool{
	"api.openai.com":         true,
	"gov.api.openai.com":     true,
	"us.api.openai.com":      true,
	"eu.api.openai.com":      true,
	"au.api.openai.com":      true,
	"ca.api.openai.com":      true,
	"jp.api.openai.com":      true,
	"in.api.openai.com":      true,
	"sg.api.openai.com":      true,
	"kr.api.openai.com":      true,
	"gb.api.openai.com":      true,
	"ae.api.openai.com":      true,
	"mtls.api.openai.com":    true,
	"mtls-us.api.openai.com": true,
	"mtls-eu.api.openai.com": true,
	"api.openai.org":         true,
	"api.openai-staging.com": true,
}

var exactChatGPTHosts = map[string]bool{
	"chatgpt.com":         true,
	"chat.openai.com":     true,
	"chatgpt-staging.com": true,
}

var chatGPTSubdomainSuffixes = []string{".chatgpt.com", ".chatgpt-staging.com"}

// IsAllowedChatGPTHost reports first-party ChatGPT HTTP hosts.
func IsAllowedChatGPTHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if exactChatGPTHosts[host] {
		return true
	}
	for _, suffix := range chatGPTSubdomainSuffixes {
		if strings.HasSuffix(host, suffix) {
			return true
		}
	}
	return false
}

// ClassifyOpenAIDestination mirrors codex_http_client::classify_openai_api_url.
func ClassifyOpenAIDestination(raw string) Destination {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Hostname() == "" {
		return DestinationOther
	}
	rawHost := strings.ToLower(parsed.Hostname())
	host := strings.TrimRight(rawHost, ".")
	if host == "" {
		return DestinationOther
	}
	// A host that ends in several dots resolves like an unsupported OpenAI
	// origin rather than a different provider (Rust
	// unsupported_openai_origins_are_distinct_from_other_providers).
	if strings.HasSuffix(rawHost, "..") && openAIOwned(host) {
		return DestinationUnsupported
	}
	if !openAIOwned(host) {
		return DestinationOther
	}
	supported := supportedOpenAIHosts[host] || IsAllowedChatGPTHost(host)
	if supported && strings.EqualFold(parsed.Scheme, "https") && parsed.Port() == "" && parsed.User == nil {
		return DestinationSupported
	}
	return DestinationUnsupported
}

// IsOpenAIAPIURL reports whether a destination may receive runtime credentials.
func IsOpenAIAPIURL(raw string) bool {
	return ClassifyOpenAIDestination(raw) == DestinationSupported
}

func openAIOwned(host string) bool {
	for _, domain := range openAIOwnedDomains {
		if host == domain {
			return true
		}
		// Mirror Rust's strip_suffix(domain) check, which accepts a subdomain
		// prefix followed by a dot even when the host still ends in one.
		if strings.HasSuffix(host, domain) {
			if prefix := strings.TrimSuffix(host, domain); strings.HasSuffix(prefix, ".") {
				return true
			}
		}
	}
	return false
}

// ProtectSensitiveHeaders wraps a redirect policy so runtime credentials are
// stripped before any cross-origin hop. Same-origin redirects keep their
// headers, so routing changes inside one backend keep working (Rust
// with_sensitive_header_protection).
func ProtectSensitiveHeaders(next func(*http.Request, []*http.Request) error) func(*http.Request, []*http.Request) error {
	return func(request *http.Request, via []*http.Request) error {
		if request != nil && len(via) > 0 {
			previous := via[len(via)-1]
			if previous != nil && previous.URL != nil && request.URL != nil {
				sameOrigin := strings.EqualFold(previous.URL.Scheme, request.URL.Scheme) &&
					strings.EqualFold(previous.URL.Host, request.URL.Host)
				if !sameOrigin {
					request.Header.Del("Authorization")
					request.Header.Del(AssertionHeader)
				}
			}
		}
		if next != nil {
			return next(request, via)
		}
		return nil
	}
}

// Provider adapts a Manager into per-request headers for HTTP clients.
type Provider struct {
	Manager   *Manager
	Mode      Mode
	Selection Selection
}

// Headers resolves the runtime selection for one request attempt and returns
// its headers.
func (p *Provider) Headers(ctx context.Context) (http.Header, error) {
	if p == nil || p.Manager == nil {
		return nil, ErrUnavailable
	}
	selected, err := p.Manager.AuthWithSelection(ctx, p.Selection)
	if err != nil {
		return nil, err
	}
	return selected.Headers(p.Mode)
}

// HeadersForDestination returns runtime headers only when the destination may
// receive runtime credentials. Other destinations keep their own
// authentication.
func (p *Provider) HeadersForDestination(ctx context.Context, destination string) (http.Header, error) {
	if !IsOpenAIAPIURL(destination) {
		return nil, nil
	}
	return p.Headers(ctx)
}
