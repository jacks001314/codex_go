package statuslineprovider

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"unicode"

	"codex_go/mcp"
	"codex_go/plugin"
)

// AppServer is the narrow request surface needed by the statusline provider
// subsystem. The app-server adapter marshals params and returns the decoded
// result object.
type AppServer interface {
	Request(ctx context.Context, method string, params any) (map[string]any, error)
}

// ProviderID is the persisted identity of a statusline provider. Display
// titles and resource URIs can change without changing the saved identity.
type ProviderID struct {
	Server string
	Tool   string
}

// Provider is one MCP tool that advertises a statusline UI entrypoint.
type Provider struct {
	ID          ProviderID
	Title       string
	PluginLabel string
	URI         string
	PluginID    *plugin.PluginId
}

// Selection chooses either a saved identity or a provider advertised by the
// current inventory refresh.
type Selection struct {
	Saved      *ProviderID
	Discovered *Provider
}

// LookupError distinguishes the bounded lookup deadline from request errors.
type LookupError struct {
	Kind string
	Err  error
}

const (
	LookupTimedOut = "timed_out"
	LookupRequest  = "request"
)

func (e *LookupError) Error() string {
	if e == nil {
		return ""
	}
	if e.Kind == LookupTimedOut {
		return "statusline provider lookup timed out after 5 seconds"
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return "statusline provider lookup failed"
}

func (e *LookupError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// ProviderFromTool interprets the statusline entrypoint advertised in one MCP
// tool's _meta. A tool with more than one valid statusline URI is ignored.
func ProviderFromTool(server string, pluginID *plugin.PluginId, tool mcp.MCPToolInfo) *Provider {
	meta, ok := normalizeObject(tool.Meta)
	if !ok {
		return nil
	}
	ui, ok := meta["openai/ui"].(map[string]any)
	if !ok {
		return nil
	}
	entries, ok := ui["entrypoints"].([]any)
	if !ok {
		return nil
	}
	var uri string
	for _, raw := range entries {
		entry, ok := raw.(map[string]any)
		if !ok || entry["type"] != "statusline" {
			continue
		}
		candidate, ok := entry["resourceUri"].(string)
		if !ok || !validStatuslineURI(candidate) {
			continue
		}
		if uri != "" {
			// Multiple valid statusline entrypoints are ambiguous.
			return nil
		}
		uri = candidate
	}
	if uri == "" {
		return nil
	}
	title := strings.TrimSpace(tool.Title)
	if title == "" {
		title = tool.Name
	}
	provider := &Provider{
		ID:       ProviderID{Server: server, Tool: tool.Name},
		Title:    title,
		URI:      uri,
		PluginID: pluginID.Clone(),
	}
	if pluginID != nil {
		provider.PluginLabel = pluginID.Key()
	}
	return provider
}

func validStatuslineURI(uri string) bool {
	if !strings.HasPrefix(uri, "ui://") {
		return false
	}
	for _, ch := range uri {
		if unicode.IsSpace(ch) || unicode.IsControl(ch) {
			return false
		}
	}
	parsed, err := url.Parse(uri)
	if err != nil || parsed.Scheme != "ui" || parsed.Host == "" {
		return false
	}
	return true
}

func normalizeObject(value any) (map[string]any, bool) {
	switch typed := value.(type) {
	case map[string]any:
		return typed, true
	case json.RawMessage:
		var out map[string]any
		if json.Unmarshal(typed, &out) == nil {
			return out, true
		}
	case []byte:
		var out map[string]any
		if json.Unmarshal(typed, &out) == nil {
			return out, true
		}
	default:
		if value == nil {
			return nil, false
		}
		encoded, err := json.Marshal(value)
		if err == nil {
			var out map[string]any
			if json.Unmarshal(encoded, &out) == nil {
				return out, true
			}
		}
	}
	return nil, false
}

var errInvalidSelection = errors.New("statusline provider selection must name a saved or discovered provider")
