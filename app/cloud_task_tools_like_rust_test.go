package app

import (
	"testing"

	"codex_go/appserverdaemon"
	"codex_go/cli"
	codextui "codex_go/tui"
)

// Rust #57267 (codex-rs/tui/src/app_server_session.rs:557-565): a cloud
// conversation returns from start_dynamic_tool_mcp before hosting the client's
// local tool MCP server or worktree tools, even on a local-daemon endpoint.
func TestCloudSkipsLocalTaskToolsHostLikeRust(t *testing.T) {
	t.Setenv("GCODE_HOME", "")
	t.Setenv("CODEX_HOME", t.TempDir())
	calls := 0
	host := remoteTUITaskToolsHostForSession(
		ThreadParamsModeCloud,
		appserverdaemon.NewWebSocketEndpoint("ws://127.0.0.1:4500/rpc", nil),
		&cli.RootOptions{},
		codextui.NewState(nil),
		func() *taskToolsMCPHost {
			calls++
			return &taskToolsMCPHost{}
		},
	)
	if host != nil {
		t.Fatalf("cloud session built a local task-tools host: %#v", host)
	}
	if calls != 0 {
		t.Fatalf("cloud session constructed %d local hosts, want 0", calls)
	}
}

// A local-daemon client-owned session still hosts its local tool MCP server.
func TestEmbeddedHostsLocalTaskToolsLikeRust(t *testing.T) {
	t.Setenv("GCODE_HOME", "")
	t.Setenv("CODEX_HOME", t.TempDir())
	calls := 0
	host := remoteTUITaskToolsHostForSession(
		ThreadParamsModeEmbedded,
		appserverdaemon.NewWebSocketEndpoint("ws://127.0.0.1:4500/rpc", nil),
		&cli.RootOptions{},
		codextui.NewState(nil),
		func() *taskToolsMCPHost {
			calls++
			return &taskToolsMCPHost{}
		},
	)
	if host == nil {
		t.Fatal("embedded local session did not host its task tools")
	}
	if calls != 1 {
		t.Fatalf("embedded session constructed %d local hosts, want 1", calls)
	}
}

// A remote workspace keeps the app-server dynamic-tools callback transport.
func TestRemoteKeepsCallbackToolTransportLikeRust(t *testing.T) {
	t.Setenv("GCODE_HOME", "")
	t.Setenv("CODEX_HOME", t.TempDir())
	calls := 0
	host := remoteTUITaskToolsHostForSession(
		ThreadParamsModeRemote,
		appserverdaemon.NewWebSocketEndpoint("wss://example.com/rpc", nil),
		&cli.RootOptions{},
		codextui.NewState(nil),
		func() *taskToolsMCPHost {
			calls++
			return &taskToolsMCPHost{}
		},
	)
	if host != nil || calls != 0 {
		t.Fatalf("remote session host = %#v (constructed %d), want the callback transport", host, calls)
	}
}
