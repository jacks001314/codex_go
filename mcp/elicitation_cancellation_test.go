package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestMCPElicitationCancellationMemoryRemembersBeforeAndDuring(t *testing.T) {
	var memory mcpElicitationCancellationMemory

	// Early cancellation: the notification arrives before the request.
	memory.remember(json.RawMessage(`900`))
	if !memory.take(json.RawMessage(`900`)) {
		t.Fatal("early cancellation was not reported")
	}
	// take consumes the cancellation so a reused id starts fresh.
	if memory.take(json.RawMessage(`900`)) {
		t.Fatal("cancellation was reported twice for the same id")
	}

	// Late cancellation: recorded while the request is already in flight.
	memory.remember(json.RawMessage(`"req-1"`))
	if !memory.cancelled(json.RawMessage(`"req-1"`)) {
		t.Fatal("late cancellation was not observable")
	}
	memory.forget(json.RawMessage(`"req-1"`))
	if memory.cancelled(json.RawMessage(`"req-1"`)) {
		t.Fatal("forget did not drop the cancellation")
	}
}

func TestMCPElicitationCancellationMemorySaturatesWithoutEvicting(t *testing.T) {
	var memory mcpElicitationCancellationMemory
	for i := 0; i < maxMCPElicitationCancellations; i++ {
		memory.remember(json.RawMessage(mcpJSONNumber(i)))
	}
	// The remembered set never evicted an entry.
	if !memory.cancelled(json.RawMessage(`0`)) {
		t.Fatal("remembered cancellation was evicted at saturation")
	}
	// A new id after saturation cancels every subsequent elicitation.
	memory.remember(json.RawMessage(`"overflow"`))
	if !memory.take(json.RawMessage(`"fresh"`)) {
		t.Fatal("saturated memory did not cancel a new elicitation")
	}
}

func TestMCPElicitationCancellationMemoryResetScopesToConnection(t *testing.T) {
	var memory mcpElicitationCancellationMemory
	memory.remember(json.RawMessage(`900`))
	memory.reset()
	// A new connection accepts a previously cancelled request id.
	if memory.take(json.RawMessage(`900`)) {
		t.Fatal("reset did not clear cancellation state")
	}
}

func TestMCPCancelledNotificationRequestIDParsesCamelAndSnakeCase(t *testing.T) {
	if got := string(mcpCancelledNotificationRequestID(json.RawMessage(`{"requestId":42}`))); got != "42" {
		t.Fatalf("requestId = %q", got)
	}
	if got := string(mcpCancelledNotificationRequestID(json.RawMessage(`{"request_id":"req-7"}`))); got != `"req-7"` {
		t.Fatalf("request_id = %q", got)
	}
	if got := mcpCancelledNotificationRequestID(json.RawMessage(`{"reason":"stop"}`)); len(got) != 0 {
		t.Fatalf("missing request id = %q", got)
	}
}

func TestMCPElicitationResultHonorsCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	handler := MCPElicitationHandlerFunc(func(context.Context, *MCPElicitationRequest) (*MCPElicitationResponse, error) {
		called = true
		return &MCPElicitationResponse{Action: MCPElicitationActionAccept}, nil
	})
	response, ok := mcpElicitationResult(ctx, "docs", handler, "elicitation/create", json.RawMessage(`1`), nil).(*MCPElicitationResponse)
	if !ok || response.Action != MCPElicitationActionCancel {
		t.Fatalf("cancelled elicitation action = %#v, want cancel", response)
	}
	if called {
		t.Fatal("handler ran for an already cancelled elicitation")
	}
}

// TestStdioElicitationReturnsCancelOnServerCancellation proves a form/URL
// elicitation answers `cancel` when the server cancels it instead of waiting
// for the user, matching Rust #44238.
func TestStdioElicitationReturnsCancelOnServerCancellation(t *testing.T) {
	if os.Getenv("MCP_STDIO_CANCEL_HELPER") == "1" {
		runStdioElicitationCancelHelper(t)
		return
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("Executable() error = %v", err)
	}
	client := newMCPStdioClient(&ServerConfig{
		Command: executable,
		Args:    []string{"-test.run=TestStdioElicitationReturnsCancelOnServerCancellation", "--"},
		Env:     map[string]string{"MCP_STDIO_CANCEL_HELPER": "1"},
	})
	defer client.Close()

	handler := MCPElicitationHandlerFunc(func(_ context.Context, request *MCPElicitationRequest) (*MCPElicitationResponse, error) {
		// Block like a user prompt would until the cancellation is observed.
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if client.elicitationCancellations.cancelled(request.ID) {
				break
			}
			time.Sleep(time.Millisecond)
		}
		return &MCPElicitationResponse{Action: MCPElicitationActionAccept, Content: map[string]any{"approved": true}}, nil
	})

	var result MCPToolCallResponse
	if err := client.CallWithOptions(&stdioCallOptions{Elicitation: handler}, "tools/call", map[string]any{
		"name":      "approve",
		"arguments": map[string]any{},
	}, &result); err != nil {
		t.Fatalf("CallWithOptions() error = %v", err)
	}
	if len(result.Content) != 1 || result.Content[0].Text != string(MCPElicitationActionCancel) {
		t.Fatalf("elicitation action = %#v, want %q", result.Content, MCPElicitationActionCancel)
	}
}

func runStdioElicitationCancelHelper(t *testing.T) {
	t.Helper()
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
		switch envelope.Method {
		case "initialize":
			writeMCPFrameOrFail(t, map[string]any{
				"jsonrpc": "2.0",
				"id":      envelope.ID,
				"result": map[string]any{
					"protocolVersion": defaultMCPProtocol,
					"capabilities":    map[string]any{},
					"serverInfo":      map[string]any{"name": "elicitation-cancel-helper", "version": "1.0.0"},
				},
			})
		case "tools/call":
			// Ask for a form elicitation, then cancel it before answering.
			writeMCPFrameOrFail(t, map[string]any{
				"jsonrpc": "2.0",
				"id":      int64(900),
				"method":  "elicitation/create",
				"params":  map[string]any{"message": "Approve?", "requestedSchema": map[string]any{"type": "object"}},
			})
			writeMCPFrameOrFail(t, map[string]any{
				"jsonrpc": "2.0",
				"method":  "notifications/cancelled",
				"params":  map[string]any{"requestId": int64(900)},
			})
			action := "none"
			for {
				frame, err := readMCPFrame(reader)
				if err != nil {
					return
				}
				var response struct {
					ID     json.RawMessage `json:"id"`
					Result map[string]any  `json:"result"`
				}
				if err := json.Unmarshal(frame, &response); err != nil {
					continue
				}
				if mcpElicitationRequestIDKey(response.ID) != "900" {
					continue
				}
				if value, ok := response.Result["action"].(string); ok {
					action = value
				}
				break
			}
			writeMCPFrameOrFail(t, map[string]any{
				"jsonrpc": "2.0",
				"id":      envelope.ID,
				"result":  map[string]any{"content": []any{map[string]any{"type": "text", "text": action}}},
			})
			return
		default:
			writeMCPFrameOrFail(t, map[string]any{
				"jsonrpc": "2.0",
				"id":      envelope.ID,
				"result":  map[string]any{},
			})
		}
	}
}

func writeMCPFrameOrFail(t *testing.T, value any) {
	t.Helper()
	if err := writeMCPFrame(os.Stdout, value); err != nil {
		t.Fatalf("writeMCPFrame() error = %v", err)
	}
}

func mcpJSONNumber(value int) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
