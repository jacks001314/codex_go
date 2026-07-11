package parity

import (
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

func rustSnapshotRoot(t *testing.T) string {
	t.Helper()
	candidates := []string{}
	if env := os.Getenv("CODEX_RUST_ROOT"); env != "" {
		candidates = append(candidates, env)
	}
	candidates = append(candidates,
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
		"secrets",
		"exec",
		"file-system",
		"exec-server-protocol",
		"exec-server",
		"execpolicy",
		"execpolicy-legacy",
		"ext/connectors",
		"ext/extension-api",
		"ext/goal",
		"ext/guardian",
		"ext/image-generation",
		"ext/memories",
		"ext/mcp",
		"ext/skills",
		"ext/web-search",
		"external-agent-migration",
		"external-agent-sessions",
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
		"realtime-webrtc",
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
		{Path: "Cargo.toml", SHA256: "e859b1f2f69133fc725396a5fc838e8d320c101fef7ec82947dbeb6d7a20d7d6"},
		{Path: "cli/src/lib.rs", SHA256: "cf24032f801314033b2ff21e6c5a85ad8c2898012e9fba279f3a5c91439c1ab6"},
		{Path: "exec/src/lib.rs", SHA256: "2b10ba9ad799fdb1e8ea4207cb1f4ed0f2e5f57bc81fa5d2f5008f46cb24ab4d"},
		{Path: "exec/src/exec_events.rs", SHA256: "fa46724c54076701d04d29415f22398eb1e8c45abc60b54abd95fad0ecbfe201"},
		{Path: "prompts/templates/review/rubric.md", SHA256: "bc96ddff6a80d36620ea27cd3e864abffd875cc4ea65a55063b99da459ba4ae2"},
		{Path: "core/src/client.rs", SHA256: "e619a85307d07681c299c824a9925d650ba80b64b7c469d2a6ac8117d1d53b03"},
		{Path: "app-server-protocol/src/protocol/common.rs", SHA256: "76c8b215f491a0dfb4f9512a66cb0e6521f78aebf67b38f4714db1a28deba493"},
		{Path: "app-server/tests/suite/v2/mod.rs", SHA256: "c84b2fde4cadd82d0a26556259b1f35c553d4f70c19d8e8ed1f47ad6802c6ed0"},
		{Path: "core/tests/suite/mod.rs", SHA256: "d6fa0cbbf0a44a406c9da218cf735a664ed63a0041e603610ffe40785e3405a8"},
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
