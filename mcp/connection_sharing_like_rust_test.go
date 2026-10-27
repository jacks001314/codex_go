package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	mcpSharingHelperEnv     = "MCP_STDIO_SHARING_HELPER"
	mcpSharingHelperModeEnv = "MCP_STDIO_SHARING_MODE"
	mcpSharingHelperLogEnv  = "MCP_STDIO_SHARING_LOG"
)

// TestMCPConnectionSharingReusesNegotiatedStdioAcrossChatsLikeRust covers Rust
// #57211: two chats share one modern helper process, each prompt reaches its own
// chat, and the helper shuts down only after the final release.
func TestMCPConnectionSharingReusesNegotiatedStdioAcrossChatsLikeRust(t *testing.T) {
	if os.Getenv(mcpSharingHelperEnv) == "1" {
		runMCPSharingHelper(t)
		return
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("Executable() error = %v", err)
	}
	logPath := filepath.Join(t.TempDir(), "starts")
	env := map[string]string{
		mcpSharingHelperEnv:     "1",
		mcpSharingHelperModeEnv: "shared",
		mcpSharingHelperLogEnv:  logPath,
	}
	chatA := newSharingService(executable, env)
	chatB := newSharingService(executable, env)
	defer chatB.Close()

	clientA := sharingClientFor(t, chatA, "shared")

	if got := mcpSharingAsk(t, clientA, "chat-a"); got != "chat-a" {
		t.Fatalf("chat A answer = %q, want chat-a", got)
	}
	clientB := sharingClientFor(t, chatB, "shared")
	if got := mcpSharingAsk(t, clientB, "chat-b"); got != "chat-b" {
		t.Fatalf("chat B answer = %q, want chat-b", got)
	}
	if clientA != clientB {
		t.Fatal("chats did not share the negotiated stdio connection")
	}
	if starts := mcpSharingLogCount(t, logPath, "start"); starts != 1 {
		t.Fatalf("helper starts = %d, want 1 shared process", starts)
	}

	// Releasing one chat must not stop the helper for the other chat.
	if err := chatA.Close(); err != nil {
		t.Fatalf("chat A Close() error = %v", err)
	}
	if got := mcpSharingAsk(t, clientB, "chat-b-again"); got != "chat-b-again" {
		t.Fatalf("chat B answer after release = %q", got)
	}
	if starts := mcpSharingLogCount(t, logPath, "start"); starts != 1 {
		t.Fatalf("helper starts after release = %d, want 1", starts)
	}

	// The final release shuts the helper down, so the next chat starts a new one.
	if err := chatB.Close(); err != nil {
		t.Fatalf("chat B Close() error = %v", err)
	}
	chatC := newSharingService(executable, env)
	defer chatC.Close()
	clientC := sharingClientFor(t, chatC, "shared")
	if got := mcpSharingAsk(t, clientC, "chat-c"); got != "chat-c" {
		t.Fatalf("chat C answer = %q", got)
	}
	if starts := mcpSharingLogCount(t, logPath, "start"); starts != 2 {
		t.Fatalf("helper starts after final release = %d, want a fresh process", starts)
	}
}

// TestMCPConnectionSharingRetiresForcedRefreshLikeRust covers Rust #57211: a
// forced refresh removes the pooled connection from the join set without
// stopping it for the chats that already share it.
func TestMCPConnectionSharingRetiresForcedRefreshLikeRust(t *testing.T) {
	if os.Getenv(mcpSharingHelperEnv) == "1" {
		runMCPSharingHelper(t)
		return
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("Executable() error = %v", err)
	}
	logPath := filepath.Join(t.TempDir(), "starts")
	env := map[string]string{
		mcpSharingHelperEnv:     "1",
		mcpSharingHelperModeEnv: "shared",
		mcpSharingHelperLogEnv:  logPath,
	}
	chatA := newSharingService(executable, env)
	defer chatA.Close()
	chatB := newSharingService(executable, env)
	defer chatB.Close()

	clientA := sharingClientFor(t, chatA, "shared")
	if got := mcpSharingAsk(t, clientA, "chat-a"); got != "chat-a" {
		t.Fatalf("chat A answer = %q, want chat-a", got)
	}
	clientB := sharingClientFor(t, chatB, "shared")
	if clientA != clientB {
		t.Fatal("chats did not share the negotiated stdio connection")
	}
	if starts := mcpSharingLogCount(t, logPath, "start"); starts != 1 {
		t.Fatalf("helper starts = %d, want 1 shared process", starts)
	}

	// Refreshing chat A retires the pooled connection for new chats only.
	chatA.Refresh()
	if got := mcpSharingAsk(t, clientB, "chat-b"); got != "chat-b" {
		t.Fatalf("chat B answer after refresh = %q, want chat-b", got)
	}
	if starts := mcpSharingLogCount(t, logPath, "start"); starts != 1 {
		t.Fatalf("helper starts after refresh = %d, want 1 running helper", starts)
	}

	chatC := newSharingService(executable, env)
	defer chatC.Close()
	clientC := sharingClientFor(t, chatC, "shared")
	if clientC == clientB {
		t.Fatal("a forced refresh left the retired connection joinable")
	}
	if got := mcpSharingAsk(t, clientC, "chat-c"); got != "chat-c" {
		t.Fatalf("chat C answer = %q, want chat-c", got)
	}
	if starts := mcpSharingLogCount(t, logPath, "start"); starts != 2 {
		t.Fatalf("helper starts after refresh = %d, want a fresh process", starts)
	}

	// Releasing the refreshed chat must not stop the helper chat B still uses.
	if err := chatA.Close(); err != nil {
		t.Fatalf("chat A Close() error = %v", err)
	}
	if got := mcpSharingAsk(t, clientB, "chat-b-again"); got != "chat-b-again" {
		t.Fatalf("chat B answer after refresh release = %q, want chat-b-again", got)
	}
	if starts := mcpSharingLogCount(t, logPath, "start"); starts != 2 {
		t.Fatalf("helper starts after refresh release = %d, want 2", starts)
	}
}

// TestMCPConnectionSharingKeepsLegacyAndStartupInteractiveServersPrivate covers
// Rust #57211: a helper that asks its chat for input while starting is bound to
// that chat and never shared.
func TestMCPConnectionSharingKeepsLegacyAndStartupInteractiveServersPrivate(t *testing.T) {
	if os.Getenv(mcpSharingHelperEnv) == "1" {
		runMCPSharingHelper(t)
		return
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("Executable() error = %v", err)
	}
	logPath := filepath.Join(t.TempDir(), "starts")
	env := map[string]string{
		mcpSharingHelperEnv:     "1",
		mcpSharingHelperModeEnv: "startup-input",
		mcpSharingHelperLogEnv:  logPath,
	}
	chatA := newSharingService(executable, env)
	defer chatA.Close()
	chatB := newSharingService(executable, env)
	defer chatB.Close()

	answered := 0
	clientA := sharingClientFor(t, chatA, "shared")
	var result MCPToolCallResponse
	if err := clientA.CallWithOptions(&stdioCallOptions{
		ServerName: "shared",
		Elicitation: MCPElicitationHandlerFunc(func(ctx context.Context, request *MCPElicitationRequest) (*MCPElicitationResponse, error) {
			answered++
			return &MCPElicitationResponse{Action: MCPElicitationActionAccept, Content: map[string]any{"answer": "startup"}}, nil
		}),
	}, "tools/call", map[string]any{"name": "echo", "arguments": map[string]any{}}, &result); err != nil {
		t.Fatalf("chat A Call() error = %v", err)
	}
	if answered != 1 {
		t.Fatalf("startup elicitations = %d, want 1", answered)
	}
	clientB := sharingClientFor(t, chatB, "shared")
	if clientA == clientB {
		t.Fatal("a startup-interactive connection was shared")
	}
	if got := mcpSharingAsk(t, clientB, "chat-b"); got != "chat-b" {
		t.Fatalf("chat B answer = %q", got)
	}
	if starts := mcpSharingLogCount(t, logPath, "start"); starts != 2 {
		t.Fatalf("helper starts = %d, want one private process per chat", starts)
	}
}

// TestMCPSharedConnectionIsolatesInputsLikeRust covers Rust #57196: prompts get
// globally unique ids, a cancelled caller does not disturb the shared helper,
// and waiting for input does not consume the caller's timeout.
func TestMCPSharedConnectionIsolatesInputsLikeRust(t *testing.T) {
	if os.Getenv(mcpSharingHelperEnv) == "1" {
		runMCPSharingHelper(t)
		return
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("Executable() error = %v", err)
	}
	logPath := filepath.Join(t.TempDir(), "starts")
	service := newSharingService(executable, map[string]string{
		mcpSharingHelperEnv:     "1",
		mcpSharingHelperModeEnv: "shared",
		mcpSharingHelperLogEnv:  logPath,
	})
	defer service.Close()
	client := sharingClientFor(t, service, "shared")

	var (
		mu  sync.Mutex
		ids []string
	)
	handler := MCPElicitationHandlerFunc(func(ctx context.Context, request *MCPElicitationRequest) (*MCPElicitationResponse, error) {
		mu.Lock()
		ids = append(ids, string(request.ID))
		mu.Unlock()
		return &MCPElicitationResponse{Action: MCPElicitationActionAccept, Content: map[string]any{"answer": "unique"}}, nil
	})
	for i := 0; i < 2; i++ {
		var result MCPToolCallResponse
		if err := client.CallWithOptions(&stdioCallOptions{ServerName: "shared", Elicitation: handler},
			"tools/call", map[string]any{"name": "ask", "arguments": map[string]any{}}, &result); err != nil {
			t.Fatalf("Call() error = %v", err)
		}
	}
	mu.Lock()
	trimmed := make([]string, 0, len(ids))
	for _, id := range ids {
		trimmed = append(trimmed, strings.Trim(id, "\""))
	}
	if len(trimmed) != 2 || trimmed[0] == trimmed[1] || !strings.HasPrefix(trimmed[0], "mcp-input/") {
		t.Fatalf("prompt ids = %#v, want two unique mcp-input ids", ids)
	}
	mu.Unlock()

	// A cancelled caller must not tear the shared transport down.
	slowCtx, cancelSlow := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancelSlow()
	var slow MCPToolCallResponse
	if err := client.CallWithOptionsContext(slowCtx, &stdioCallOptions{ServerName: "shared"},
		"tools/call", map[string]any{"name": "slow", "arguments": map[string]any{}}, &slow); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("slow Call() error = %v, want a deadline", err)
	}
	var echo MCPToolCallResponse
	if err := client.CallWithOptions(&stdioCallOptions{ServerName: "shared"},
		"tools/call", map[string]any{"name": "echo", "arguments": map[string]any{}}, &echo); err != nil {
		t.Fatalf("echo after a cancelled caller error = %v", err)
	}
	if len(echo.Content) != 1 || echo.Content[0].Text != "ok" {
		t.Fatalf("echo result = %#v", echo.Content)
	}
	var cancellations MCPToolCallResponse
	if err := client.CallWithOptions(&stdioCallOptions{ServerName: "shared"},
		"tools/call", map[string]any{"name": "cancellations", "arguments": map[string]any{}}, &cancellations); err != nil {
		t.Fatalf("cancellations Call() error = %v", err)
	}
	if len(cancellations.Content) != 1 || cancellations.Content[0].Text == "0" {
		t.Fatalf("server cancellation count = %#v, want the cancelled request", cancellations.Content)
	}

	// Waiting for the user must not consume this caller's own timeout.
	slowHandler := MCPElicitationHandlerFunc(func(ctx context.Context, request *MCPElicitationRequest) (*MCPElicitationResponse, error) {
		time.Sleep(400 * time.Millisecond)
		return &MCPElicitationResponse{Action: MCPElicitationActionAccept, Content: map[string]any{"answer": "paused"}}, nil
	})
	client.config.ToolTimeout = 300 * time.Millisecond
	var paused MCPToolCallResponse
	if err := client.CallWithOptions(&stdioCallOptions{ServerName: "shared", Elicitation: slowHandler},
		"tools/call", map[string]any{"name": "ask", "arguments": map[string]any{}}, &paused); err != nil {
		t.Fatalf("paused Call() error = %v, want the timeout paused during input", err)
	}
	if len(paused.Content) != 1 || paused.Content[0].Text != "paused" {
		t.Fatalf("paused result = %#v", paused.Content)
	}
}

func newSharingService(executable string, env map[string]string) *MCPService {
	return NewMCPService(&RuntimeConfig{
		DiscoverLocalStdio: true,
		Servers: map[string]ServerRegistration{
			"shared": {
				Name: "shared",
				Config: ServerConfig{
					Enabled:        true,
					Command:        executable,
					Args:           []string{"-test.run=TestMCPConnectionSharing", "--"},
					StartupTimeout: 5 * time.Second,
					Env:            env,
				},
			},
		},
	})
}

func sharingClientFor(t *testing.T, service *MCPService, name string) *stdioClient {
	t.Helper()
	config, ok := service.serverConfig(name)
	if !ok {
		t.Fatalf("server %q is not configured", name)
	}
	if config.ProtocolMode != MCPProtocolDiscoverLocalStdio {
		t.Fatalf("protocol mode = %d, want discovery", config.ProtocolMode)
	}
	return service.stdioClientForServer(name, &config)
}

func mcpSharingAsk(t *testing.T, client *stdioClient, answer string) string {
	t.Helper()
	var result MCPToolCallResponse
	err := client.CallWithOptions(&stdioCallOptions{
		ServerName: "shared",
		Elicitation: MCPElicitationHandlerFunc(func(ctx context.Context, request *MCPElicitationRequest) (*MCPElicitationResponse, error) {
			return &MCPElicitationResponse{Action: MCPElicitationActionAccept, Content: map[string]any{"answer": answer}}, nil
		}),
	}, "tools/call", map[string]any{"name": "ask", "arguments": map[string]any{}}, &result)
	if err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	if len(result.Content) != 1 {
		t.Fatalf("result = %#v", result.Content)
	}
	return result.Content[0].Text
}

func mcpSharingLogCount(t *testing.T, path string, word string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	count := 0
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == word {
			count++
		}
	}
	return count
}

func runMCPSharingHelper(t *testing.T) {
	t.Helper()
	mode := os.Getenv(mcpSharingHelperModeEnv)
	logPath := os.Getenv(mcpSharingHelperLogEnv)
	if logPath != "" {
		appendMCPSharingLog(t, logPath, "start")
	}
	reader := bufio.NewReader(os.Stdin)
	initialized := false
	cancellations := 0
	var startupInitializeID json.RawMessage
	for {
		data, err := readMCPFrame(reader)
		if err != nil {
			return
		}
		var message struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params map[string]any  `json:"params"`
			Result map[string]any  `json:"result"`
		}
		if json.Unmarshal(data, &message) != nil {
			continue
		}
		if message.Method == "" {
			// A response from the client: finish a startup elicitation.
			if startupInitializeID != nil {
				appendMCPSharingLog(t, logPath, "startup-answer")
				writeMCPSharingResponse(t, startupInitializeID, map[string]any{
					"resultType":        "complete",
					"supportedVersions": []string{modernMCPProtocol},
					"capabilities":      map[string]any{"tools": map[string]any{}},
				})
				initialized = true
				startupInitializeID = nil
			}
			continue
		}
		switch message.Method {
		case "server/discover":
			mcpDiscoveryHelperAssertModernMeta(t, message.Params)
			result := map[string]any{
				"resultType":        "complete",
				"supportedVersions": []string{modernMCPProtocol},
				"capabilities":      map[string]any{"tools": map[string]any{}},
			}
			if mode == "startup-input" {
				// Ask the starting chat for input before discovery completes.
				startupInitializeID = message.ID
				if err := writeMCPFrame(os.Stdout, map[string]any{
					"jsonrpc": "2.0",
					"id":      "startup-approval",
					"method":  "elicitation/create",
					"params":  map[string]any{"message": "Approve startup"},
				}); err != nil {
					t.Fatalf("write startup elicitation error = %v", err)
				}
				continue
			}
			initialized = true
			writeMCPSharingResponse(t, message.ID, result)
		case "initialize":
			initialized = true
			writeMCPSharingResponse(t, message.ID, map[string]any{
				"protocolVersion": defaultMCPProtocol,
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "sharing-helper", "version": "1.0.0"},
			})
		case "notifications/initialized":
		case "notifications/cancelled":
			cancellations++
		case "tools/list":
			writeMCPSharingResponse(t, message.ID, map[string]any{"tools": []any{map[string]any{
				"name":        "ask",
				"description": "asks",
				"inputSchema": map[string]any{"type": "object"},
			}}})
		case "tools/call":
			if !initialized {
				t.Fatalf("tool call before initialization")
			}
			name, _ := message.Params["name"].(string)
			switch name {
			case "slow":
				// Never answer: the caller cancels this request only.
			case "cancellations":
				writeMCPSharingResponse(t, message.ID, map[string]any{"content": []any{map[string]any{"type": "text", "text": strconv.Itoa(cancellations)}}})
			case "echo":
				writeMCPSharingResponse(t, message.ID, map[string]any{"content": []any{map[string]any{"type": "text", "text": "ok"}}})
			case "ask":
				responses, _ := message.Params["inputResponses"].(map[string]any)
				if len(responses) == 0 {
					writeMCPSharingResponse(t, message.ID, map[string]any{
						"resultType":   "input_required",
						"requestState": "sharing-state",
						"inputRequests": map[string]any{
							"approval": map[string]any{
								"method": "elicitation/create",
								"params": map[string]any{"message": "Approve"},
							},
						},
					})
					continue
				}
				approval, _ := responses["approval"].(map[string]any)
				content, _ := approval["content"].(map[string]any)
				answer, _ := content["answer"].(string)
				writeMCPSharingResponse(t, message.ID, map[string]any{"content": []any{map[string]any{"type": "text", "text": answer}}})
			default:
				t.Fatalf("unexpected tool %q", name)
			}
		default:
			t.Fatalf("unexpected method %q", message.Method)
		}
	}
}

func appendMCPSharingLog(t *testing.T, path string, line string) {
	t.Helper()
	if path == "" {
		return
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("OpenFile(%q) error = %v", path, err)
	}
	_, _ = file.WriteString(line + "\n")
	_ = file.Close()
}

func writeMCPSharingResponse(t *testing.T, id json.RawMessage, result map[string]any) {
	t.Helper()
	if err := writeMCPFrame(os.Stdout, map[string]any{"jsonrpc": "2.0", "id": id, "result": result}); err != nil {
		t.Fatalf("write response error = %v", err)
	}
}
