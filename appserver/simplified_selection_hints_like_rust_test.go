package appserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/config"
	"codex_go/mcp"
	"codex_go/plugin"
	"codex_go/session"
	"codex_go/turn"
)

func TestRuntimeRouterSimplifiedPluginSelectionHintsLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(config.ConfigPath(home)), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(config.ConfigPath(home), []byte("[features]\nsimplified_selection_hints = true\n"), 0o600); err != nil {
		t.Fatalf("write config error = %v", err)
	}
	store := session.NewStore(t.TempDir())
	sink := NewNotificationBuffer()
	agent := newRecordingRuntimeAgent("ok")
	plugins := plugin.NewPluginService()
	plugins.AddPlugin(plugin.PluginDetail{Summary: plugin.PluginSummary{
		Name:            "docs",
		MarketplaceName: "local",
		DisplayName:     "Docs",
		Installed:       true,
		Enabled:         true,
		HasSkills:       true,
		MCPServers:      []string{"docs-mcp"},
	}})
	mcpService := mcp.NewMCPService(nil)
	mcpService.SetServer(mcp.MCPServerStatus{
		Name:  "docs-mcp",
		State: mcp.MCPServerReady,
		Server: mcp.MCPServerInfo{
			Name: "docs-mcp",
		},
		Tools: []mcp.MCPToolInfo{{Name: "search"}},
	})
	router := NewRuntimeRouter(RuntimeServices{
		Config:       config.NewConfigService(home),
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		Plugins:      plugins,
		MCP:          mcpService,
		ThreadStatus: NewThreadStatusManager(),
	})
	defer router.Close()
	router.SetNotificationSink(sink)

	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:    t.TempDir(),
		Prompt: "hello plugin",
	}))
	if threadStart.Error != nil {
		t.Fatalf("thread start error: %+v", threadStart.Error)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID
	turnStart := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Input: []turn.TurnUserInput{{
			Type: "mention",
			Path: "plugin://docs@local",
		}, {
			Type: "text",
			Text: "use this plugin",
		}},
	}))
	if turnStart.Error != nil {
		t.Fatalf("turn start error: %+v", turnStart.Error)
	}
	request := waitForRuntimeAgentRequest(t, agent)
	const hint = "The user selected the `Docs` plugin. Use its available skills and tools to complete this request."
	if !strings.Contains(request.Instructions, hint) {
		t.Fatalf("instructions missing the simplified plugin hint:\n%s", request.Instructions)
	}
	if strings.Contains(request.Instructions, "Capabilities from the `Docs` plugin:") {
		t.Fatalf("instructions kept the detailed plugin hint:\n%s", request.Instructions)
	}
	turnID := turnStart.Result.(*turn.TurnStartResponse).Turn.ID
	waitForTurnCompletedStatus(t, sink, turnID, TurnStatusCompleted)
}
