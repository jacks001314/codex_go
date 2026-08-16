package parity

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func TestRustTopLevelDirectoryInventory(t *testing.T) {
	root := rustSnapshotRoot(t)
	got := rustTopLevelDirectories(t, root)
	want := rustTopLevelDirectoriesSnapshot()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Rust top-level directory inventory drift; missing=%v unexpected=%v gotCount=%d wantCount=%d", missingStrings(want, got), missingStrings(got, want), len(got), len(want))
	}
}

func TestGoTopLevelDirectoryInventory(t *testing.T) {
	got := goTopLevelDirectories(t, filepath.Join(".."))
	want := goTopLevelDirectoriesSnapshot()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Go top-level directory inventory drift; missing=%v unexpected=%v gotCount=%d wantCount=%d", missingStrings(want, got), missingStrings(got, want), len(got), len(want))
	}
}

func rustTopLevelDirectories(t *testing.T, root string) []string {
	t.Helper()
	dirs := collectTopLevelDirectories(t, root)
	out := dirs[:0]
	for _, dir := range dirs {
		if dir != "target" {
			out = append(out, dir)
		}
	}
	return out
}

func goTopLevelDirectories(t *testing.T, root string) []string {
	t.Helper()
	dirs := collectTopLevelDirectories(t, root)
	ignored := map[string]bool{
		"bin":          true,
		"cmd":          true,
		"deliverables": true,
		"docs":         true,
		"dist":         true,
		"npm":          true,
		"scripts":      true,
		"sdktests":     true,
		"tmp":          true,
		"update":       true,
		"vscodetests":  true,
	}
	packages := dirs[:0]
	for _, dir := range dirs {
		if dir[0] == '.' || ignored[dir] {
			continue
		}
		packages = append(packages, dir)
	}
	return packages
}

func collectTopLevelDirectories(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("ReadDir(%s) error = %v", root, err)
	}
	dirs := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dirs = append(dirs, entry.Name())
	}
	sort.Strings(dirs)
	return dirs
}

func rustTopLevelDirectoriesSnapshot() []string {
	return []string{
		".cargo",
		".config",
		"agent-graph-store",
		"agent-identity",
		"analytics",
		"ansi-escape",
		"app-server",
		"app-server-client",
		"app-server-daemon",
		"app-server-protocol",
		"app-server-protocol-noop-macros",
		"app-server-test-client",
		"app-server-transport",
		"apply-patch",
		"arg0",
		"async-utils",
		"aws-auth",
		"backend-client",
		"build-info",
		"bwrap",
		"chatgpt",
		"cli",
		"cloud-config",
		"cloud-tasks",
		"cloud-tasks-client",
		"cloud-tasks-mock-client",
		"code-mode",
		"code-mode-host",
		"code-mode-protocol",
		"code-mode-runtime",
		"codex-api",
		"codex-backend-openapi-models",
		"codex-client",
		"codex-experimental-api-macros",
		"codex-home",
		"codex-mcp",
		"collaboration-mode-templates",
		"config",
		"connectors",
		"context-fragments",
		"core",
		"core-api",
		"core-plugins",
		"diagnostics",
		"docs",
		"exec",
		"exec-server",
		"exec-server-protocol",
		"execpolicy",
		"ext",
		"external-agent-migration",
		"features",
		"feedback",
		"file-search",
		"file-system",
		"file-watcher",
		"git-utils",
		"history",
		"hooks",
		"http-client",
		"install-context",
		"keyring-store",
		"linux-sandbox",
		"lmstudio",
		"login",
		"mcp-server",
		"memories",
		"message-history",
		"model-provider",
		"model-provider-info",
		"models-manager",
		"network-proxy",
		"ollama",
		"otel",
		"plugin",
		"process-hardening",
		"prompts",
		"protocol",
		"response-debug-context",
		"responses-api-proxy",
		"rmcp-client",
		"rollout",
		"rollout-trace",
		"sandboxing",
		"scripts",
		"secrets",
		"shell-command",
		"shell-escalation",
		"skills",
		"state",
		"stdio-to-uds",
		"terminal-detection",
		"test-binary-support",
		"thread-manager-sample",
		"thread-store",
		"tools",
		"tui",
		"uds",
		"utils",
		"v8-poc",
		"vendor",
		"websocket-client",
		"windows-sandbox-rs",
		"workload-identity",
	}
}

func goTopLevelDirectoriesSnapshot() []string {
	return []string{
		"agent",
		"app",
		"applypatch",
		"apps",
		"appserver",
		"appserverdaemon",
		"auth",
		"chatgptapi",
		"cli",
		"codemode",
		"codexapi",
		"compact",
		"config",
		"context",
		"doctor",
		"envutil",
		"eventmap",
		"exec",
		"execpolicy",
		"execserver",
		"features",
		"filesearch",
		"gitutil",
		"history",
		"install",
		"mcp",
		"memories",
		"model",
		"network",
		"parity",
		"plugin",
		"prompt",
		"protocol",
		"realtime",
		"recordreplay",
		"remotecontrol",
		"review",
		"rollout",
		"runtimeutil",
		"safety",
		"sandbox",
		"session",
		"shell",
		"skillprovider",
		"state",
		"systemskills",
		"telemetry",
		"tool",
		"tui",
		"turn",
		"utils",
	}
}
