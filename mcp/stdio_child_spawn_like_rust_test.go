package mcp

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	stdioChildLifecycleHelperEnv = "MCP_STDIO_CHILD_LIFECYCLE_HELPER"
	stdioChildLifecyclePIDEnv    = "MCP_STDIO_CHILD_LIFECYCLE_PID_FILE"
	stdioChildLifecycleMarker    = "mcp-child-stderr-marker"
)

// Mirrors Rust's shared child-process API exposed from `codex-utils-pty`
// (d086e2752dad, #46659): the local stdio MCP server is spawned with stdin,
// stdout, and stderr piped, and the transport owns the child so closing it
// tears the process down (kill-on-drop).
func TestStdioChildSpawnPipesStreamsAndKillsOnCloseLikeRust(t *testing.T) {
	if os.Getenv(stdioChildLifecycleHelperEnv) == "1" {
		runStdioChildLifecycleHelper(t)
		return
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("Executable() error = %v", err)
	}
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	client := newMCPStdioClient(&ServerConfig{
		Command:      executable,
		Args:         []string{"-test.run=TestStdioChildSpawnPipesStreamsAndKillsOnCloseLikeRust", "--"},
		ProtocolMode: MCPProtocolLegacy,
		Env: map[string]string{
			stdioChildLifecycleHelperEnv: "1",
			stdioChildLifecyclePIDEnv:    pidFile,
		},
	})

	var response MCPToolCallResponse
	if err := client.Call("tools/call", map[string]any{
		"name":      "echo",
		"arguments": map[string]any{"text": "piped"},
	}, &response); err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	if len(response.Content) != 1 || response.Content[0].Text != "piped" {
		t.Fatalf("Call() = %#v, want the helper's piped stdout response", response)
	}
	if !awaitStdioChildStderr(t, client, stdioChildLifecycleMarker) {
		t.Fatal("child stderr was not captured through the transport")
	}

	childPID := awaitMCPTestPID(t, pidFile)
	if !mcpTestProcessAlive(childPID) {
		t.Fatalf("child %d exited before the transport was closed", childPID)
	}

	// Rust drops the child and ignores the kill result; Go reports the killed
	// child's exit status, so only unexpected failures are fatal here.
	if err := client.Close(); err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("Close() error = %v", err)
		}
	}
	if !awaitMCPTestProcessExit(childPID, 10*time.Second) {
		t.Fatalf("child %d survived Close(); the transport does not own the child", childPID)
	}
}

// runStdioChildLifecycleHelper is re-executed as the local stdio server. It
// records its pid and writes one stderr diagnostic before serving frames, so
// the parent can prove both pipes are wired.
func runStdioChildLifecycleHelper(t *testing.T) {
	t.Helper()
	if pidFile := os.Getenv(stdioChildLifecyclePIDEnv); pidFile != "" {
		if err := os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", pidFile, err)
		}
	}
	if _, err := fmt.Fprintln(os.Stderr, stdioChildLifecycleMarker); err != nil {
		t.Fatalf("write stderr marker error = %v", err)
	}
	reader := bufio.NewReader(os.Stdin)
	for {
		data, err := readMCPFrame(reader)
		if err != nil {
			return
		}
		var envelope stdioRPCEnvelope
		if err := json.Unmarshal(data, &envelope); err != nil {
			t.Fatalf("Unmarshal helper request error = %v", err)
		}
		if strings.TrimSpace(envelope.Method) == "" || len(envelope.ID) == 0 {
			continue
		}
		result := any(map[string]any{})
		switch envelope.Method {
		case "initialize":
			result = map[string]any{
				"protocolVersion": defaultMCPProtocol,
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]any{"name": "stdio-child-lifecycle-helper", "version": "1.0.0"},
			}
		case "tools/call":
			result = map[string]any{
				"content": []any{map[string]any{"type": "text", "text": stdioChildLifecycleCallText(envelope.Params)}},
			}
		}
		if err := writeMCPFrame(os.Stdout, map[string]any{
			"jsonrpc": "2.0",
			"id":      envelope.ID,
			"result":  result,
		}); err != nil {
			t.Fatalf("write helper response error = %v", err)
		}
	}
}

func stdioChildLifecycleCallText(params json.RawMessage) string {
	var request struct {
		Arguments struct {
			Text string `json:"text"`
		} `json:"arguments"`
	}
	if err := json.Unmarshal(params, &request); err != nil {
		return ""
	}
	return request.Arguments.Text
}

func awaitStdioChildStderr(t *testing.T, client *stdioClient, marker string) bool {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if buffer := client.currentStderr(); buffer != nil && strings.Contains(buffer.String(), marker) {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}
