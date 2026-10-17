package appserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/config"
	"codex_go/mcp"
	"codex_go/session"
	"codex_go/turn"
)

func TestEffectiveMCPConfigForThreadLoadsHostProjectConfigLikeRust(t *testing.T) {
	home := t.TempDir()
	hostCWD := t.TempDir()
	remoteCWD := t.TempDir()
	writeProjectConfig := func(cwd, model string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(cwd, ".gcode"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(config.ProjectConfigPath(cwd), []byte("model = \""+model+"\"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeProjectConfig(hostCWD, "host-project-model")
	writeProjectConfig(remoteCWD, "remote-project-model")
	trust := func(cwd string) string {
		return strings.ReplaceAll(filepath.Clean(cwd), `\`, `\\`)
	}
	configBody := "[projects.\"" + trust(hostCWD) + "\"]\ntrust_level = \"trusted\"\n" +
		"[projects.\"" + trust(remoteCWD) + "\"]\ntrust_level = \"trusted\"\n"
	if err := os.WriteFile(config.ConfigPath(home), []byte(configBody), 0o600); err != nil {
		t.Fatal(err)
	}
	store := session.NewStore(t.TempDir())
	if err := store.Save(&session.Record{
		ID: "thread-host-config", SessionID: "thread-host-config",
		Metadata: session.Metadata{CWD: hostCWD},
	}); err != nil {
		t.Fatal(err)
	}
	router := NewRuntimeRouter(RuntimeServices{
		Config:       config.NewConfigService(home),
		ThreadRouter: NewRouter(store),
		DefaultCWD:   hostCWD,
	})
	t.Cleanup(func() { _ = router.Close() })
	params := &turn.TurnStartParams{
		ThreadID: "thread-host-config",
		CWD:      remoteCWD,
		Environments: []map[string]any{
			{"environmentId": "remote", "cwd": remoteCWD},
			{"environmentId": "local", "cwd": hostCWD},
		},
	}
	if err := router.threads.RegisterTurn("thread-host-config", "turn-host-config", nil, 0, params); err != nil {
		t.Fatal(err)
	}
	cfg := router.effectiveMCPConfigForThread("thread-host-config")
	if cfg == nil || strings.TrimSpace(sharedStringValue(cfg.Values["model"])) != "host-project-model" {
		t.Fatalf("host MCP config model = %#v, want host-project-model", cfg)
	}
}

func sharedStringValue(value any) string {
	text, _ := value.(string)
	return text
}

func TestHostMCPPathsKeepRemotePrimaryLocalLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(t.TempDir())
	if err := store.Save(&session.Record{
		ID: "thread-host", SessionID: "thread-host",
		Metadata: session.Metadata{CWD: "/host/session"},
	}); err != nil {
		t.Fatal(err)
	}
	router := NewRuntimeRouter(RuntimeServices{
		Config:       config.NewConfigService(home),
		ThreadRouter: NewRouter(store),
		DefaultCWD:   "/host/default",
	})
	t.Cleanup(func() { _ = router.Close() })
	params := &turn.TurnStartParams{
		ThreadID: "thread-host",
		CWD:      "/remote/project",
		Environments: []map[string]any{
			{"environmentId": "remote", "cwd": "/remote/project"},
			{"environmentId": "local", "cwd": "/host/project", "workspaceRoots": []any{}},
		},
	}
	context := router.hostMCPPathContextForTurn("thread-host", params)
	if context.CWD != "/host/project" {
		t.Fatalf("host MCP cwd = %q, want /host/project", context.CWD)
	}
	if context.EnvironmentCWDs["remote"] != "/remote/project" {
		t.Fatalf("remote environment cwd = %#v", context.EnvironmentCWDs)
	}
	if _, ok := context.EnvironmentCWDs["local"]; ok {
		t.Fatalf("local environment must use the per-server cwd carrier: %#v", context.EnvironmentCWDs)
	}

	runtime := &mcp.RuntimeConfig{Servers: map[string]mcp.ServerRegistration{
		"relative": {
			Name: "relative",
			Config: mcp.ServerConfig{
				Command: "./server", Enabled: true,
			},
		},
		"explicit": {
			Name: "explicit",
			Config: mcp.ServerConfig{
				Command: "explicit-server", CWD: "/explicit", Enabled: true,
			},
		},
		"remote": {
			Name: "remote",
			Config: mcp.ServerConfig{
				Command: "remote-server", EnvironmentID: "remote", Enabled: true,
			},
		},
	}}
	applyHostMCPRuntimePaths(runtime, context)
	if runtime.Servers["relative"].Config.CWD != "/host/project" {
		t.Fatalf("relative server cwd = %q", runtime.Servers["relative"].Config.CWD)
	}
	if runtime.Servers["explicit"].Config.CWD != "/explicit" {
		t.Fatalf("explicit server cwd = %q", runtime.Servers["explicit"].Config.CWD)
	}
	if runtime.Servers["remote"].Config.CWD != "" {
		t.Fatalf("remote server inherited host cwd: %q", runtime.Servers["remote"].Config.CWD)
	}
}

func TestHostMCPPathsFallBackToSessionHostCWDLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(t.TempDir())
	if err := store.Save(&session.Record{
		ID: "thread-remote-only", SessionID: "thread-remote-only",
		Metadata: session.Metadata{CWD: "/host/session"},
	}); err != nil {
		t.Fatal(err)
	}
	router := NewRuntimeRouter(RuntimeServices{
		Config:       config.NewConfigService(home),
		ThreadRouter: NewRouter(store),
		DefaultCWD:   "/host/default",
	})
	t.Cleanup(func() { _ = router.Close() })
	context := router.hostMCPPathContextForTurn("thread-remote-only", &turn.TurnStartParams{
		ThreadID: "thread-remote-only",
		CWD:      "/remote/project",
		Environments: []map[string]any{
			{"environmentId": "remote", "cwd": "/remote/project"},
		},
	})
	if context.CWD != "/host/session" {
		t.Fatalf("fallback host cwd = %q, want /host/session", context.CWD)
	}
}

func TestMCPEnvironmentFingerprintTracksRuntimeWorkspaceRootsLikeRust(t *testing.T) {
	params := &turn.TurnStartParams{
		ThreadID: "thread-roots",
		Environments: []map[string]any{
			{"environmentId": "local", "cwd": "/host", "workspaceRoots": []any{"/host/a"}},
		},
	}
	before := mcpEnvironmentRuntimeFingerprint(params)
	params.Environments[0]["workspaceRoots"] = []any{"/host/b"}
	after := mcpEnvironmentRuntimeFingerprint(params)
	if before == "" || before == after {
		t.Fatalf("MCP environment fingerprint did not change with workspace roots: %q / %q", before, after)
	}
}
