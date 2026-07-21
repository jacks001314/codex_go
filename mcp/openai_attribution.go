package mcp

import (
	"net/http"
	"net/url"
	"strings"
)

const (
	openAIDeveloperDocsMCPURL      = "https://developers.openai.com/mcp"
	openAIDeveloperDocsMCPCodexURL = "https://developers.openai.com/mcp?source=codex"
)

// OpenAI Docs source attribution: when the MCP server URL matches the OpenAI
// developer docs MCP endpoint, all HTTP requests to that server are rewritten
// to include ?source=codex so the server can attribute usage to Codex.
// This mirrors Rust's maybe_with_openai_docs_source_attribution.

// MaybeWithOpenAIDocsSourceAttribution returns an HTTP request modifier that
// rewrites the URL of requests to the OpenAI developer docs MCP server to
// include ?source=codex. For all other URLs, the original request is used
// unchanged.
func MaybeWithOpenAIDocsSourceAttribution(serverURL string) func(*http.Request) {
	serverURL = strings.TrimSpace(serverURL)
	if serverURL == openAIDeveloperDocsMCPURL {
		return openAIAttributionRequestModifier
	}
	return nil
}

// openAIAttributionRequestModifier rewrites the request URL to include
// ?source=codex for the OpenAI developer docs MCP endpoint.
func openAIAttributionRequestModifier(req *http.Request) {
	if req == nil || req.URL == nil {
		return
	}
	reqURL := req.URL.String()
	if reqURL == openAIDeveloperDocsMCPURL || strings.HasPrefix(reqURL, openAIDeveloperDocsMCPURL) {
		parsed, err := url.Parse(openAIDeveloperDocsMCPCodexURL)
		if err != nil {
			return
		}
		// Preserve any existing query parameters and add ?source=codex
		query := req.URL.Query()
		query.Set("source", "codex")
		req.URL.RawQuery = query.Encode()
		// If the original URL had no path beyond /mcp, use the parsed URL's structure
		if parsed.Path != "" && req.URL.Path == parsed.Path || req.URL.Path == "" {
			// Paths match; just add the source param we already set via Set above
		}
		_ = parsed // resolved
	}
}
