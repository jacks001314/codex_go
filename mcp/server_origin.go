package mcp

import (
	"net/url"
	"strings"
)

// ServerOrigin mirrors Rust's McpServerOrigin. It records the transport origin
// of an MCP server for telemetry and diagnostics.
type ServerOrigin struct {
	Origin string `json:"origin"`
}

// NewServerOriginFromConfig derives the server origin from a ServerConfig's
// transport settings. For stdio servers, the origin is "stdio". For HTTP
// servers, the origin is the URL scheme + host.
func NewServerOriginFromConfig(config *ServerConfig) *ServerOrigin {
	if config == nil {
		return nil
	}
	if strings.TrimSpace(config.Command) != "" {
		return &ServerOrigin{Origin: "stdio"}
	}
	if configURL := strings.TrimSpace(config.URL); configURL != "" {
		parsed, err := url.Parse(configURL)
		if err != nil {
			return nil
		}
		origin := parsed.Scheme + "://" + parsed.Host
		return &ServerOrigin{Origin: origin}
	}
	return nil
}

// OriginString returns the origin string for telemetry use.
func (o *ServerOrigin) OriginString() string {
	if o == nil {
		return ""
	}
	return o.Origin
}

// Transport returns the transport type derived from the origin.
// Returns "stdio" for stdio origins or "streamable_http" for HTTP origins.
func (o *ServerOrigin) Transport() string {
	if o == nil {
		return ""
	}
	if o.Origin == "stdio" {
		return "stdio"
	}
	return "streamable_http"
}

// ServerOriginTracker tracks which MCP server each tool originates from.
// This is used during tool exposure to annotate tools with their server origin.
type ServerOriginTracker struct {
	origins map[string]*ServerOrigin
}

// NewServerOriginTracker creates a new tracker from a map of configured servers.
func NewServerOriginTracker(servers map[string]ServerRegistration) *ServerOriginTracker {
	tracker := &ServerOriginTracker{origins: make(map[string]*ServerOrigin, len(servers))}
	for name, registration := range servers {
		origin := NewServerOriginFromConfig(&registration.Config)
		if origin != nil {
			tracker.origins[name] = origin
		}
	}
	return tracker
}

// OriginForServer returns the ServerOrigin for a given server name.
func (t *ServerOriginTracker) OriginForServer(serverName string) *ServerOrigin {
	if t == nil {
		return nil
	}
	return t.origins[serverName]
}

// AnnotateToolsWithOrigin annotates a slice of RuntimeToolInfo with server
// origin information for telemetry. It sets the ServerOrigin field on each tool.
func (t *ServerOriginTracker) AnnotateToolsWithOrigin(tools []RuntimeToolInfo) []RuntimeToolInfo {
	if t == nil {
		return tools
	}
	out := make([]RuntimeToolInfo, len(tools))
	for i := range tools {
		out[i] = tools[i]
		if origin := t.OriginForServer(tools[i].ServerName); origin != nil {
			out[i].ServerOrigin = origin.OriginString()
		}
	}
	return out
}
