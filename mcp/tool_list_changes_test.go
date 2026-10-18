package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// notifyingToolListServer is an HTTP MCP server whose tool catalog and
// notification behaviour a test can steer. It counts `tools/list` requests so a
// test can prove whether a read re-listed the server or answered from a
// retained catalog.
type notifyingToolListServer struct {
	server *httptest.Server

	mu     sync.Mutex
	tools  []string
	lists  int
	fail   bool
	notify bool
}

func newNotifyingToolListServer(t *testing.T) *notifyingToolListServer {
	t.Helper()
	ts := &notifyingToolListServer{}
	ts.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var request struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("Decode() error = %v", err)
			return
		}
		switch request.Method {
		case "initialize":
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{
				"protocolVersion": defaultMCPProtocol,
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]string{"name": "docs", "version": "1"},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			ts.mu.Lock()
			ts.lists++
			fail, notify, names := ts.fail, ts.notify, append([]string(nil), ts.tools...)
			ts.mu.Unlock()
			if fail {
				writeHTTPMCPError(t, w, request.ID, -32603, "tools/list failed")
				return
			}
			tools := make([]map[string]any, 0, len(names))
			for _, name := range names {
				tools = append(tools, map[string]any{"name": name, "inputSchema": map[string]any{"type": "object"}})
			}
			if notify {
				writeHTTPMCPToolListChangedSSE(t, w, request.ID, map[string]any{"tools": tools})
				return
			}
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{"tools": tools})
		default:
			writeHTTPMCPError(t, w, request.ID, -32601, "not found")
		}
	}))
	t.Cleanup(ts.server.Close)
	return ts
}

func (ts *notifyingToolListServer) url() string { return ts.server.URL }

func (ts *notifyingToolListServer) setTools(names ...string) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.tools = names
}

func (ts *notifyingToolListServer) setFail(fail bool) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.fail = fail
}

func (ts *notifyingToolListServer) setNotify(notify bool) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.notify = notify
}

func (ts *notifyingToolListServer) listCount() int {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.lists
}

// writeHTTPMCPToolListChangedSSE answers one request with an SSE stream that
// carries a `notifications/tools/list_changed` notification in front of the
// response, which is how a streamable HTTP server reports a catalog change
// (Rust #54656).
func writeHTTPMCPToolListChangedSSE(t *testing.T, w http.ResponseWriter, id int64, result any) {
	t.Helper()
	notification, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": mcpToolListChangedMethod})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	payload, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	w.Header().Set("Content-Type", "text/event-stream")
	if _, err := io.WriteString(w, "data: "+string(notification)+"\n\ndata: "+string(payload)+"\n\n"); err != nil {
		t.Errorf("WriteString() error = %v", err)
	}
}

func mcpStatusToolNames(statuses []MCPServerStatus) []string {
	names := make([]string, 0)
	for _, status := range statuses {
		for _, tool := range status.Tools {
			names = append(names, tool.Name)
		}
	}
	return names
}

func mcpToolListRefreshTestService(t *testing.T, ts *notifyingToolListServer, featureEnabled bool) *MCPService {
	t.Helper()
	service := NewMCPService(&RuntimeConfig{
		Servers: map[string]ServerRegistration{
			"docs": {Config: ServerConfig{URL: ts.url(), Enabled: true, Required: true}},
		},
		RefreshToolListOnNotifications: featureEnabled,
	})
	t.Cleanup(func() { _ = service.Close() })
	return service
}

func mcpToolListRefreshTestParams() *MCPListServerStatusParams {
	return &MCPListServerStatusParams{
		Detail:              &MCPServerStatusDetail{Mode: MCPServerStatusDetailToolsAndAuthOnly},
		NonBlockingOptional: true,
	}
}

func mcpToolListRefreshTestClient(t *testing.T, service *MCPService) *httpClient {
	t.Helper()
	service.mu.Lock()
	cached := service.httpClients["docs"]
	service.mu.Unlock()
	if cached == nil || cached.client == nil {
		t.Fatalf("docs has no cached HTTP connection")
	}
	return cached.client
}

func mcpToolListRefreshTestRead(t *testing.T, service *MCPService, params *MCPListServerStatusParams) []string {
	t.Helper()
	response, err := service.ListStatusChecked(params)
	if err != nil {
		t.Fatalf("ListStatusChecked() error = %v", err)
	}
	if len(response.Data) != 1 {
		t.Fatalf("status data = %#v", response.Data)
	}
	return mcpStatusToolNames(response.Data)
}

// TestMCPToolListChangedFeatureDrivesRuntimeConfig covers Rust #54656's feature
// gate: the notification refresh is off unless `mcp_tool_list_changed` is set.
func TestMCPToolListChangedFeatureDrivesRuntimeConfig(t *testing.T) {
	enabled := RuntimeConfigFromValues(map[string]any{
		"features": map[string]any{"mcp_tool_list_changed": true},
	}, "")
	if !enabled.RefreshToolListOnNotifications {
		t.Fatalf("RefreshToolListOnNotifications = false, want true")
	}
	disabled := RuntimeConfigFromValues(map[string]any{
		"features": map[string]any{"mcp_tool_list_changed": false},
	}, "")
	if disabled.RefreshToolListOnNotifications {
		t.Fatalf("RefreshToolListOnNotifications = true, want false")
	}
	absent := RuntimeConfigFromValues(map[string]any{}, "")
	if absent.RefreshToolListOnNotifications {
		t.Fatalf("RefreshToolListOnNotifications = true for an absent feature, want false")
	}
}

// TestMCPToolListChangeRecordingCountsOnlyToolListChanges covers the Rust
// #54656 transport rule: a connection counts `notifications/tools/list_changed`
// in transport order, and no other notification invalidates its catalog.
func TestMCPToolListChangeRecordingCountsOnlyToolListChanges(t *testing.T) {
	var revision mcpToolListRevision
	recordMCPToolListChangedNotification(&revision, "notifications/tools/list_changed")
	if revision.current() != 1 {
		t.Fatalf("revision after tools/list_changed = %d, want 1", revision.current())
	}
	recordMCPToolListChangedNotification(&revision, "notifications/progress")
	recordMCPToolListChangedNotification(&revision, "notifications/cancelled")
	if revision.current() != 1 {
		t.Fatalf("revision after unrelated notifications = %d, want 1", revision.current())
	}
	recordMCPToolListChangedNotification(nil, "notifications/tools/list_changed")
	if revision.current() != 1 {
		t.Fatalf("revision after a nil recorder = %d, want 1", revision.current())
	}

	// The stdio read loop counts the notification it dispatches.
	stdio := &stdioClient{}
	if err := stdio.handleClientNotification(context.Background(), "docs", nil, mcpToolListChangedMethod, nil); err != nil {
		t.Fatalf("handleClientNotification() error = %v", err)
	}
	if err := stdio.handleClientNotification(context.Background(), "docs", nil, "notifications/message", nil); err != nil {
		t.Fatalf("handleClientNotification() error = %v", err)
	}
	if got := stdio.toolsListRevision(); got != 1 {
		t.Fatalf("stdio toolsListRevision = %d, want 1", got)
	}

	// The HTTP read path counts the notification an SSE stream delivers next to
	// a response.
	client := newMCPHTTPClient(nil)
	body := "data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/tools/list_changed\"}\n\n" +
		"data: {\"jsonrpc\":\"2.0\",\"id\":7,\"result\":{\"tools\":[]}}\n\n"
	response := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
	if _, err := client.readRPCResponse(context.Background(), response, 7, ""); err != nil {
		t.Fatalf("readRPCResponse() error = %v", err)
	}
	if got := client.toolsListRevision(); got != 1 {
		t.Fatalf("http toolsListRevision = %d, want 1", got)
	}
	// A streaming request wraps the caller's handler the same way.
	tracked := client.trackingToolListNotifications(nil)
	if err := tracked(&stdioRPCEnvelope{Method: "notifications/progress"}); err != nil {
		t.Fatalf("tracking handler error = %v", err)
	}
	if err := tracked(&stdioRPCEnvelope{Method: mcpToolListChangedMethod}); err != nil {
		t.Fatalf("tracking handler error = %v", err)
	}
	if got := client.toolsListRevision(); got != 2 {
		t.Fatalf("http toolsListRevision after tracking handler = %d, want 2", got)
	}
}

// TestMCPToolListRefreshStateDecisions covers the Rust #54656 bookkeeping: a
// catalog is current at the revision it was listed at, an unconsumed
// notification forces a re-list, and a failed refresh serves the old catalog
// until its cooldown expires.
func TestMCPToolListRefreshStateDecisions(t *testing.T) {
	now := time.Now()
	var state mcpToolListRefreshState
	if got := state.decisionLocked(0, now); got != mcpToolListNoChange {
		t.Fatalf("decision at revision 0 = %v, want no change", got)
	}
	if got := state.decisionLocked(3, now); got != mcpToolListRefresh {
		t.Fatalf("decision at revision 3 = %v, want refresh", got)
	}
	state.publishedLocked(3)
	if got := state.decisionLocked(3, now); got != mcpToolListNoChange {
		t.Fatalf("decision after publishing revision 3 = %v, want no change", got)
	}
	state.cooldownLocked(now)
	if got := state.decisionLocked(4, now); got != mcpToolListInCooldown {
		t.Fatalf("decision during cooldown = %v, want in cooldown", got)
	}
	if got := state.decisionLocked(3, now); got != mcpToolListNoChange {
		t.Fatalf("decision for the published revision during cooldown = %v, want no change", got)
	}
	afterCooldown := now.Add(mcpToolListNotificationRefreshCooldown + time.Second)
	if got := state.decisionLocked(4, afterCooldown); got != mcpToolListRefresh {
		t.Fatalf("decision after the cooldown = %v, want refresh", got)
	}
	state.publishedLocked(4)
	if got := state.decisionLocked(4, afterCooldown); got != mcpToolListNoChange {
		t.Fatalf("decision after republishing = %v, want no change", got)
	}
}

// TestRetainedToolCatalogInventoryCopiesThePublishedCatalog covers the retained
// catalog Rust #54656 falls back to: it rebuilds the published catalog, and it
// is absent when nothing was published yet.
func TestRetainedToolCatalogInventoryCopiesThePublishedCatalog(t *testing.T) {
	if inventory := retainedToolCatalogInventory(MCPServerStatus{}); inventory != nil {
		t.Fatalf("retained inventory without tools = %#v, want nil", inventory)
	}
	published := MCPServerStatus{
		Tools:              []MCPToolInfo{{Name: "search"}},
		Resources:          []MCPResource{{URI: "file:///one"}},
		ResourceTemplates:  []MCPResourceTemplate{{URITemplate: "file:///{path}"}},
		ToolListMeta:       json.RawMessage(`{"traceId":"retained"}`),
		ServerCapabilities: json.RawMessage(`{"tools":{}}`),
	}
	inventory := retainedToolCatalogInventory(published)
	if inventory == nil {
		t.Fatalf("retained inventory = nil, want the published catalog")
	}
	if len(inventory.Tools) != 1 || inventory.Tools[0].Name != "search" {
		t.Fatalf("retained tools = %#v", inventory.Tools)
	}
	if string(inventory.ToolListMeta) != `{"traceId":"retained"}` {
		t.Fatalf("retained tool list metadata = %s", inventory.ToolListMeta)
	}
	if string(inventory.ServerCapabilities) != `{"tools":{}}` {
		t.Fatalf("retained capabilities = %s", inventory.ServerCapabilities)
	}
	// The retained catalog must not alias the status it was rebuilt from.
	published.Tools[0].Name = "mutated"
	if inventory.Tools[0].Name != "search" {
		t.Fatalf("retained tools alias the published status: %#v", inventory.Tools)
	}
}

// TestMCPToolListRefreshGateExcludesCodexAppsAndDisabledFeature covers the two
// Rust #54656 exclusions: the refresh is feature-gated, and the host-owned Codex
// Apps registration keeps its shared provider.
func TestMCPToolListRefreshGateExcludesCodexAppsAndDisabledFeature(t *testing.T) {
	notified := func(service *MCPService, name string) {
		service.mu.Lock()
		if service.httpClients[name] == nil {
			service.httpClients[name] = &cachedMCPHTTPClient{client: newMCPHTTPClient(nil)}
		}
		client := service.httpClients[name].client
		service.mu.Unlock()
		recordMCPToolListChangedNotification(&client.toolListChanges, mcpToolListChangedMethod)
	}

	enabled := NewMCPService(&RuntimeConfig{RefreshToolListOnNotifications: true})
	notified(enabled, "docs")
	if enabled.retainedToolCatalogUsable("docs") {
		t.Fatalf("the retained catalog is usable after a notification, want a re-list")
	}
	if !enabled.mcpToolListRefreshApplies("docs") {
		t.Fatalf("the refresh should apply to an ordinary server")
	}

	notified(enabled, CodexAppsServerName)
	if !enabled.retainedToolCatalogUsable(CodexAppsServerName) {
		t.Fatalf("the Codex Apps catalog must keep its shared provider")
	}
	if enabled.mcpToolListRefreshApplies(CodexAppsServerName) {
		t.Fatalf("the refresh must not apply to the Codex Apps registration")
	}

	disabled := NewMCPService(&RuntimeConfig{})
	notified(disabled, "docs")
	if !disabled.retainedToolCatalogUsable("docs") {
		t.Fatalf("a disabled feature must not refuse the retained catalog")
	}
	if disabled.toolListRefresh != nil {
		t.Fatalf("a disabled feature must not record refresh bookkeeping: %#v", disabled.toolListRefresh)
	}
}

// TestMCPToolListChangeRelistsAndRepublishesWithinTheService covers the Rust
// #54656 end-to-end contract: a `tools/list_changed` notification makes the
// cached catalog unusable, the next read re-lists the server, and the listing it
// published stays current until the next notification.
func TestMCPToolListChangeRelistsAndRepublishesWithinTheService(t *testing.T) {
	ts := newNotifyingToolListServer(t)
	ts.setTools("one")
	service := mcpToolListRefreshTestService(t, ts, true)

	if names := mcpToolListRefreshTestRead(t, service, mcpToolListRefreshTestParams()); !reflect.DeepEqual(names, []string{"one"}) {
		t.Fatalf("first read tools = %#v, want [one]", names)
	}
	if got := ts.listCount(); got != 1 {
		t.Fatalf("tools/list calls = %d, want 1", got)
	}
	// No notification invalidated the connection, so the retained catalog
	// answers this read without listing the server again.
	if names := mcpToolListRefreshTestRead(t, service, mcpToolListRefreshTestParams()); !reflect.DeepEqual(names, []string{"one"}) {
		t.Fatalf("second read tools = %#v, want [one]", names)
	}
	if got := ts.listCount(); got != 1 {
		t.Fatalf("tools/list calls = %d, want the retained catalog to be reused", got)
	}

	client := mcpToolListRefreshTestClient(t, service)
	recordMCPToolListChangedNotification(&client.toolListChanges, mcpToolListChangedMethod)
	// The server publishes a different catalog while the notification is
	// unconsumed.
	ts.setTools("two")

	if names := mcpToolListRefreshTestRead(t, service, mcpToolListRefreshTestParams()); !reflect.DeepEqual(names, []string{"two"}) {
		t.Fatalf("third read tools = %#v, want the re-listed catalog [two]", names)
	}
	if got := ts.listCount(); got != 2 {
		t.Fatalf("tools/list calls = %d, want the notification to force a re-list", got)
	}
	// The re-listed catalog is current again, so the next read reuses it.
	if names := mcpToolListRefreshTestRead(t, service, mcpToolListRefreshTestParams()); !reflect.DeepEqual(names, []string{"two"}) {
		t.Fatalf("fourth read tools = %#v, want [two]", names)
	}
	if got := ts.listCount(); got != 2 {
		t.Fatalf("tools/list calls = %d, want the republished catalog to be reused", got)
	}
}

// TestMCPToolListRefreshFailureKeepsThePublishedCatalogAndCoolsDown covers the
// Rust #54656 failure path: a failed refresh keeps the previous catalog and
// stops re-listing until the retry cooldown expires.
func TestMCPToolListRefreshFailureKeepsThePublishedCatalogAndCoolsDown(t *testing.T) {
	ts := newNotifyingToolListServer(t)
	ts.setTools("one")
	service := mcpToolListRefreshTestService(t, ts, true)

	if names := mcpToolListRefreshTestRead(t, service, mcpToolListRefreshTestParams()); !reflect.DeepEqual(names, []string{"one"}) {
		t.Fatalf("first read tools = %#v, want [one]", names)
	}
	client := mcpToolListRefreshTestClient(t, service)
	recordMCPToolListChangedNotification(&client.toolListChanges, mcpToolListChangedMethod)
	ts.setFail(true)

	// The refresh fails, so the previously published catalog answers the read.
	if names := mcpToolListRefreshTestRead(t, service, mcpToolListRefreshTestParams()); !reflect.DeepEqual(names, []string{"one"}) {
		t.Fatalf("failed refresh tools = %#v, want the retained catalog [one]", names)
	}
	if got := ts.listCount(); got != 2 {
		t.Fatalf("tools/list calls = %d, want exactly one failed refresh attempt", got)
	}

	// A management-style read does not wait out the dormant cache: it lists
	// unless the connection is inside the retry cooldown.
	if names := mcpToolListRefreshTestRead(t, service, &MCPListServerStatusParams{
		Detail: &MCPServerStatusDetail{Mode: MCPServerStatusDetailToolsAndAuthOnly},
	}); !reflect.DeepEqual(names, []string{"one"}) {
		t.Fatalf("cooled-down read tools = %#v, want the retained catalog [one]", names)
	}
	if got := ts.listCount(); got != 2 {
		t.Fatalf("tools/list calls = %d, want the cooldown to suppress the retry", got)
	}

	// Once the cooldown expires the refresh is retried, so a recovered server
	// republishes its catalog.
	state := service.toolListRefreshStateFor("docs")
	state.mu.Lock()
	state.retryAfter = time.Now().Add(-time.Second)
	state.mu.Unlock()
	ts.setFail(false)
	ts.setTools("two")
	if names := mcpToolListRefreshTestRead(t, service, mcpToolListRefreshTestParams()); !reflect.DeepEqual(names, []string{"two"}) {
		t.Fatalf("retried refresh tools = %#v, want [two]", names)
	}
	if got := ts.listCount(); got != 3 {
		t.Fatalf("tools/list calls = %d, want the retry after the cooldown", got)
	}
}

// TestMCPToolListRefreshChurnRetriesOnceThenCoolsDown covers the Rust #54656
// churn rule: a notification that arrives while a listing is being fetched makes
// that listing unusable, exactly one retry follows, and a still-churning refresh
// keeps the previous catalog and cools down.
func TestMCPToolListRefreshChurnRetriesOnceThenCoolsDown(t *testing.T) {
	ts := newNotifyingToolListServer(t)
	ts.setTools("one")
	service := mcpToolListRefreshTestService(t, ts, true)

	// The first read publishes the catalog at the connection's revision.
	if names := mcpToolListRefreshTestRead(t, service, mcpToolListRefreshTestParams()); !reflect.DeepEqual(names, []string{"one"}) {
		t.Fatalf("first read tools = %#v, want [one]", names)
	}
	if got := ts.listCount(); got != 1 {
		t.Fatalf("tools/list calls = %d, want 1", got)
	}
	// A notification is outstanding from now on, and every listing races
	// another one, so no listing is stable enough to publish.
	client := mcpToolListRefreshTestClient(t, service)
	recordMCPToolListChangedNotification(&client.toolListChanges, mcpToolListChangedMethod)
	ts.setNotify(true)

	// Every listing now races a notification, so the first read refuses to
	// publish a catalog and reuses the previous one.
	if names := mcpToolListRefreshTestRead(t, service, &MCPListServerStatusParams{
		Detail: &MCPServerStatusDetail{Mode: MCPServerStatusDetailToolsAndAuthOnly},
	}); !reflect.DeepEqual(names, []string{"one"}) {
		t.Fatalf("churning refresh tools = %#v, want the retained catalog [one]", names)
	}
	if got := ts.listCount(); got != 3 {
		t.Fatalf("tools/list calls = %d, want the refresh to retry exactly once", got)
	}
	// The churning refresh is cooling down, so the next read does not list.
	if names := mcpToolListRefreshTestRead(t, service, mcpToolListRefreshTestParams()); !reflect.DeepEqual(names, []string{"one"}) {
		t.Fatalf("cooled-down read tools = %#v, want the retained catalog [one]", names)
	}
	if got := ts.listCount(); got != 3 {
		t.Fatalf("tools/list calls = %d, want the cooldown to suppress the retry", got)
	}

	// After the cooldown the refresh is retried and publishes the settled
	// catalog.
	state := service.toolListRefreshStateFor("docs")
	state.mu.Lock()
	state.retryAfter = time.Now().Add(-time.Second)
	state.mu.Unlock()
	ts.setNotify(false)
	ts.setTools("two")
	if names := mcpToolListRefreshTestRead(t, service, &MCPListServerStatusParams{
		Detail: &MCPServerStatusDetail{Mode: MCPServerStatusDetailToolsAndAuthOnly},
	}); !reflect.DeepEqual(names, []string{"two"}) {
		t.Fatalf("settled refresh tools = %#v, want [two]", names)
	}
}

// TestMCPToolListRefreshWithoutNotificationsListsEveryRead guards the Rust
// #54656 change against over-caching: with the feature on and no notification on
// the wire, a read still lists the server exactly as it did before.
func TestMCPToolListRefreshWithoutNotificationsListsEveryRead(t *testing.T) {
	ts := newNotifyingToolListServer(t)
	ts.setTools("one")
	service := mcpToolListRefreshTestService(t, ts, true)
	params := &MCPListServerStatusParams{Detail: &MCPServerStatusDetail{Mode: MCPServerStatusDetailToolsAndAuthOnly}}

	if names := mcpToolListRefreshTestRead(t, service, params); !reflect.DeepEqual(names, []string{"one"}) {
		t.Fatalf("first read tools = %#v, want [one]", names)
	}
	ts.setTools("two")
	if names := mcpToolListRefreshTestRead(t, service, params); !reflect.DeepEqual(names, []string{"two"}) {
		t.Fatalf("second read tools = %#v, want the live listing [two]", names)
	}
	if got := ts.listCount(); got != 2 {
		t.Fatalf("tools/list calls = %d, want a listing per read", got)
	}
}

// TestMCPToolListRefreshChurnWithoutAPublishedCatalogReportsAnError covers the
// Rust #54656 rule that a refresh which never settles has nothing to fall back
// to: the read reports the churn instead of an empty catalog.
func TestMCPToolListRefreshChurnWithoutAPublishedCatalogReportsAnError(t *testing.T) {
	ts := newNotifyingToolListServer(t)
	ts.setTools("one")
	ts.setNotify(true)
	service := mcpToolListRefreshTestService(t, ts, true)

	// The first read creates the connection; the notification it carries leaves
	// the catalog unpublished.
	_ = mcpToolListRefreshTestRead(t, service, mcpToolListRefreshTestParams())

	config, ok := service.serverConfig("docs")
	if !ok {
		t.Fatalf("docs config = %#v, want a registered server", config)
	}
	before := ts.listCount()
	inventory, err := service.discoverToolCatalogForStatus("docs", &config, "", MCPServerStatus{})
	if err == nil {
		t.Fatalf("churning refresh error = nil, want the churn reported")
	}
	if inventory != nil {
		t.Fatalf("churning refresh inventory = %#v, want nil", inventory)
	}
	if got := ts.listCount(); got != before+2 {
		t.Fatalf("tools/list calls = %d, want exactly one retry after the churn (before=%d)", got, before)
	}
}
