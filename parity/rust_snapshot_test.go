package parity

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestRustWorkspaceMembersSnapshot(t *testing.T) {
	root := rustSnapshotRoot(t)
	got := rustWorkspaceMembers(t, filepath.Join(root, "Cargo.toml"))
	want := rustWorkspaceMembersSnapshot()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Rust workspace members snapshot drift; missing=%v unexpected=%v gotCount=%d wantCount=%d", missingStrings(want, got), missingStrings(got, want), len(got), len(want))
	}
}

func TestRustCriticalFileHashesSnapshot(t *testing.T) {
	root := rustSnapshotRoot(t)
	for _, entry := range rustCriticalFileHashSnapshot() {
		path := filepath.Join(root, filepath.FromSlash(entry.Path))
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", path, err)
		}
		sum := sha256.Sum256(data)
		got := hex.EncodeToString(sum[:])
		if got != entry.SHA256 {
			t.Fatalf("Rust critical file hash drift for %s: got %s want %s", entry.Path, got, entry.SHA256)
		}
	}
}

func TestPrecomputedAppServerExportsMatchRustTarget(t *testing.T) {
	rustRoot := rustSnapshotRoot(t)
	for _, name := range []string{
		"app-server-exports-stable.json.zst",
		"app-server-exports-experimental.json.zst",
	} {
		rustPath := filepath.Join(rustRoot, "app-server-protocol", "schema", "precomputed", name)
		goPath := filepath.Join("..", "appserver", "schema", "precomputed", name)
		rustData, err := os.ReadFile(rustPath)
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", rustPath, err)
		}
		goData, err := os.ReadFile(goPath)
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", goPath, err)
		}
		if !bytes.Equal(goData, rustData) {
			t.Fatalf("vendored precomputed export %s differs from Rust target", name)
		}
	}
}

func rustSnapshotRoot(t *testing.T) string {
	t.Helper()
	candidates := []string{}
	if env := os.Getenv("CODEX_RUST_ROOT"); env != "" {
		candidates = append(candidates, env)
	}
	candidates = append(candidates,
		filepath.Join("..", "..", "git", "codex", "codex-rs"),
		filepath.Join("..", "..", "..", "git", "codex", "codex-rs"),
		filepath.Join("..", "..", "..", "codex-main", "codex-rs"),
		filepath.Join("..", "codex-main", "codex-rs"),
	)
	for _, candidate := range candidates {
		abs, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		if _, err := os.Stat(filepath.Join(abs, "Cargo.toml")); err == nil {
			return abs
		}
	}
	t.Skip("Rust snapshot not found; set CODEX_RUST_ROOT")
	return ""
}

func rustWorkspaceMembers(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", path, err)
	}
	inMembers := false
	members := []string{}
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if !inMembers && strings.HasPrefix(trimmed, "members") && strings.Contains(trimmed, "[") {
			inMembers = true
			continue
		}
		if inMembers && strings.HasPrefix(trimmed, "]") {
			break
		}
		if !inMembers || !strings.HasPrefix(trimmed, `"`) {
			continue
		}
		value, err := strconv.Unquote(strings.TrimSuffix(trimmed, ","))
		if err != nil {
			t.Fatalf("invalid Rust workspace member line %q in %s: %v", line, path, err)
		}
		members = append(members, value)
	}
	if len(members) == 0 {
		t.Fatalf("no Rust workspace members found in %s", path)
	}
	return members
}

func rustWorkspaceMembersSnapshot() []string {
	return []string{
		"aws-auth",
		"analytics",
		"agent-graph-store",
		"agent-message-board-client",
		"agent-identity",
		"agent-roles",
		"backend-client",
		"bwrap",
		"build-info",
		"ansi-escape",
		"attachment-store",
		"async-utils",
		"app-server",
		"app-server-transport",
		"app-server-daemon",
		"app-server-client",
		"app-server-protocol",
		"app-server-protocol-noop-macros",
		"app-server-test-client",
		"apply-patch",
		"arg0",
		"feedback",
		"features",
		"install-context",
		"codex-backend-openapi-models",
		"code-mode",
		"code-mode-host",
		"code-mode-protocol",
		"code-mode-runtime",
		"codex-home",
		"cloud-config",
		"cloud-tasks",
		"cloud-tasks-client",
		"cloud-tasks-mock-client",
		"cli",
		"collaboration-mode-templates",
		"connectors",
		"config",
		"config-schema",
		"context-fragments",
		"shell-command",
		"shell-escalation",
		"skills",
		"core",
		"core-api",
		"core-plugin-common",
		"core-plugins",
		"diagnostics",
		"guardian-context",
		"hooks",
		"history",
		"http-client",
		"secrets",
		"exec",
		"file-system",
		"exec-server-protocol",
		"exec-server",
		"exec-server/tests/support",
		"execpolicy",
		"ext/agent",
		"ext/agent-message-board",
		"ext/connectors",
		"ext/extension-api",
		"ext/goal",
		"ext/git-attribution",
		"ext/guardian-reviewer",
		"ext/guardian-v2",
		"ext/history-notes",
		"ext/image-generation",
		"ext/items",
		"ext/memories",
		"ext/mcp",
		"ext/queue",
		"ext/skills",
		"ext/web-search",
		"external-agent-migration",
		"keyring-store",
		"file-search",
		"file-watcher",
		"linux-sandbox",
		"lmstudio",
		"login",
		"codex-mcp",
		"memories/read",
		"memories/write",
		"mermaid",
		"model-provider-info",
		"mxc-sandbox",
		"models-manager",
		"network-proxy",
		"ollama",
		"process-hardening",
		"realtime-webrtc",
		"voice-host",
		"protocol",
		"prompts",
		"rollout",
		"rollout-trace",
		"rmcp-client",
		"responses-api-proxy",
		"response-debug-context",
		"sandboxing",
		"stdio-to-uds",
		"otel",
		"otel-trace-websocket",
		"tcp-tunnel",
		"tui",
		"user-verification",
		"tools",
		"v8-poc",
		"websocket-auth",
		"websocket-client",
		"windows-sandbox-service",
		"worktree",
		"workload-identity",
		"utils/absolute-path",
		"utils/audio",
		"utils/path-uri",
		"utils/cargo-bin",
		"git-utils",
		"utils/cache",
		"utils/git-discovery",
		"utils/image",
		"utils/json-to-toml",
		"utils/home-dir",
		"utils/pty",
		"utils/readiness",
		"utils/redacted-string",
		"utils/rustls-provider",
		"utils/string",
		"utils/cli",
		"utils/elapsed",
		"utils/sandbox-summary",
		"utils/sleep-inhibitor",
		"utils/approval-presets",
		"utils/oss",
		"utils/output-truncation",
		"utils/path-utils",
		"utils/plugins",
		"utils/fuzzy-match",
		"utils/stream-parser",
		"utils/template",
		"codex-client",
		"codex-api",
		"state",
		"terminal-detection",
		"test-binary-support",
		"thread-manager-sample",
		"thread-store",
		"uds",
		"codex-experimental-api-macros",
		"plugin",
		"model-provider",
	}
}

type rustCriticalFileHash struct {
	Path   string
	SHA256 string
}

func rustCriticalFileHashSnapshot() []rustCriticalFileHash {
	return []rustCriticalFileHash{
		// Re-pinned to upstream e75b36efde (#48100): the
		// agent-message-board-client workspace member.
		{Path: "Cargo.toml", SHA256: "bafbeba3f6752808c1cac2902813ec767f4615d2913d092021d29d86a540f8f0"},
		{Path: "cli/src/lib.rs", SHA256: "9471ba0b4b388dfb339408fd54d78e3237573a8ce1e7bb83551f4ea1f35c0d7d"},
		// Re-pinned to upstream 8ae55c863d (#47411): the shared network policy
		// runs through embedded Codex startup.
		{Path: "exec/src/lib.rs", SHA256: "d37a7839dcf7e5b74a9dff32ee9030dd09a3ac290b105b8ae39ad15d449611cd"},
		// Re-pinned to upstream 7498521d (#46319): exec JSON web-search items
		// now carry the structured results array.
		{Path: "exec/src/exec_events.rs", SHA256: "2e9eb984f0de88bc3fbe7ea0d1017e39e93df108a7a3f9ffd9c66aa54e94a316"},
		{Path: "prompts/templates/review/rubric.md", SHA256: "56e3d0a5a4df3d670dc18b3b26f0525188fd4d81260a8676905a2573aa6d6dee"},
		// Re-pinned to upstream 8ae55c863d (#47745/#47758/#47957): the startup
		// prewarm, tool-observation and message-budget client changes.
		{Path: "core/src/client.rs", SHA256: "82608d33acccfc4ece91b912c2573d7b1bd91c157aee30b990590868a23cf927"},
		// Re-pinned to upstream 8ae55c863d (#47207/#47248/#47377/#47648): the
		// gateway OAuth, MCP resource-target, realtime reasoning-status and
		// executor bearer-token requests.
		{Path: "app-server-protocol/src/protocol/common.rs", SHA256: "d1aef98633f16cff20e2ef9836292a7bf1a4656d960e9ab1f29625596eefe168"},
		// Re-pinned to upstream 8ae55c863d (#46917/#47028/#47207/#47407): the
		// provider-requirements, message-board, gateway-auth and network-policy
		// suites joined the module list.
		{Path: "app-server/tests/suite/v2/mod.rs", SHA256: "6311cdf058b50df02f9e7d22278775caed30b26cea351c5545133caf746291a1"},
		// Re-pinned to upstream 8ae55c863d (#47407/#47679/#47819/#47820/#47879):
		// the network-policy, extension-hook, guardian-authorization,
		// agent-controller and macOS patch-permission suites joined the module
		// list.
		{Path: "core/tests/suite/mod.rs", SHA256: "c045457c7477057874bee427856f01d2a0eb5b5b2247515baff94e4203e662b7"},
	}
}

func missingStrings(want []string, got []string) []string {
	present := make(map[string]bool, len(got))
	for _, value := range got {
		present[value] = true
	}
	missing := []string{}
	for _, value := range want {
		if !present[value] {
			missing = append(missing, value)
		}
	}
	return missing
}
