package mcp

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// seedWarmToolCatalog installs a previously published catalog on a server that
// has not connected in this service, standing in for the warm cache a returning
// chat sees. The service only defers startup when such a catalog is usable.
func seedWarmToolCatalog(t *testing.T, service *MCPService, name string, tool string) {
	t.Helper()
	service.mu.Lock()
	defer service.mu.Unlock()
	status := service.servers[name]
	status.State = MCPServerReady
	status.Tools = []MCPToolInfo{{
		Name:        tool,
		Description: "cached",
		InputSchema: map[string]any{"type": "object"},
	}}
	service.servers[name] = status
}

func newLazyStdioService(executable string, logPath string, lazy bool, required bool) *MCPService {
	env := map[string]string{
		mcpSharingHelperEnv:     "1",
		mcpSharingHelperModeEnv: "shared",
		mcpSharingHelperLogEnv:  logPath,
	}
	return NewMCPService(&RuntimeConfig{
		DiscoverLocalStdio: true,
		LazyStdioStartup:   lazy,
		Servers: map[string]ServerRegistration{
			"shared": {
				Name: "shared",
				Config: ServerConfig{
					Enabled:        true,
					Required:       required,
					Command:        executable,
					Args:           []string{"-test.run=^TestMCPConnectionSharingHelperProcess$", "--"},
					StartupTimeout: 10 * time.Second,
					Env:            env,
				},
			},
		},
	})
}

// TestMcpLazyStdioStartupDefersCachedServersLikeRust covers Rust #60039's opt-in
// lazy startup: with `mcp_lazy_stdio_startup` a regular chat satisfies startup
// for an eligible stdio server from its warm catalog and does not launch the
// helper until first use. Required live startup stays eager, which is the half
// of the upstream rule that keeps a required server from being skipped.
func TestMcpLazyStdioStartupDefersCachedServersLikeRust(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("Executable() error = %v", err)
	}

	t.Run("feature defers an optional cached stdio server", func(t *testing.T) {
		logPath := filepath.Join(t.TempDir(), "lazy-starts")
		service := newLazyStdioService(executable, logPath, true, false)
		defer func() { _ = service.Close() }()
		seedWarmToolCatalog(t, service, "shared", "ask")

		response, err := service.ListStatusChecked(&MCPListServerStatusParams{})
		if err != nil {
			t.Fatalf("ListStatusChecked() error = %v", err)
		}
		if starts := mcpSharingLogCount(t, logPath, "start"); starts != 0 {
			t.Fatalf("helper starts = %d, want the cached server to stay dormant", starts)
		}
		status := statusForServer(t, response, "shared")
		if status.State != MCPServerReady {
			t.Fatalf("state = %q, want ready from the cached catalog", status.State)
		}
		if len(status.Tools) != 1 || status.Tools[0].Name != "ask" {
			t.Fatalf("tools = %#v, want the cached catalog", status.Tools)
		}

		// First use is what connects: the deferred server has no live client
		// until a call needs one.
		config, _ := service.serverConfig("shared")
		client := service.stdioClientForServer("shared", &config)
		if got := mcpSharingAsk(t, client, "first-use"); got != "first-use" {
			t.Fatalf("first call answer = %q", got)
		}
		if starts := mcpSharingLogCount(t, logPath, "start"); starts == 0 {
			t.Fatal("first use did not start the deferred helper")
		}
	})

	t.Run("feature off keeps the eager startup", func(t *testing.T) {
		logPath := filepath.Join(t.TempDir(), "eager-starts")
		service := newLazyStdioService(executable, logPath, false, false)
		defer func() { _ = service.Close() }()
		seedWarmToolCatalog(t, service, "shared", "ask")

		if _, err := service.ListStatusChecked(&MCPListServerStatusParams{}); err != nil {
			t.Fatalf("ListStatusChecked() error = %v", err)
		}
		if starts := mcpSharingLogCount(t, logPath, "start"); starts == 0 {
			t.Fatal("without the feature the status read must start the helper")
		}
	})

	t.Run("required live startup stays eager", func(t *testing.T) {
		logPath := filepath.Join(t.TempDir(), "required-starts")
		service := newLazyStdioService(executable, logPath, true, true)
		defer func() { _ = service.Close() }()
		seedWarmToolCatalog(t, service, "shared", "ask")

		if _, err := service.ListStatusChecked(&MCPListServerStatusParams{}); err != nil {
			t.Fatalf("ListStatusChecked() error = %v", err)
		}
		if starts := mcpSharingLogCount(t, logPath, "start"); starts == 0 {
			t.Fatal("a required server must start live even with lazy startup enabled")
		}
	})
}

// TestDeferredStartupKeepsDiscoverLocalStdioSharingLikeRust covers Rust #60037:
// deferring a cached stdio server must not downgrade its protocol mode to the
// legacy handshake, so the first use still negotiates connection sharing and two
// chats end up on the same helper process.
func TestDeferredStartupKeepsDiscoverLocalStdioSharingLikeRust(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("Executable() error = %v", err)
	}
	logPath := filepath.Join(t.TempDir(), "deferred-sharing")
	chatA := newLazyStdioService(executable, logPath, true, false)
	defer func() { _ = chatA.Close() }()
	chatB := newLazyStdioService(executable, logPath, true, false)
	defer func() { _ = chatB.Close() }()
	seedWarmToolCatalog(t, chatA, "shared", "ask")
	seedWarmToolCatalog(t, chatB, "shared", "ask")

	for _, chat := range []*MCPService{chatA, chatB} {
		config, ok := chat.serverConfig("shared")
		if !ok {
			t.Fatal("server is not configured")
		}
		if config.ProtocolMode != MCPProtocolDiscoverLocalStdio {
			t.Fatalf("protocol mode = %d, want discovery preserved while deferred", config.ProtocolMode)
		}
	}
	if _, err := chatA.ListStatusChecked(&MCPListServerStatusParams{}); err != nil {
		t.Fatalf("ListStatusChecked() error = %v", err)
	}
	if _, err := chatB.ListStatusChecked(&MCPListServerStatusParams{}); err != nil {
		t.Fatalf("ListStatusChecked() error = %v", err)
	}
	if starts := mcpSharingLogCount(t, logPath, "start"); starts != 0 {
		t.Fatalf("helper starts while dormant = %d, want 0", starts)
	}

	// First use negotiates the modern protocol and starts exactly one helper.
	if err := chatA.WaitForServerStartup(context.Background(), "shared", ""); err != nil {
		t.Fatalf("chat A WaitForServerStartup() error = %v", err)
	}
	configA, _ := chatA.serverConfig("shared")
	clientA := chatA.stdioClientForServer("shared", &configA)
	if got := mcpSharingAsk(t, clientA, "chat-a"); got != "chat-a" {
		t.Fatalf("chat A answer = %q", got)
	}
	if err := chatB.WaitForServerStartup(context.Background(), "shared", ""); err != nil {
		t.Fatalf("chat B WaitForServerStartup() error = %v", err)
	}
	configB, _ := chatB.serverConfig("shared")
	clientB := chatB.stdioClientForServer("shared", &configB)
	if clientA != clientB {
		t.Fatal("deferred chats did not share one negotiated stdio connection")
	}
	if starts := mcpSharingLogCount(t, logPath, "start"); starts != 1 {
		t.Fatalf("helper starts = %d, want one shared process", starts)
	}
}

func statusForServer(t *testing.T, response *MCPListServerStatusResponse, name string) MCPServerStatus {
	t.Helper()
	if response == nil {
		t.Fatal("nil status response")
	}
	for _, status := range response.Servers {
		if status.effectiveName() == name {
			return status
		}
	}
	t.Fatalf("server %q missing from %#v", name, response.Servers)
	return MCPServerStatus{}
}
