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
		// Upstream embeds these files with LF; a Windows checkout of the Rust
		// tree may hand us CRLF, so normalize before hashing to keep the pins
		// platform independent (blob digests).
		sum := sha256.Sum256(bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n")))
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
		"plugin-host",
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
		"skills",
		"core",
		"core-api",
		"core-plugin-common",
		"core-plugins",
		"diagnostics",
		"direct-tool-timing",
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
		"ext/multi-env-copy-file",
		"ext/goal",
		"ext/git-attribution",
		"ext/guardian-reviewer",
		"ext/guardian-v2",
		"ext/guardian-v3",
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
		"message-board",
		"model-provider-info",
		"mxc-sandbox",
		"models-manager",
		"network-proxy",
		"ollama",
		"process-hardening",
		"realtime-webrtc",
		"voice-host",
		"protocol",
		"protocol/tests/support",
		"prompts",
		"rollout",
		"rollout-trace",
		"rmcp-client",
		"responses-api-proxy",
		"runtime-agent-instance",
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
		"utils/critical-path",
		"utils/git-discovery",
		"utils/head-tail-buffer",
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
		// Re-anchored to upstream head 5b0b253035 (round-86 snapshot): all nine
		// hashes below were recomputed from `git -C <rust> cat-file blob
		// 5b0b253035:codex-rs/<path>`, so the pins are blob (LF) digests of the
		// upstream head rather than of a working tree that may be CRLF-converted
		// by core.autocrlf on Windows.
		// Round-89 re-anchor to head 95ec468619: the checkout advanced with the
		// code_mode_tool_description_first registration (#51690); only
		// core/tests/suite/mod.rs changed, and its pin is updated below.
		// Round-90 re-anchor to head 4eb4a65407: the checkout advanced across the
		// 338-commit #54590 delta; seven of the nine pins below changed and the
		// workspace lost the shell-escalation crate (#52160).
		// Previously re-pinned to upstream 18e28fe1b9 (#51556): every hash below
		// was recomputed from the then-current Rust tree so the critical-file
		// drift check tracks the protocol, exec, client and suite surfaces the
		// Go port mirrors after the 498-commit gap from the 5f3180c793 pin.
		// Re-pinned to upstream e75b36efde (#48100): the
		// agent-message-board-client workspace member.
		// Round-91 re-anchor to head e16b631d08: Cargo.toml gained the
		// ext/multi-env-copy-file workspace member (#54674).
		// Round-106 re-anchor to head 3b8f43a06b: the zip dependency moved to
		// 9.0.0 (#54761); every other pin below is unchanged at that head.
		// Round-108 re-anchor to head ce0323f2f2 (#54776): Cargo.toml gained the
		// protocol/tests/support workspace member; every other pin is unchanged.
		// Round-111 re-anchor to head 091008729d: the workspace gained
		// runtime-agent-instance.
		// Re-anchored to upstream 4d3ed3f3b4: Cargo.toml gained the
		// plugin-host member, and the remaining pins below track the protocol,
		// client and suite surfaces after the intervening sync windows
		// (accountSession/list and accountSession/switch in
		// app-server-protocol/src/protocol/common.rs).
		{Path: "Cargo.toml", SHA256: "80b5d1de8eb3431b16e97fad56c4e0dd4c03b247026e6cb976d031acdb83f0bc"},
		{Path: "cli/src/lib.rs", SHA256: "bc73cee3f5714a1ab90ca8853bbaa74f16dc5abd6edc6bb888d8648b6c49f14a"},
		// Re-pinned to upstream 8ae55c863d (#47411): the shared network policy
		// runs through embedded Codex startup.
		// Re-anchored to head 5b0b253035 via 1fbe15c962 (#51595): the two
		// ThreadListParams literals in resolve_resume_thread_id gained
		// `excluded_thread_ids: None` (mechanical consequence of the
		// thread/list exclusion field; Go side already landed as sync551).
		{Path: "exec/src/lib.rs", SHA256: "e1860499175b5c8d37e061773621f41aa4098fced7c83a7bbb3bfd6c735b497e"},
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
		// Re-pinned to upstream 8c076942 (#54945): response item clones share
		// the additional-tool declaration backing storage; no wire change.
		// Round-110 re-anchor to head e389b2444c: Responses Lite incremental
		// tool declarations and the pre-turn compaction/config refresh changes.
		{Path: "core/src/client.rs", SHA256: "5296f323c72ddd27db79c211eb1b773ac85a0a7fd3273546a1e7ea890363a846"},
		// Re-pinned to upstream 8ae55c863d (#47207/#47248/#47377/#47648): the
		// gateway OAuth, MCP resource-target, realtime reasoning-status and
		// executor bearer-token requests.
		// Round-112 re-anchor to 8196bcdc18: thread/sample/cancel joined the
		// client request definitions.
		{Path: "app-server-protocol/src/protocol/common.rs", SHA256: "4d42dd4aabf5b3210e0953c8d9ba5f6e7d76f2cba8418d16d76bf4512993f918"},
		// Re-pinned to upstream 8ae55c863d (#46917/#47028/#47207/#47407): the
		// provider-requirements, message-board, gateway-auth and network-policy
		// suites joined the module list.
		// Re-pinned to upstream 8c076942 (#54930/#54949/#54961): the
		// WebSocket-compression and agent-message-wake suites joined the list.
		{Path: "app-server/tests/suite/v2/mod.rs", SHA256: "2a41c2232f39d7f23b3b7d1348cd73b0b1516d621e4ef46312c62349a2f95482"},
		// Re-pinned to upstream 8ae55c863d (#47407/#47679/#47819/#47820/#47879):
		// the network-policy, extension-hook, guardian-authorization,
		// agent-controller and macOS patch-permission suites joined the module
		// list.
		// Re-pinned to upstream 95ec468619 (round-89 pin advance): the core suite
		// module dropped its windows_sandbox module declaration when #51678 moved
		// that file to core/tests/windows_sandbox.rs.
		{Path: "core/tests/suite/mod.rs", SHA256: "da941c56087578fb2fce6c0e55c39b56f5b628f135488b7a6a8b17c8ff7a2c8a"},
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
