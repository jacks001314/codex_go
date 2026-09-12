package mcp

import (
	"bufio"
	"encoding/json"
	"os"
	"sync"
	"testing"
	"time"
)

type fakeAuthChangeSource struct {
	mu      sync.Mutex
	state   MCPAuthChangeState
	changed chan struct{}
}

func newFakeAuthChangeSource(state MCPAuthChangeState) *fakeAuthChangeSource {
	return &fakeAuthChangeSource{state: state, changed: make(chan struct{})}
}

func (f *fakeAuthChangeSource) AuthChangeState() MCPAuthChangeState {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state
}

func (f *fakeAuthChangeSource) AuthChanged() <-chan struct{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.changed
}

func (f *fakeAuthChangeSource) trigger(state MCPAuthChangeState) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state = state
	close(f.changed)
	f.changed = make(chan struct{})
}

func TestMCPClientCapabilitiesAdvertiseAuthChangeOnDemand(t *testing.T) {
	offered := mcpClientCapabilitiesWithAuthChange(false, true)
	experimental, ok := offered["experimental"].(map[string]any)
	if !ok {
		t.Fatalf("capabilities = %#v, want experimental map", offered)
	}
	if _, ok := experimental[MCPAuthChangeCapability]; !ok {
		t.Fatalf("experimental = %#v, want %q", experimental, MCPAuthChangeCapability)
	}
	if _, ok := mcpClientCapabilitiesWithAuthChange(false, false)["experimental"]; ok {
		t.Fatal("auth-change capability advertised without a source")
	}
}

func TestMCPServerSupportsAuthChangeReadsExperimentalCapability(t *testing.T) {
	optedIn := json.RawMessage(`{"experimental":{"codex/auth-change":{}}}`)
	if !mcpServerSupportsAuthChange(optedIn) {
		t.Fatal("opted-in server was not detected")
	}
	if mcpServerSupportsAuthChange(json.RawMessage(`{"experimental":{"other":{}}}`)) {
		t.Fatal("unrelated experimental capability counted as opt-in")
	}
	if mcpServerSupportsAuthChange(nil) {
		t.Fatal("missing capabilities counted as opt-in")
	}
}

// TestStdioAuthChangeNotificationsFollowOptIn mirrors Rust #43428: an opted-in
// server receives the current revisions after initialization and on each
// change, and a server that does not opt in receives nothing.
func TestStdioAuthChangeNotificationsFollowOptIn(t *testing.T) {
	if os.Getenv("MCP_STDIO_AUTH_CHANGE_HELPER") == "1" {
		runStdioAuthChangeHelper(t)
		return
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("Executable() error = %v", err)
	}

	source := newFakeAuthChangeSource(MCPAuthChangeState{})
	client := newMCPStdioClient(&ServerConfig{
		Command: executable,
		Args:    []string{"-test.run=TestStdioAuthChangeNotificationsFollowOptIn", "--"},
		Env:     map[string]string{"MCP_STDIO_AUTH_CHANGE_HELPER": "1"},
	})
	defer client.Close()
	client.authChanges = source

	// Initialize first so the opted-in server receives the initial revisions.
	var listed MCPToolCallResponse
	if err := client.CallWithOptions(nil, "tools/list", map[string]any{}, &listed); err != nil {
		t.Fatalf("tools/list error = %v", err)
	}
	source.trigger(MCPAuthChangeState{Generation: 5, OwnerGeneration: 3})

	var result MCPToolCallResponse
	if err := client.CallWithOptions(nil, "tools/call", map[string]any{
		"name":      "collect",
		"arguments": map[string]any{},
	}, &result); err != nil {
		t.Fatalf("CallWithOptions() error = %v", err)
	}
	if len(result.Content) != 1 {
		t.Fatalf("result content = %#v", result.Content)
	}
	var notifications []map[string]any
	if err := json.Unmarshal([]byte(result.Content[0].Text), &notifications); err != nil {
		t.Fatalf("Unmarshal notifications %q error = %v", result.Content[0].Text, err)
	}
	if len(notifications) != 2 {
		t.Fatalf("notifications = %#v, want initial and one change", notifications)
	}
	if notifications[0]["generation"] != float64(0) || notifications[0]["ownerGeneration"] != float64(0) {
		t.Fatalf("initial notification = %#v, want {0 0}", notifications[0])
	}
	if notifications[1]["generation"] != float64(5) || notifications[1]["ownerGeneration"] != float64(3) {
		t.Fatalf("changed notification = %#v, want {5 3}", notifications[1])
	}
}

// TestStdioAuthChangeNotificationsRequireOptIn proves a server that does not
// advertise the capability receives no auth-change notifications.
func TestStdioAuthChangeNotificationsRequireOptIn(t *testing.T) {
	if os.Getenv("MCP_STDIO_AUTH_CHANGE_HELPER") == "1" {
		runStdioAuthChangeHelper(t)
		return
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("Executable() error = %v", err)
	}
	client := newMCPStdioClient(&ServerConfig{
		Command: executable,
		Args:    []string{"-test.run=TestStdioAuthChangeNotificationsRequireOptIn", "--"},
		Env: map[string]string{
			"MCP_STDIO_AUTH_CHANGE_HELPER": "1",
			"MCP_STDIO_AUTH_CHANGE_OPT_IN": "0",
		},
	})
	defer client.Close()
	client.authChanges = newFakeAuthChangeSource(MCPAuthChangeState{Generation: 9})

	var listed MCPToolCallResponse
	if err := client.CallWithOptions(nil, "tools/list", map[string]any{}, &listed); err != nil {
		t.Fatalf("tools/list error = %v", err)
	}
	var result MCPToolCallResponse
	if err := client.CallWithOptions(nil, "tools/call", map[string]any{
		"name":      "collect",
		"arguments": map[string]any{},
	}, &result); err != nil {
		t.Fatalf("CallWithOptions() error = %v", err)
	}
	if len(result.Content) != 1 || result.Content[0].Text != "[]" {
		t.Fatalf("notifications = %#v, want none for a server that did not opt in", result.Content)
	}
}

func runStdioAuthChangeHelper(t *testing.T) {
	t.Helper()
	optIn := os.Getenv("MCP_STDIO_AUTH_CHANGE_OPT_IN") != "0"
	frames := make(chan []byte)
	go func() {
		defer close(frames)
		reader := bufio.NewReader(os.Stdin)
		for {
			data, err := readMCPFrame(reader)
			if err != nil {
				return
			}
			frames <- data
		}
	}()

	recorded := []map[string]any{}
	timeout := time.After(15 * time.Second)
	for {
		select {
		case data, ok := <-frames:
			if !ok {
				return
			}
			var envelope stdioRPCEnvelope
			if err := json.Unmarshal(data, &envelope); err != nil {
				continue
			}
			if envelope.Method == MCPAuthChangeNotification {
				recorded = append(recorded, decodeAuthChangeParams(envelope.Params))
				continue
			}
			switch envelope.Method {
			case "initialize":
				capabilities := map[string]any{}
				if optIn {
					capabilities["experimental"] = map[string]any{MCPAuthChangeCapability: map[string]any{}}
				}
				writeMCPFrameOrFail(t, map[string]any{
					"jsonrpc": "2.0",
					"id":      envelope.ID,
					"result": map[string]any{
						"protocolVersion": defaultMCPProtocol,
						"capabilities":    capabilities,
						"serverInfo":      map[string]any{"name": "auth-change-helper", "version": "1.0.0"},
					},
				})
			case "tools/list":
				writeMCPFrameOrFail(t, map[string]any{
					"jsonrpc": "2.0",
					"id":      envelope.ID,
					"result":  map[string]any{"tools": []any{}},
				})
			case "tools/call":
				// Wait for the follow-up notification the test triggers before
				// responding so the collected payloads are deterministic.
				deadline := time.After(5 * time.Second)
				expected := 2
				if !optIn {
					expected = 0
					deadline = time.After(300 * time.Millisecond)
				}
			collect:
				for len(recorded) < expected {
					select {
					case data, ok := <-frames:
						if !ok {
							break collect
						}
						var pending stdioRPCEnvelope
						if err := json.Unmarshal(data, &pending); err != nil {
							continue
						}
						if pending.Method == MCPAuthChangeNotification {
							recorded = append(recorded, decodeAuthChangeParams(pending.Params))
						}
					case <-deadline:
						break collect
					}
				}
				encoded, _ := json.Marshal(recorded)
				writeMCPFrameOrFail(t, map[string]any{
					"jsonrpc": "2.0",
					"id":      envelope.ID,
					"result":  map[string]any{"content": []any{map[string]any{"type": "text", "text": string(encoded)}}},
				})
				return
			}
		case <-timeout:
			return
		}
	}
}

func decodeAuthChangeParams(params json.RawMessage) map[string]any {
	var decoded map[string]any
	if err := json.Unmarshal(params, &decoded); err != nil {
		return map[string]any{"error": err.Error()}
	}
	return decoded
}
