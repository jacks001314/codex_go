package statuslineprovider

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/mcp"
	"codex_go/plugin"
)

type requestCall struct {
	Method string
	Params map[string]any
}

type fakeApp struct {
	mu      sync.Mutex
	calls   []requestCall
	handler func(method string, params map[string]any) (map[string]any, error)
}

func (f *fakeApp) Request(ctx context.Context, method string, params any) (map[string]any, error) {
	encoded, _ := json.Marshal(params)
	var callParams map[string]any
	_ = json.Unmarshal(encoded, &callParams)
	f.mu.Lock()
	f.calls = append(f.calls, requestCall{Method: method, Params: callParams})
	f.mu.Unlock()
	if f.handler == nil {
		return map[string]any{}, nil
	}
	return f.handler(method, callParams)
}

func (f *fakeApp) callsFor(method string) []requestCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []requestCall{}
	for _, call := range f.calls {
		if call.Method == method {
			out = append(out, call)
		}
	}
	return out
}

func statusRow(name string, httpOrigin any, pluginID string, runtime mcp.MCPConnectionStatus, tools ...map[string]any) map[string]any {
	row := map[string]any{
		"name":          name,
		"httpOrigin":    httpOrigin,
		"runtimeStatus": string(runtime),
		"tools":         tools,
	}
	if pluginID != "" {
		row["pluginId"] = pluginID
	}
	return row
}

func statuslineTool(name string, title string, uri string) map[string]any {
	return map[string]any{
		"name":  name,
		"title": title,
		"_meta": map[string]any{
			"openai/ui": map[string]any{
				"entrypoints": []any{
					map[string]any{"type": "statusline", "resourceUri": uri},
				},
			},
		},
	}
}

func TestProviderFromToolKeepsSavedIdentityLikeRust(t *testing.T) {
	owner, _ := plugin.ParsePluginId("tool@market")
	provider := ProviderFromTool("calendar", owner, mcp.MCPToolInfo{
		Name:  "usage",
		Title: "Usage dashboard",
		Meta: map[string]any{"openai/ui": map[string]any{"entrypoints": []any{
			map[string]any{"type": "statusline", "resourceUri": "ui://calendar/usage"},
		}}},
	})
	if provider == nil {
		t.Fatal("statusline provider was not parsed")
	}
	if provider.ID != (ProviderID{Server: "calendar", Tool: "usage"}) || provider.Title != "Usage dashboard" || provider.URI != "ui://calendar/usage" {
		t.Fatalf("provider = %#v", provider)
	}
	if provider.PluginLabel != "tool@market" || provider.PluginID == nil || provider.PluginID.Key() != "tool@market" {
		t.Fatalf("plugin identity = %#v", provider)
	}

	if got := ProviderFromTool("calendar", nil, mcp.MCPToolInfo{Name: "plain"}); got != nil {
		t.Fatalf("tool without metadata produced %#v", got)
	}
	invalidURIs := []string{"https://calendar/usage", "ui://", "ui://calendar/bad path", "ui://calendar/%zz"}
	for _, uri := range invalidURIs {
		tool := mcp.MCPToolInfo{Name: "bad", Meta: map[string]any{"openai/ui": map[string]any{"entrypoints": []any{
			map[string]any{"type": "statusline", "resourceUri": uri},
		}}}}
		if got := ProviderFromTool("calendar", nil, tool); got != nil {
			t.Fatalf("URI %q produced %#v", uri, got)
		}
	}
	multiple := mcp.MCPToolInfo{Name: "ambiguous", Meta: map[string]any{"openai/ui": map[string]any{"entrypoints": []any{
		map[string]any{"type": "statusline", "resourceUri": "ui://calendar/a"},
		map[string]any{"type": "statusline", "resourceUri": "ui://calendar/b"},
	}}}}
	if got := ProviderFromTool("calendar", nil, multiple); got != nil {
		t.Fatalf("multiple statusline entrypoints produced %#v", got)
	}
}

func TestServerInventoryFiltersAndPaginatesLikeRust(t *testing.T) {
	app := &fakeApp{}
	app.handler = func(method string, params map[string]any) (map[string]any, error) {
		if method != mcpServerStatusListMethod {
			t.Fatalf("method = %q", method)
		}
		if params["serverName"] != nil || params["threadId"] != nil {
			t.Fatalf("global params = %#v", params)
		}
		if _, ok := params["environmentId"]; !ok || params["environmentId"] != nil {
			t.Fatalf("environmentId must be explicitly null: %#v", params)
		}
		if params["cursor"] == nil {
			return map[string]any{
				"data": []any{
					statusRow("local", nil, "", mcp.MCPConnectionConnected, statuslineTool("usage", "Usage", "ui://local/usage")),
					statusRow("remote", "https://example.test", "", mcp.MCPConnectionConnected, statuslineTool("remote", "", "ui://remote/usage")),
					map[string]any{"name": "legacy", "tools": []any{}},
					statusRow("failed", nil, "", mcp.MCPConnectionFailed, statuslineTool("failed", "", "ui://failed/usage")),
					statusRow("badplugin", nil, "invalid", mcp.MCPConnectionConnected, statuslineTool("bad", "", "ui://bad/usage")),
				},
				"nextCursor": "page-2",
			}, nil
		}
		return map[string]any{
			"data":       []any{statusRow("second", nil, "", mcp.MCPConnectionConnected, statuslineTool("second", "", "ui://second/usage"))},
			"nextCursor": nil,
		}, nil
	}
	events := globalPages(context.Background(), app)
	var servers []Server
	var finalErr error
	for event := range events {
		servers = append(servers, event.Servers...)
		if event.Done {
			finalErr = event.Err
		}
	}
	if finalErr != nil {
		t.Fatalf("global pages error = %v", finalErr)
	}
	names := []string{}
	for _, server := range servers {
		names = append(names, server.Name)
	}
	if len(names) != 3 || names[0] != "local" || names[1] != "failed" || names[2] != "second" {
		t.Fatalf("server names = %#v", names)
	}
	providers, err := servers[0].Providers()
	if err != nil || len(providers) != 1 || providers[0].ID.Tool != "usage" {
		t.Fatalf("local providers = %#v, err = %v", providers, err)
	}
	if _, err := servers[1].Providers(); err == nil || err.Error() != "MCP server failed is not connected" {
		t.Fatalf("failed server error = %v", err)
	}
}

func TestTargetedInventoryRejectsAmbiguousAnswersLikeRust(t *testing.T) {
	row := statusRow("calendar", nil, "", mcp.MCPConnectionConnected, statuslineTool("usage", "", "ui://calendar/usage"))
	for _, tc := range []struct {
		name string
		data []any
		next any
		want string
	}{
		{name: "more than one", data: []any{row, row}, want: "targeted MCP inventory returned more than one server"},
		{name: "other name", data: []any{statusRow("other", nil, "", mcp.MCPConnectionConnected)}, want: "targeted MCP inventory did not identify the requested server"},
		{name: "more", data: []any{row}, next: "next", want: "targeted MCP inventory returned more than one server"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := &fakeApp{handler: func(method string, params map[string]any) (map[string]any, error) {
				if params["serverName"] != "calendar" {
					t.Fatalf("serverName = %#v", params["serverName"])
				}
				return map[string]any{"data": tc.data, "nextCursor": tc.next}, nil
			}}
			_, err := inventoryServer(context.Background(), app, "calendar", nil)
			if err == nil || err.Error() != tc.want {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestResolveChecksProjectScopeAndLiveOwnerLikeRust(t *testing.T) {
	owner, _ := plugin.ParsePluginId("tool@market")
	global := statusRow("calendar", nil, owner.Key(), mcp.MCPConnectionConnected, statuslineTool("usage", "Usage", "ui://calendar/usage"))
	live := statusRow("calendar", nil, owner.Key(), mcp.MCPConnectionConnected, statuslineTool("usage", "Usage", "ui://calendar/usage"))
	newApp := func() *fakeApp {
		return &fakeApp{handler: func(method string, params map[string]any) (map[string]any, error) {
			if method != mcpServerStatusListMethod {
				t.Fatalf("method = %q", method)
			}
			if params["threadId"] == nil {
				return map[string]any{"data": []any{global}}, nil
			}
			return map[string]any{"data": []any{live}}, nil
		}}
	}
	userLayer := config.Layer{Name: config.LayerSource{Type: config.LayerSourceUser}, Config: map[string]any{}}
	projectLayer := func(values map[string]any) config.Layer {
		return config.Layer{Name: config.LayerSource{Type: config.LayerSourceProject}, Config: values}
	}
	layers := func(stack ...config.Layer) LayerReader {
		return func(context.Context, string) ([]config.Layer, error) { return stack, nil }
	}
	id := ProviderID{Server: "calendar", Tool: "usage"}

	resolved, err := Lookup(context.Background(), newApp(), layers(userLayer), "thread-1", "/work", Selection{Saved: &id})
	if err != nil || resolved == nil || resolved.Provider.ID != id {
		t.Fatalf("resolved = %#v, err = %v", resolved, err)
	}

	for _, tc := range []struct {
		name    string
		project map[string]any
	}{
		{name: "server", project: map[string]any{"mcp_servers": map[string]any{"calendar": map[string]any{"command": "calendar"}}}},
		{name: "plugin", project: map[string]any{"plugins": map[string]any{owner.Key(): map[string]any{"enabled": true}}}},
		{name: "marketplace", project: map[string]any{"marketplaces": map[string]any{owner.MarketplaceName: map[string]any{"enabled": true}}}},
	} {
		t.Run("project "+tc.name, func(t *testing.T) {
			resolved, err := Lookup(context.Background(), newApp(), layers(projectLayer(tc.project), userLayer), "thread-1", "/work", Selection{Saved: &id})
			if err != nil || resolved != nil {
				t.Fatalf("resolved = %#v, err = %v", resolved, err)
			}
		})
	}

	userSets := config.Layer{Name: config.LayerSource{Type: config.LayerSourceUser}, Config: map[string]any{"mcp_servers": map[string]any{"calendar": map[string]any{"command": "calendar"}}}}
	lowerProject := projectLayer(map[string]any{"mcp_servers": map[string]any{"calendar": map[string]any{"command": "old"}}})
	resolved, err = Lookup(context.Background(), newApp(), layers(userSets, lowerProject), "thread-1", "/work", Selection{Saved: &id})
	if err != nil || resolved == nil {
		t.Fatalf("shadowed project server blocked provider: resolved=%#v err=%v", resolved, err)
	}

	mismatchedLive := statusRow("calendar", nil, "other@market", mcp.MCPConnectionConnected, statuslineTool("usage", "Usage", "ui://calendar/usage"))
	app := newApp()
	app.handler = func(method string, params map[string]any) (map[string]any, error) {
		if params["threadId"] == nil {
			return map[string]any{"data": []any{global}}, nil
		}
		return map[string]any{"data": []any{mismatchedLive}}, nil
	}
	resolved, err = Lookup(context.Background(), app, layers(userLayer), "thread-1", "/work", Selection{Saved: &id})
	if err != nil || resolved != nil {
		t.Fatalf("owner mismatch resolved=%#v err=%v", resolved, err)
	}

}

func TestLookupTimeoutIsBoundedLikeRust(t *testing.T) {
	previous := statuslineLookupTimeout
	statuslineLookupTimeout = 30 * time.Millisecond
	defer func() { statuslineLookupTimeout = previous }()
	app := &fakeApp{}
	reader := func(ctx context.Context, cwd string) ([]config.Layer, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	id := ProviderID{Server: "calendar", Tool: "usage"}
	_, err := Lookup(context.Background(), app, reader, "thread-1", "/work", Selection{Saved: &id})
	var lookupErr *LookupError
	if !errors.As(err, &lookupErr) || lookupErr.Kind != LookupTimedOut {
		t.Fatalf("error = %v", err)
	}
}

func TestReadUsesThreadScopedResourceAndNullEnvironmentLikeRust(t *testing.T) {
	app := &fakeApp{handler: func(method string, params map[string]any) (map[string]any, error) {
		if method != mcpResourceReadMethod {
			t.Fatalf("method = %q", method)
		}
		return map[string]any{"contents": []any{}}, nil
	}}
	owner, _ := plugin.ParsePluginId("tool@market")
	resolved := &Resolved{app: app, ThreadID: "thread-1", Provider: Provider{
		ID: ProviderID{Server: "calendar", Tool: "usage"}, URI: "ui://calendar/usage", PluginID: owner,
	}}
	if _, err := resolved.Read(context.Background(), map[string]any{"version": 1}); err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	calls := app.callsFor(mcpResourceReadMethod)
	if len(calls) != 1 {
		t.Fatalf("read calls = %#v", calls)
	}
	params := calls[0].Params
	if params["threadId"] != "thread-1" || params["server"] != "calendar" || params["uri"] != "ui://calendar/usage" {
		t.Fatalf("read params = %#v", params)
	}
	if _, ok := params["environmentId"]; !ok || params["environmentId"] != nil {
		t.Fatalf("environmentId = %#v", params)
	}
	meta, _ := params["_meta"].(map[string]any)
	if meta["com.openai/codex-statusline-state"] == nil {
		t.Fatalf("statusline meta = %#v", params["_meta"])
	}
}

func TestRefreshPublishesInventoryAndKnownLookupLikeRust(t *testing.T) {
	previousHead, previousTimeout := inventoryHeadStart, statuslineLookupTimeout
	inventoryHeadStart = 20 * time.Millisecond
	statuslineLookupTimeout = 500 * time.Millisecond
	defer func() {
		inventoryHeadStart = previousHead
		statuslineLookupTimeout = previousTimeout
	}()
	row := statusRow("calendar", nil, "", mcp.MCPConnectionConnected, statuslineTool("usage", "Usage", "ui://calendar/usage"))
	slow := statusRow("slow", nil, "", mcp.MCPConnectionConnected, statuslineTool("slow", "Slow", "ui://slow/usage"))
	app := &fakeApp{handler: func(method string, params map[string]any) (map[string]any, error) {
		if params["serverName"] == nil {
			return map[string]any{"data": []any{row}}, nil
		}
		if params["serverName"] == "slow" {
			return map[string]any{"data": []any{slow}}, nil
		}
		return map[string]any{"data": []any{}}, nil
	}}
	updates := Refresh(context.Background(), app, []string{"slow"})
	got := map[string]Update{}
	for update := range updates {
		if update.Kind == UpdateServer {
			got[update.Server] = update
		}
	}
	if len(got) != 2 || len(got["calendar"].Providers) != 1 || len(got["slow"].Providers) != 1 {
		t.Fatalf("updates = %#v", got)
	}
	for _, call := range app.callsFor(mcpServerStatusListMethod) {
		if value, ok := call.Params["environmentId"]; !ok || value != nil {
			t.Fatalf("environmentId in request = %#v", call.Params)
		}
	}
}

func TestRefreshTimesOutNamedProbeLikeRust(t *testing.T) {
	previousHead, previousTimeout := inventoryHeadStart, statuslineLookupTimeout
	inventoryHeadStart = 10 * time.Millisecond
	statuslineLookupTimeout = 120 * time.Millisecond
	defer func() {
		inventoryHeadStart = previousHead
		statuslineLookupTimeout = previousTimeout
	}()
	release := make(chan struct{})
	app := &fakeApp{handler: func(method string, params map[string]any) (map[string]any, error) {
		if params["serverName"] == "slow" {
			<-release
		}
		return map[string]any{"data": []any{}}, nil
	}}
	updates := Refresh(context.Background(), app, []string{"slow"})
	var slow *Update
	for update := range updates {
		if update.Kind == UpdateServer && update.Server == "slow" {
			copied := update
			slow = &copied
		}
	}
	close(release)
	if slow == nil {
		t.Fatal("slow probe produced no update")
	}
	var lookupErr *LookupError
	if !errors.As(slow.Err, &lookupErr) || lookupErr.Kind != LookupTimedOut {
		t.Fatalf("slow probe error = %v", slow.Err)
	}
}
