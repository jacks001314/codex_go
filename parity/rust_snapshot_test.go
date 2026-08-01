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
		"agent-identity",
		"backend-client",
		"bwrap",
		"ansi-escape",
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
		"context-fragments",
		"shell-command",
		"shell-escalation",
		"skills",
		"core",
		"core-api",
		"core-plugins",
		"core-skills",
		"hooks",
		"http-client",
		"secrets",
		"exec",
		"file-system",
		"exec-server-protocol",
		"exec-server",
		"exec-server/tests/support",
		"execpolicy",
		"ext/agent",
		"ext/connectors",
		"ext/extension-api",
		"ext/goal",
		"ext/git-attribution",
		"ext/guardian",
		"ext/image-generation",
		"ext/items",
		"ext/memories",
		"ext/mcp",
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
		"mcp-server",
		"memories/read",
		"memories/write",
		"model-provider-info",
		"models-manager",
		"network-proxy",
		"ollama",
		"process-hardening",
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
		"tui",
		"tools",
		"v8-poc",
		"websocket-client",
		"utils/absolute-path",
		"utils/path-uri",
		"utils/cargo-bin",
		"git-utils",
		"utils/cache",
		"utils/image",
		"utils/json-to-toml",
		"utils/home-dir",
		"utils/pty",
		"utils/readiness",
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
		{Path: "Cargo.toml", SHA256: "ca75d0f70dc20ea4af7e55e17c691309b21414dfdac94a1477bef78da2483476"},
		{Path: "cli/src/lib.rs", SHA256: "9471ba0b4b388dfb339408fd54d78e3237573a8ce1e7bb83551f4ea1f35c0d7d"},
		{Path: "exec/src/lib.rs", SHA256: "ff90d8f906d07573e83b79ae40369fc5b49984fa9230bd3d13b05e1635a3c4c4"},
		{Path: "exec/src/exec_events.rs", SHA256: "fc914a7d8f7e990b19a95c41abf758e95e5b7ea028caa8b34b1c82306382c004"},
		{Path: "prompts/templates/review/rubric.md", SHA256: "56e3d0a5a4df3d670dc18b3b26f0525188fd4d81260a8676905a2573aa6d6dee"},
		{Path: "core/src/client.rs", SHA256: "3d6a5ffadd4a942d1ac40a85c5651854ebea2c538f389314c367ddcd995a2881"},
		{Path: "app-server-protocol/src/protocol/common.rs", SHA256: "17a7dc8de4e41220664777d23a05e57198bf0cd67e5c1021a3b3a822dacc5170"},
		{Path: "app-server/tests/suite/v2/mod.rs", SHA256: "058ce7b2232c415e3fc53458518487e4982040e906b8aa731b9c18ac79ac5cba"},
		{Path: "core/tests/suite/mod.rs", SHA256: "f2b8f51fe52f0e7cc2526dae577af5d146374a2d64f141f8d6045f4331bfd1ae"},
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
