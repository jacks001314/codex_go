package mcp

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// Mirrors codex-rs/codex-mcp/src/connection_pool_tests.rs and the app-server
// sharing suite added by Rust #57978: pooled stdio helpers that negotiate
// `codex/connection-lease` get one lease per chat, the host-owned lease id
// replaces caller-supplied metadata on tool calls and resource reads, and a
// release writes pending cancellations before the release request.

const (
	leaseHelperEnv     = "MCP_STDIO_LEASE_HELPER"
	leaseHelperModeEnv = "MCP_STDIO_LEASE_HELPER_MODE"
	leaseHelperLogEnv  = "MCP_STDIO_LEASE_HELPER_LOG"
)

func TestMCPConnectionLeasePerChatLikeRust(t *testing.T) {
	if os.Getenv(leaseHelperEnv) == "1" {
		runMCPLeaseHelper(t)
		return
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("Executable() error = %v", err)
	}
	logPath := t.TempDir() + "/leases.log"
	env := map[string]string{
		leaseHelperEnv:     "1",
		leaseHelperModeEnv: "lease",
		leaseHelperLogEnv:  logPath,
	}
	chatA := newLeaseService(executable, env)
	chatB := newLeaseService(executable, env)
	defer chatB.Close()

	clientA := sharingClientFor(t, chatA, "leased")
	handleA := chatA.stdioConnectionLease("leased")
	if got := mcpLeaseAsk(t, clientA, handleA, "spoofed-lease"); got == "" || got == "spoofed-lease" {
		t.Fatalf("chat A lease metadata = %q, want the host-owned lease", got)
	}
	leaseA := handleA.ID()
	if leaseA == "" {
		t.Fatal("chat A has no server-side lease")
	}
	if got := leaseLogCount(t, logPath, "open:"+leaseA); got != 1 {
		t.Fatalf("chat A open count = %d, want 1", got)
	}
	if got := mcpLeaseAsk(t, clientA, handleA, ""); got != leaseA {
		t.Fatalf("chat A lease metadata = %q, want the host-owned %q", got, leaseA)
	}

	clientB := sharingClientFor(t, chatB, "leased")
	handleB := chatB.stdioConnectionLease("leased")
	if got := mcpLeaseAsk(t, clientB, handleB, ""); got == "" {
		t.Fatal("chat B has no server-side lease")
	}
	leaseB := handleB.ID()
	if leaseB == "" || leaseB == leaseA {
		t.Fatalf("chat leases = %q / %q, want distinct ids", leaseA, leaseB)
	}
	if clientA != clientB {
		t.Fatal("chats did not share the negotiated stdio connection")
	}

	// A pending request must be cancelled before the release write.
	slowDone := make(chan error, 1)
	go func() {
		var result MCPToolCallResponse
		slowDone <- clientA.CallWithOptions(&stdioCallOptions{ServerName: "leased", Lease: handleA},
			"tools/call", map[string]any{"name": "slow", "arguments": map[string]any{}}, &result)
	}()
	waitForLeaseLog(t, logPath, "slow")
	if err := chatA.Close(); err != nil {
		t.Fatalf("chat A Close() error = %v", err)
	}
	if err := <-slowDone; err == nil {
		t.Fatal("chat A slow call was not cancelled by its release")
	}
	lines := leaseLogLines(t, logPath)
	cancelledIndex, releaseIndex := -1, -1
	for i, line := range lines {
		if line == "cancelled" && cancelledIndex < 0 {
			cancelledIndex = i
		}
		if line == "release:"+leaseA {
			releaseIndex = i
		}
	}
	if cancelledIndex < 0 || releaseIndex < 0 || cancelledIndex > releaseIndex {
		t.Fatalf("cancellation did not precede release: %#v", lines)
	}
	if got := leaseLogCount(t, logPath, "release:"+leaseB); got != 0 {
		t.Fatalf("chat B lease was released early")
	}

	// Chat B keeps working after chat A's release.
	if got := mcpLeaseAsk(t, clientB, handleB, ""); got != leaseB {
		t.Fatalf("chat B lease metadata after release = %q", got)
	}
	if err := chatB.Close(); err != nil {
		t.Fatalf("chat B Close() error = %v", err)
	}
	if got := leaseLogCount(t, logPath, "release:"+leaseB); got != 1 {
		t.Fatalf("chat B release count = %d, want 1", got)
	}
}

func TestMCPConnectionLeaseSkipsHelpersWithoutExtensionLikeRust(t *testing.T) {
	if os.Getenv(leaseHelperEnv) == "1" {
		runMCPLeaseHelper(t)
		return
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("Executable() error = %v", err)
	}
	logPath := t.TempDir() + "/leases.log"
	service := newLeaseService(executable, map[string]string{
		leaseHelperEnv:     "1",
		leaseHelperModeEnv: "plain",
		leaseHelperLogEnv:  logPath,
	})
	defer service.Close()
	client := sharingClientFor(t, service, "leased")
	handle := service.stdioConnectionLease("leased")
	// Without a lease the caller's metadata is preserved verbatim.
	if got := mcpLeaseAsk(t, client, handle, "caller-lease"); got != "caller-lease" {
		t.Fatalf("metadata = %q, want the caller value", got)
	}
	if lease := handle.ID(); lease != "" {
		t.Fatalf("lease = %q, want none without the extension", lease)
	}
	for _, line := range leaseLogLines(t, logPath) {
		if strings.HasPrefix(line, "open:") || strings.HasPrefix(line, "release:") {
			t.Fatalf("helper without the extension received a lease request: %q", line)
		}
	}
}

func newLeaseService(executable string, env map[string]string) *MCPService {
	return NewMCPService(&RuntimeConfig{
		DiscoverLocalStdio: true,
		Servers: map[string]ServerRegistration{
			"leased": {
				Name: "leased",
				Config: ServerConfig{
					Enabled:        true,
					Command:        executable,
					Args:           []string{"-test.run=TestMCPConnectionLease", "--"},
					StartupTimeout: 5 * time.Second,
					Env:            env,
				},
			},
		},
	})
}

func mcpLeaseAsk(t *testing.T, client *stdioClient, lease *mcpChatLease, metaLease string) string {
	t.Helper()
	var meta any
	if metaLease != "" {
		meta = map[string]any{ConnectionLeaseMetaKey: metaLease}
	}
	result, err := callMCPStdioToolWithLease(client, lease, "leased", "", "", "", nil, nil, nil, "lease", map[string]any{}, meta)
	if err != nil {
		t.Fatalf("tools/call error = %v", err)
	}
	if len(result.Content) != 1 {
		t.Fatalf("result = %#v", result.Content)
	}
	return result.Content[0].Text
}

func leaseLogLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	lines := make([]string, 0, 8)
	for _, line := range strings.Split(string(data), "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			lines = append(lines, trimmed)
		}
	}
	return lines
}

func leaseLogCount(t *testing.T, path string, word string) int {
	t.Helper()
	count := 0
	for _, line := range leaseLogLines(t, path) {
		if line == word {
			count++
		}
	}
	return count
}

func waitForLeaseLog(t *testing.T, path string, word string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if leaseLogCount(t, path, word) > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("helper never logged %q", word)
}

func runMCPLeaseHelper(t *testing.T) {
	t.Helper()
	mode := os.Getenv(leaseHelperModeEnv)
	logPath := os.Getenv(leaseHelperLogEnv)
	appendLog := func(line string) {
		if logPath == "" {
			return
		}
		file, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return
		}
		_, _ = file.WriteString(line + "\n")
		_ = file.Close()
	}
	appendLog("start")
	capabilities := map[string]any{"tools": map[string]any{}}
	if mode == "lease" {
		capabilities["extensions"] = map[string]any{ConnectionLeaseCapability: map[string]any{}}
	}
	reader := bufio.NewReader(os.Stdin)
	var mu sync.Mutex
	for {
		data, err := readMCPFrame(reader)
		if err != nil {
			return
		}
		var message struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params map[string]any  `json:"params"`
			Result json.RawMessage `json:"result"`
		}
		if json.Unmarshal(data, &message) != nil {
			continue
		}
		reply := func(result map[string]any) {
			mu.Lock()
			defer mu.Unlock()
			_ = writeMCPFrame(os.Stdout, map[string]any{"jsonrpc": "2.0", "id": message.ID, "result": result})
		}
		switch message.Method {
		case "server/discover":
			reply(map[string]any{
				"resultType":        "complete",
				"supportedVersions": []string{modernMCPProtocol},
				"capabilities":      capabilities,
			})
		case "initialize":
			reply(map[string]any{
				"protocolVersion": defaultMCPProtocol,
				"capabilities":    capabilities,
				"serverInfo":      map[string]any{"name": "lease-helper", "version": "1.0.0"},
			})
		case "codex/connection/open":
			leaseID, _ := message.Params["leaseId"].(string)
			appendLog("open:" + leaseID)
			reply(map[string]any{})
		case "codex/connection/release":
			leaseID, _ := message.Params["leaseId"].(string)
			appendLog("release:" + leaseID)
			reply(map[string]any{})
		case "notifications/initialized":
		case "notifications/cancelled":
			appendLog("cancelled")
		case "tools/list":
			reply(map[string]any{"tools": []any{
				map[string]any{"name": "lease", "description": "echoes the lease", "inputSchema": map[string]any{"type": "object"}},
				map[string]any{"name": "slow", "description": "never answers", "inputSchema": map[string]any{"type": "object"}},
			}})
		case "tools/call":
			name, _ := message.Params["name"].(string)
			switch name {
			case "slow":
				appendLog("slow")
			case "lease":
				meta, _ := message.Params["_meta"].(map[string]any)
				value, _ := meta[ConnectionLeaseMetaKey].(string)
				reply(map[string]any{"content": []any{map[string]any{"type": "text", "text": value}}})
			default:
				reply(map[string]any{"content": []any{map[string]any{"type": "text", "text": "unknown"}}})
			}
		default:
			reply(map[string]any{})
		}
	}
}
