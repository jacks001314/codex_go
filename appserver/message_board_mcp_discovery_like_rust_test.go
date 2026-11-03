package appserver

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/mcp"
	"codex_go/session"
	"codex_go/turn"
)

// Rust #58973: enabling agent_message_board_mcp starts a temporary tree-scoped
// HTTP MCP board and exposes it through the ordinary MCP configuration, while
// the direct board tools stay off.
func TestMessageBoardMCPDiscoveryInjectsServerConfigLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(filepath.Join(home, "sessions"))
	rootID := "root-thread"
	if err := store.Save(&session.Record{
		ID: session.ThreadID(rootID), SessionID: rootID,
		CreatedAt: time.Unix(1, 0).UTC(), UpdatedAt: time.Unix(1, 0).UTC(),
		Metadata: session.Metadata{CWD: home, AgentPath: "/root"},
	}); err != nil {
		t.Fatalf("save root record: %v", err)
	}
	cfg := &config.Config{Values: map[string]any{"features": map[string]any{
		"multi_agent_v2":          true,
		"agent_message_board":     true,
		"agent_message_board_mcp": true,
	}}}
	if messageBoardFeatureEnabled(cfg) {
		t.Fatal("the direct board stayed enabled beside the MCP board")
	}
	if !messageBoardMCPEnabled(cfg) {
		t.Fatal("the MCP board was not enabled")
	}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
		Turns:        turn.NewTurnService(),
		ThreadStatus: NewThreadStatusManager(),
	})
	defer router.Close()
	service, managed := router.managedMCPServiceForThread(rootID, cfg)
	if !managed || service == nil {
		t.Fatalf("managedMCPServiceForThread() = %#v, %v", service, managed)
	}
	server, ok := service.ServerConfigForServer(messageBoardMCPServerName)
	if !ok {
		t.Fatal("the temporary MCP board was not registered")
	}
	if !strings.HasPrefix(server.URL, "http://127.0.0.1:") || !strings.HasSuffix(server.URL, "/mcp") {
		t.Fatalf("MCP board URL = %q", server.URL)
	}
	if !strings.HasPrefix(server.HTTPHeaders["Authorization"], "Bearer ") {
		t.Fatalf("MCP board authorization = %q", server.HTTPHeaders["Authorization"])
	}
	if !server.Required || !server.Enabled || server.SupportsParallelToolCalls {
		t.Fatalf("MCP board config = %#v", server)
	}
	if server.DefaultToolsApprovalMode == nil || *server.DefaultToolsApprovalMode != "approve" {
		t.Fatalf("MCP board approval mode = %#v", server.DefaultToolsApprovalMode)
	}
	if router.messageBoardMCP.TreeCount() != 1 {
		t.Fatalf("tree endpoints = %d", router.messageBoardMCP.TreeCount())
	}
	statuses, err := service.ListStatusChecked(&mcp.MCPListServerStatusParams{ThreadID: &rootID})
	if err != nil {
		t.Fatalf("ListStatusChecked() error = %v", err)
	}
	var boardStatus *mcp.MCPServerStatus
	for index := range statuses.Data {
		if statuses.Data[index].Name == messageBoardMCPServerName {
			boardStatus = &statuses.Data[index]
			break
		}
	}
	if boardStatus == nil || boardStatus.State != mcp.MCPServerReady || len(boardStatus.Tools) == 0 {
		t.Fatalf("MCP board status = %#v", boardStatus)
	}
	router.closeMessageBoardHandle(rootID)
	if router.messageBoardMCP.TreeCount() != 0 {
		t.Fatalf("tree endpoint survived the last thread: %d", router.messageBoardMCP.TreeCount())
	}
}
