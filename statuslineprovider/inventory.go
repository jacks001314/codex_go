package statuslineprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"codex_go/mcp"
	"codex_go/plugin"
)

const mcpServerStatusListMethod = "mcpServerStatus/list"

// Server is the subset of one MCP inventory row that statusline discovery
// needs.
type Server struct {
	Name          string
	RuntimeStatus *mcp.MCPConnectionStatus
	PluginID      *plugin.PluginId
	Tools         map[string]mcp.MCPToolInfo
	ToolsErr      string
}

// Providers returns every tool in the server catalog that advertises one valid
// statusline entrypoint.
func (s *Server) Providers() ([]Provider, error) {
	if s == nil {
		return nil, nil
	}
	if s.ToolsErr != "" {
		return nil, fmt.Errorf("%s", s.ToolsErr)
	}
	out := make([]Provider, 0, len(s.Tools))
	for _, tool := range s.Tools {
		if provider := ProviderFromTool(s.Name, s.PluginID, tool); provider != nil {
			out = append(out, *provider)
		}
	}
	return out, nil
}

// Provider looks up one advertised statusline tool by name.
func (s *Server) Provider(name string) (*Provider, error) {
	if s == nil {
		return nil, nil
	}
	if s.ToolsErr != "" {
		return nil, fmt.Errorf("%s", s.ToolsErr)
	}
	tool, ok := s.Tools[name]
	if !ok {
		return nil, nil
	}
	return ProviderFromTool(s.Name, s.PluginID, tool), nil
}

type statusPage struct {
	Data       []map[string]any
	NextCursor *string
}

func statusListParams(threadID *string, serverName *string, cursor *string) map[string]any {
	var thread any
	if threadID != nil {
		thread = *threadID
	}
	var server any
	if serverName != nil {
		server = *serverName
	}
	var next any
	if cursor != nil {
		next = *cursor
	}
	return map[string]any{
		"threadId":      thread,
		"serverName":    server,
		"environmentId": nil,
		"detail":        string(mcp.MCPServerStatusDetailToolsAndAuthOnly),
		"cursor":        next,
		"limit":         nil,
	}
}

func readStatusPage(ctx context.Context, app AppServer, params map[string]any) (statusPage, error) {
	raw, err := app.Request(ctx, mcpServerStatusListMethod, params)
	if err != nil {
		return statusPage{}, err
	}
	page := statusPage{}
	data, _ := raw["data"].([]any)
	for _, item := range data {
		if row, ok := item.(map[string]any); ok {
			page.Data = append(page.Data, row)
		}
	}
	if cursor, ok := raw["nextCursor"].(string); ok && strings.TrimSpace(cursor) != "" {
		value := cursor
		page.NextCursor = &value
	}
	return page, nil
}

type pageEvent struct {
	Servers []Server
	Done    bool
	Err     error
}

func globalPages(ctx context.Context, app AppServer) <-chan pageEvent {
	out := make(chan pageEvent, 1)
	go func() {
		defer close(out)
		var cursor *string
		seen := map[string]bool{}
		for {
			if cursor != nil {
				if seen[*cursor] {
					sendPage(ctx, out, pageEvent{Done: true, Err: fmt.Errorf("MCP inventory repeated a cursor")})
					return
				}
				seen[*cursor] = true
			}
			page, err := readStatusPage(ctx, app, statusListParams(nil, nil, cursor))
			if err != nil {
				sendPage(ctx, out, pageEvent{Done: true, Err: err})
				return
			}
			servers := make([]Server, 0, len(page.Data))
			for _, value := range page.Data {
				server := localServer(value)
				if server != nil {
					servers = append(servers, *server)
				}
			}
			if !sendPage(ctx, out, pageEvent{Servers: servers}) {
				return
			}
			if page.NextCursor == nil {
				sendPage(ctx, out, pageEvent{Done: true})
				return
			}
			cursor = page.NextCursor
		}
	}()
	return out
}

func sendPage(ctx context.Context, out chan<- pageEvent, event pageEvent) bool {
	select {
	case out <- event:
		return true
	case <-ctx.Done():
		return false
	}
}

// localServer mirrors the Rust statusline inventory filter: only stdio/local
// servers with an explicit null httpOrigin are eligible. Older rows that omit
// the field have unknown transport and are skipped.
func localServer(raw map[string]any) *Server {
	origin, present := raw["httpOrigin"]
	if !present || origin != nil {
		return nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var status mcp.MCPServerStatus
	if err := json.Unmarshal(encoded, &status); err != nil {
		return nil
	}
	return statuslineServerFromStatus(status)
}

func statuslineServerFromStatus(status mcp.MCPServerStatus) *Server {
	tools := map[string]mcp.MCPToolInfo{}
	var toolsErr string
	switch {
	case status.ToolsError != nil:
		toolsErr = *status.ToolsError
	case status.RuntimeStatus != nil && *status.RuntimeStatus == mcp.MCPConnectionFailed:
		toolsErr = fmt.Sprintf("MCP server %s is not connected", status.Name)
	default:
		for _, tool := range status.Tools {
			tools[tool.Name] = tool
		}
	}
	server := &Server{
		Name:          status.Name,
		RuntimeStatus: cloneConnectionStatus(status.RuntimeStatus),
		Tools:         tools,
		ToolsErr:      toolsErr,
	}
	if status.PluginID != nil {
		if id, err := plugin.ParsePluginId(*status.PluginID); err == nil {
			server.PluginID = id
		} else {
			return nil
		}
	}
	return server
}

func cloneConnectionStatus(value *mcp.MCPConnectionStatus) *mcp.MCPConnectionStatus {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

// inventoryServer asks the targeted global or thread-scoped inventory for one
// named server. Hosts may paginate; a targeted answer must identify exactly the
// requested server.
func inventoryServer(ctx context.Context, app AppServer, name string, threadID *string) (*Server, error) {
	page, err := readStatusPage(ctx, app, statusListParams(threadID, &name, nil))
	if err != nil {
		return nil, err
	}
	if page.NextCursor != nil || len(page.Data) > 1 {
		return nil, fmt.Errorf("targeted MCP inventory returned more than one server")
	}
	if len(page.Data) == 0 {
		return nil, nil
	}
	value := page.Data[0]
	if got, _ := value["name"].(string); got != name {
		return nil, fmt.Errorf("targeted MCP inventory did not identify the requested server")
	}
	return localServer(value), nil
}
