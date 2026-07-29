package appserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"codex_go/auth"
	"codex_go/config"
	"codex_go/mcp"
	"codex_go/plugin"
)

func TestExistingThreadLoadsAPICuratedMCPAfterAuthSwitchSync(t *testing.T) {
	mcpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var rpc struct {
			ID     int64           `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(request.Body).Decode(&rpc); err != nil {
			t.Errorf("decode MCP request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch rpc.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "curated-routing-test")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": rpc.ID,
				"result": map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "serverInfo": map[string]string{"name": "curated", "version": "test"}},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": rpc.ID,
				"result": map[string]any{"tools": []any{map[string]any{"name": "echo", "description": "echo", "inputSchema": map[string]any{"type": "object"}}}},
			})
		case "tools/call":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": rpc.ID,
				"result": map[string]any{"content": []any{}, "structuredContent": map[string]any{"available": true}},
			})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": rpc.ID, "error": map[string]any{"code": -32601, "message": "not found"}})
		}
	}))
	defer mcpServer.Close()

	home := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte("[features]\napps = false\nplugins = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot := auth.FromAPIKey("sk-curated-test")
	if err := auth.NewStore(home).Save(snapshot); err != nil {
		t.Fatal(err)
	}
	account := auth.NewAccountManager()
	account.ApplyAuthSnapshot(&snapshot)
	plugins := plugin.NewPluginService()
	plugins.SetCodexHome(home)
	plugins.AddPlugin(plugin.PluginDetail{Summary: plugin.PluginSummary{
		ID: "api-plugin@openai-api-curated", Name: "api-plugin", MarketplaceName: plugin.OpenAIAPICuratedMarketplaceName,
		Installed: true, Enabled: true,
	}})
	plugins.SetMarketplaceMaterializer(plugin.MarketplaceMaterializerFunc(func(_ *plugin.ParsedMarketplaceSource, _ []string, destination string) error {
		manifestRoot := filepath.Join(destination, ".agents", "plugins")
		pluginRoot := filepath.Join(destination, "plugins", "api-plugin")
		if err := os.MkdirAll(filepath.Join(pluginRoot, ".codex-plugin"), 0o755); err != nil {
			return err
		}
		if err := os.MkdirAll(manifestRoot, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(manifestRoot, "api_marketplace.json"), []byte(`{"name":"openai-api-curated","plugins":[{"name":"api-plugin","source":{"source":"local","path":"./plugins/api-plugin"}}]}`), 0o600); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(pluginRoot, ".codex-plugin", "plugin.json"), []byte(`{"name":"api-plugin"}`), 0o600); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(pluginRoot, ".mcp.json"), []byte(`{"mcpServers":{"curated":{"type":"http","url":"`+mcpServer.URL+`"}}}`), 0o600)
	}))

	threadStatus := NewThreadStatusManager()
	threadStatus.UpsertThread("thread-existing", false)
	router := NewRuntimeRouter(RuntimeServices{
		Config:       config.NewConfigService(home),
		Account:      account,
		Plugins:      plugins,
		Skills:       NewSkillsService(nil),
		MCP:          mcp.NewMCPService(nil),
		ThreadStatus: threadStatus,
	})
	t.Cleanup(func() { _ = router.Close() })
	router.configureMCPFromConfig()
	initial := router.mcpServiceForThread("thread-existing", nil)
	if statuses := initial.ConfiguredStatuses(); len(statuses) != 0 {
		t.Fatalf("initial MCP statuses = %#v", statuses)
	}
	if !plugins.StartCuratedRepoSync(router.effectivePluginsChanged) {
		t.Fatal("curated repo sync did not start")
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		service := router.mcpServiceForThread("thread-existing", nil)
		statuses := service.ConfiguredStatuses()
		if len(statuses) == 1 && statuses[0].Name == "curated" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("existing thread did not receive curated MCP config: %#v", statuses)
		}
		time.Sleep(10 * time.Millisecond)
	}

	response := router.Handle(requestWithParams(t, IntID(1), MethodMCPServerToolCall, mcp.MCPToolCallParams{
		ThreadID: "thread-existing", Server: "curated", Tool: "echo", Arguments: map[string]any{"message": "available after sync"},
	}))
	if response.Error != nil {
		t.Fatalf("curated MCP tool call error = %+v", response.Error)
	}
	result := response.Result.(*mcp.MCPToolCallResponse)
	structured, ok := result.StructuredContent.(map[string]any)
	if !ok || structured["available"] != true {
		t.Fatalf("curated MCP tool result = %#v", result.StructuredContent)
	}
}
