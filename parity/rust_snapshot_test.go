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

// TestPrecomputedAppServerExportsMatchRustTarget byte-compares the vendored
// app-server protocol exports against the Rust tree.
//
// Re-pinned to upstream head a6baf8867c (#51611): the two `.zst` artifacts in
// appserver/schema/precomputed/ now come from the Rust tree at the current
// head instead of the 18e28fe1b9 (#51556) pin. The experimental artifact was
// last regenerated at 1fbe15c962 (#51595) and the stable artifact at
// a6baf8867c (#51611, EventMsg::ElicitationAbandoned).
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
		"cloud-client",
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
		"windows-sandbox-rs/tests/support",
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
		"utils/process",
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
		// Re-pinned to upstream 18e28fe1b9 (#51556): every hash below is
		// recomputed from the current Rust tree so the critical-file drift check
		// tracks the protocol, exec, client and suite surfaces the Go port
		// mirrors after the 498-commit gap from the 5f3180c793 pin.
		// Re-pinned to upstream e75b36efde (#48100): the
		// agent-message-board-client workspace member.
		{Path: "Cargo.toml", SHA256: "26a713b6714637e008a30b0bf17fb03f1111c09f3d944857e2c4c1b050a20779"},
		{Path: "cli/src/lib.rs", SHA256: "cf24032f801314033b2ff21e6c5a85ad8c2898012e9fba279f3a5c91439c1ab6"},
		// Re-pinned to upstream 8ae55c863d (#47411): the shared network policy
		// runs through embedded Codex startup.
		// Re-pinned to upstream 1fbe15c962 (#51595): `thread/list` gained its
		// excluded thread ids, the newest writer to exec/src/lib.rs after the
		// 5a3140176e reference point.
		{Path: "exec/src/lib.rs", SHA256: "8f53a22a562e913f652f971c9955b987c1966e7facc0940aa1aab4d5ab5c4ab9"},
		// Re-pinned to upstream 7498521d (#46319): exec JSON web-search items
		// now carry the structured results array.
		{Path: "exec/src/exec_events.rs", SHA256: "dafa872d7e86a099e56e28a329dcb9c03db90ed768c3b88cca8c91d46dc1d0e5"},
		{Path: "prompts/templates/review/rubric.md", SHA256: "ec60e7f36a1d1c2679ce095c0205ecc56f7dd8fb57707a13ef362072390f219f"},
		// Re-pinned to upstream 8ae55c863d (#47745/#47758/#47957): the startup
		// prewarm, tool-observation and message-budget client changes.
		// Re-pinned to upstream c7e80f873f (#48141): preempting model responses
		// when new user input arrives.
		// Re-pinned to upstream e72da2b538 (#48344): the runtime-only
		// `include_internal_metadata` provider grant for tool metadata.
		// Re-pinned to upstream 12de0e395d (#48508): the interrupt seam that
		// preserves WebSocket continuations when steering a turn.
		{Path: "core/src/client.rs", SHA256: "6ca16eb5fb185192738d321c22eacf74664e0ae2c107176c1c8a7f23fde33089"},
		// Re-pinned to upstream 8ae55c863d (#47207/#47248/#47377/#47648): the
		// gateway OAuth, MCP resource-target, realtime reasoning-status and
		// executor bearer-token requests.
		{Path: "app-server-protocol/src/protocol/common.rs", SHA256: "e25ebb90bf79be79def79ebf2dd704057577fa9806710fbea0ee240a909d0dc1"},
		// Re-pinned to upstream 8ae55c863d (#46917/#47028/#47207/#47407): the
		// provider-requirements, message-board, gateway-auth and network-policy
		// suites joined the module list.
		{Path: "app-server/tests/suite/v2/mod.rs", SHA256: "8a9ec914063f50d62f16e71b7648943948314e4b169ec83d0a23e53d811ac0dc"},
		// Re-pinned to upstream 8ae55c863d (#47407/#47679/#47819/#47820/#47879):
		// the network-policy, extension-hook, guardian-authorization,
		// agent-controller and macOS patch-permission suites joined the module
		// list.
		{Path: "core/tests/suite/mod.rs", SHA256: "62853612f780dde84b877b3263e50a384a9e753dc03854773c4daaf5e9119e80"},
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
