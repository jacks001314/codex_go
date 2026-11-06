package app

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/cli"
	"codex_go/config"
	codexexec "codex_go/exec"
	"codex_go/protocol"
	codextui "codex_go/tui"
	codextea "codex_go/tui/tea"
)

// Rust #50148: the embedded session hosts a worktree-only MCP service so the
// in-process runner can call the managed worktree tools.
func TestEmbeddedWorktreeMCPHostsOnlyWorktreeToolsLikeRust(t *testing.T) {
	service := &managedWorktreeTools{now: time.Now, operations: map[string]*managedWorktreeOperation{}}
	server, err := startManagedWorktreeMCPServer(service)
	if err != nil {
		t.Fatalf("startManagedWorktreeMCPServer: %v", err)
	}
	defer server.close()

	config := server.configValues()["mcp_servers."+DynamicToolNamespace].(map[string]any)
	toolsConfig := config["tools"].(map[string]any)
	if _, ok := toolsConfig["create_worktree"]; !ok {
		t.Fatalf("worktree-only server config = %#v, want create_worktree", toolsConfig)
	}
	for _, taskTool := range []string{"create_thread", "send_message_to_thread", "fork_thread"} {
		if _, ok := toolsConfig[taskTool]; ok {
			t.Fatalf("worktree-only server config kept task tool %q", taskTool)
		}
	}

	listed := taskToolsMCPPost(t, config, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"})
	names := map[string]bool{}
	for _, raw := range listed["result"].(map[string]any)["tools"].([]any) {
		names[raw.(map[string]any)["name"].(string)] = true
	}
	if len(names) != 3 || !names["create_worktree"] || !names["get_worktree_creation_status"] || !names["list_worktrees"] {
		t.Fatalf("worktree-only tools/list = %v, want the three managed worktree tools", names)
	}

	// Task tools stay on the dynamic-tools callback transport.
	response := taskToolsMCPPost(t, config, map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "create_thread",
			"arguments": map[string]any{},
			"_meta":     map[string]any{"threadId": "thread-a"},
		},
	})
	errorPayload, ok := response["error"].(map[string]any)
	if !ok || !strings.Contains(errorPayload["message"].(string), "unknown tool") {
		t.Fatalf("task tool on the worktree-only server = %#v, want unknown tool", response)
	}
}

// The host config reaches the embedded runner as per-turn "-c" overrides.
func TestEmbeddedWorktreeMCPConfigOverridesLikeRust(t *testing.T) {
	service := &managedWorktreeTools{now: time.Now, operations: map[string]*managedWorktreeOperation{}}
	server, err := startManagedWorktreeMCPServer(service)
	if err != nil {
		t.Fatalf("startManagedWorktreeMCPServer: %v", err)
	}
	host := &taskToolsMCPHost{server: server, worktrees: service}
	restore := installEmbeddedWorktreeMCPHost(host)
	defer restore()

	overrides := embeddedWorktreeMCPConfigOverrides()
	joined := strings.Join(overrides, "\n")
	for _, want := range []string{
		"mcp_servers." + DynamicToolNamespace + ".url=http://127.0.0.1:",
		"mcp_servers." + DynamicToolNamespace + ".http_headers.Authorization=Bearer ",
		"mcp_servers." + DynamicToolNamespace + ".tools.create_worktree.approval_mode=approve",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("embedded overrides are missing %q:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "create_thread") {
		t.Fatalf("embedded overrides exposed task tools:\n%s", joined)
	}

	// The same strings must resolve through the CLI config override parser.
	parsed, err := config.ParseOverrides(overrides)
	if err != nil {
		t.Fatalf("ParseOverrides: %v", err)
	}
	values := map[string]any{}
	config.ApplyOverrides(values, parsed)
	servers, _ := values["mcp_servers"].(map[string]any)
	entry, _ := servers[DynamicToolNamespace].(map[string]any)
	if url, _ := entry["url"].(string); !strings.HasPrefix(url, "http://127.0.0.1:") {
		t.Fatalf("parsed mcp server = %#v, want a loopback URL", entry)
	}
	if headers, _ := entry["http_headers"].(map[string]any); headers["Authorization"] == nil {
		t.Fatalf("parsed mcp server lost the authorization header: %#v", entry)
	}
}

// A configured server that owns the TUI namespace keeps hosting off, and an
// explicit untrusted project never advertises the tools.
func TestEmbeddedWorktreeToolsEligibilityLikeRust(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	store := newSessionStore()
	state := codextui.NewState(nil)
	state.CWD = t.TempDir()

	if tools := interactiveEmbeddedWorktreeTools(&cli.RootOptions{}, state, store); tools == nil {
		t.Fatal("a trusted local session must advertise the worktree tools")
	}

	configPath := filepath.Join(os.Getenv("CODEX_HOME"), "config.toml")
	if err := os.WriteFile(configPath, []byte("[mcp_servers."+DynamicToolNamespace+"]\ncommand = \"owned\"\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if tools := interactiveEmbeddedWorktreeTools(&cli.RootOptions{}, state, store); tools != nil {
		t.Fatal("a configured namespace owner must block the hosted server")
	}
}

// A turn issued by the embedded TUI carries the hosted worktree MCP config.
func TestEmbeddedTurnCarriesWorktreeMCPOverridesLikeRust(t *testing.T) {
	service := &managedWorktreeTools{now: time.Now, operations: map[string]*managedWorktreeOperation{}}
	server, err := startManagedWorktreeMCPServer(service)
	if err != nil {
		t.Fatalf("startManagedWorktreeMCPServer: %v", err)
	}
	host := &taskToolsMCPHost{server: server, worktrees: service}
	restore := installEmbeddedWorktreeMCPHost(host)
	defer restore()

	var captured *codexexec.Request
	runner := interactiveTurnRunnerFunc(func(ctx context.Context, req *codexexec.Request, stdin io.Reader, stdout, stderr io.Writer) (*codexexec.Result, error) {
		captured = req
		_ = json.NewEncoder(stdout).Encode(protocol.ThreadStarted("thread-1"))
		return &codexexec.Result{ThreadID: "thread-1", LastMessage: "ok"}, nil
	})
	state := codextui.NewState(nil)
	message := interactiveTurnCommand(context.Background(), &cli.RootOptions{}, runner, state, "hello", nil, nil, nil)()
	started, ok := message.(codextea.StreamStartedMsg)
	if !ok {
		t.Fatalf("message = %T, want StreamStartedMsg", message)
	}
	_, _, _ = collectInteractiveStream(t, started)
	if captured == nil {
		t.Fatal("the embedded turn did not reach the runner")
	}
	joined := strings.Join(captured.Root.ConfigOverrides, "\n")
	if !strings.Contains(joined, "mcp_servers."+DynamicToolNamespace+".url=http://127.0.0.1:") {
		t.Fatalf("embedded turn lost the worktree MCP overrides:\n%s", joined)
	}
}
