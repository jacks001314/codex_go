package appserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"codex_go/internal/remotecontrol"
	"codex_go/internal/session"
	"codex_go/internal/turn"
)

func stdioTestRequestWithParams(t *testing.T, id RequestID, method Method, params any) *Request {
	t.Helper()
	data, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("Marshal params error = %v", err)
	}
	return &Request{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  data,
	}
}

func TestStdioServerHandlesJSONRPCLine(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewDefaultRuntimeRouter(store, t.TempDir())
	server := NewStdioServer(router)
	var out strings.Builder
	err := server.Serve(strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"test","version":"1"}}}`+"\n"), &out)
	if err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	if !strings.Contains(out.String(), `"id":1`) || !strings.Contains(out.String(), `"result"`) {
		t.Fatalf("output = %s", out.String())
	}
}

func TestStdioServerInitializeEmitsRemoteControlStatusSnapshot(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewDefaultRuntimeRouterWithOptions(store, t.TempDir(), &RuntimeRouterOptions{
		RemoteControlStartupMode: RemoteControlStartupEnabledEphemeral,
	})
	server := NewStdioServer(router)
	var out strings.Builder
	err := server.Serve(strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"test","version":"1"}}}`+"\n"), &out)
	if err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	lines := nonEmptyJSONLines(out.String())
	if len(lines) != 2 {
		t.Fatalf("output lines = %#v, want initialize response and remote-control snapshot", lines)
	}
	var response Response
	if err := json.Unmarshal([]byte(lines[0]), &response); err != nil || response.Error != nil || response.ID.String() != "1" {
		t.Fatalf("initialize response line = %q response = %+v error = %v", lines[0], response, err)
	}
	var notification struct {
		Method NotificationMethod                     `json:"method"`
		Params RemoteControlStatusChangedNotification `json:"params"`
	}
	if err := json.Unmarshal([]byte(lines[1]), &notification); err != nil {
		t.Fatalf("decode snapshot notification error = %v; line = %q", err, lines[1])
	}
	if notification.Method != NotificationRemoteControlStatusChanged || notification.Params.Status != remotecontrol.StatusConnected {
		t.Fatalf("snapshot notification = %+v", notification)
	}
}

func TestStdioServerInitializeRemoteControlStatusSnapshotHonorsOptOut(t *testing.T) {
	server := NewDefaultStdioServer(&StdioOptions{CodexHome: t.TempDir()})
	var out strings.Builder
	input := mustJSONLine(t, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  MethodInitialize,
		"params": map[string]any{
			"clientInfo": map[string]any{"name": "test", "version": "1"},
			"capabilities": map[string]any{
				"optOutNotificationMethods": []string{string(NotificationRemoteControlStatusChanged)},
			},
		},
	}) + "\n"
	err := server.Serve(strings.NewReader(input), &out)
	if err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	lines := nonEmptyJSONLines(out.String())
	if len(lines) != 1 {
		t.Fatalf("output lines = %#v, want only initialize response", lines)
	}
}

func TestStdioServerReportsInvalidLine(t *testing.T) {
	server := NewDefaultStdioServer(&StdioOptions{CodexHome: t.TempDir()})
	var out strings.Builder
	err := server.Serve(strings.NewReader(`{"id":1}`+"\n"), &out)
	if err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	if !strings.Contains(out.String(), `"error"`) || !strings.Contains(out.String(), `invalid request`) {
		t.Fatalf("output = %s", out.String())
	}
}

func TestStdioServerRoutesServerRequestResponse(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	sent := make(chan *ServerRequest, 1)
	router.SetServerRequestSink(ServerRequestSinkFunc(func(request *ServerRequest) {
		sent <- request
	}))
	server := NewStdioServer(router)
	broker := router.requireServerRequests()
	var out strings.Builder
	done := make(chan *ChatGPTAuthTokensRefreshResponse, 1)
	go func() {
		var response ChatGPTAuthTokensRefreshResponse
		if err := broker.Request(context.Background(), ServerRequestChatGPTAuthTokensRefresh, nil, &response); err != nil {
			t.Errorf("Request() error = %v", err)
			return
		}
		done <- &response
	}()

	var requestID string
	select {
	case request := <-sent:
		requestID = request.ID.String()
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for server request")
	}
	err := server.Serve(strings.NewReader(`{"jsonrpc":"2.0","id":"`+requestID+`","result":{"accessToken":"token","chatgptAccountId":"account"}}`+"\n"), &out)
	if err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	if strings.TrimSpace(out.String()) != "" {
		t.Fatalf("output = %s, want no direct response", out.String())
	}
	select {
	case response := <-done:
		if response.AccessToken != "token" || response.ChatGPTAccountID != "account" {
			t.Fatalf("response = %+v", response)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for server request response")
	}
}

func TestStdioServerHandlesConcurrentCommandExecWrite(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewDefaultRuntimeRouter(store, t.TempDir())
	server := NewStdioServer(router)
	processID := "stdio-command-1"
	delta := base64.StdEncoding.EncodeToString([]byte("typed"))
	input := strings.Join([]string{
		mustJSONLine(t, map[string]any{
			"jsonrpc": "2.0",
			"id":      100,
			"method":  MethodInitialize,
			"params": map[string]any{
				"clientInfo": map[string]any{"name": "test", "version": "1"},
			},
		}),
		mustJSONLine(t, map[string]any{
			"jsonrpc": "2.0",
			"method":  ClientNotificationInitialized,
			"params":  map[string]any{},
		}),
		mustJSONLine(t, map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"method":  MethodCommandExec,
			"params": map[string]any{
				"command":            commandExecTestStdinEchoCommand(),
				"processId":          processID,
				"streamStdin":        true,
				"streamStdoutStderr": true,
				"disableTimeout":     true,
				"disableOutputCap":   true,
				"outputBytesCap":     nil,
				"timeoutMs":          nil,
				"cwd":                nil,
				"env":                nil,
				"size":               nil,
				"sandboxPolicy":      nil,
				"permissionProfile":  nil,
			},
		}),
		mustJSONLine(t, map[string]any{
			"jsonrpc": "2.0",
			"id":      2,
			"method":  MethodCommandExecWrite,
			"params": map[string]any{
				"processId":   processID,
				"deltaBase64": delta,
				"closeStdin":  true,
			},
		}),
		"",
	}, "\n")

	var out strings.Builder
	if err := server.Serve(strings.NewReader(input), &out); err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) < 3 {
		t.Fatalf("output lines = %#v", lines)
	}
	if !containsJSONRPCID(lines, 2) {
		t.Fatalf("write response missing in %#v", lines)
	}
	if !containsCommandExecDelta(lines, processID, delta) {
		t.Fatalf("output delta missing in %#v", lines)
	}
	if !containsCommandExecFinalResponse(lines, 1) {
		t.Fatalf("final command response missing in %#v", lines)
	}
}

func TestStdioServerHandlesConcurrentTurnStarts(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewDefaultRuntimeRouter(store, t.TempDir())
	server := NewStdioServer(router)
	firstThread := router.Handle(stdioTestRequestWithParams(t, IntID(100), MethodThreadStart, ThreadStartParams{Ephemeral: true}))
	if firstThread.Error != nil {
		t.Fatalf("first thread/start error: %+v", firstThread.Error)
	}
	secondThread := router.Handle(stdioTestRequestWithParams(t, IntID(101), MethodThreadStart, ThreadStartParams{Ephemeral: true}))
	if secondThread.Error != nil {
		t.Fatalf("second thread/start error: %+v", secondThread.Error)
	}
	firstThreadResult := firstThread.Result.(*ThreadStartResponse)
	secondThreadResult := secondThread.Result.(*ThreadStartResponse)
	firstThreadID := firstThreadResult.Thread.ID
	secondThreadID := secondThreadResult.Thread.ID
	input := strings.Join([]string{
		mustJSONLine(t, map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"method":  MethodInitialize,
			"params": map[string]any{
				"clientInfo": map[string]any{"name": "test", "version": "1"},
			},
		}),
		mustJSONLine(t, map[string]any{
			"jsonrpc": "2.0",
			"method":  ClientNotificationInitialized,
			"params":  map[string]any{},
		}),
		mustJSONLine(t, map[string]any{
			"jsonrpc": "2.0",
			"id":      6,
			"method":  MethodTurnStart,
			"params": turn.TurnStartParams{
				ThreadID: firstThreadID,
				Prompt:   "hello from first thread",
			},
		}),
		mustJSONLine(t, map[string]any{
			"jsonrpc": "2.0",
			"id":      7,
			"method":  MethodTurnStart,
			"params": turn.TurnStartParams{
				ThreadID: secondThreadID,
				Prompt:   "hello from second thread",
			},
		}),
		"",
	}, "\n")

	var out strings.Builder
	if err := server.Serve(strings.NewReader(input), &out); err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	lines := nonEmptyJSONLines(out.String())
	for _, id := range []float64{1, 6, 7} {
		if !containsJSONRPCID(lines, id) {
			t.Fatalf("response id %v missing from %#v", id, lines)
		}
	}
}

func mustJSONLine(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	return string(data)
}

func nonEmptyJSONLines(output string) []string {
	var lines []string
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func containsJSONRPCID(lines []string, id float64) bool {
	for _, line := range lines {
		var envelope map[string]any
		if err := json.Unmarshal([]byte(line), &envelope); err != nil {
			continue
		}
		if envelope["id"] == id && envelope["result"] != nil {
			return true
		}
	}
	return false
}

func containsCommandExecDelta(lines []string, processID string, deltaBase64 string) bool {
	for _, line := range lines {
		var envelope struct {
			Method NotificationMethod `json:"method"`
			Params map[string]any     `json:"params"`
		}
		if err := json.Unmarshal([]byte(line), &envelope); err != nil {
			continue
		}
		if envelope.Method == NotificationCommandExecOutputDelta &&
			envelope.Params["processId"] == processID &&
			envelope.Params["deltaBase64"] == deltaBase64 {
			return true
		}
	}
	return false
}

func containsCommandExecFinalResponse(lines []string, id float64) bool {
	for _, line := range lines {
		var envelope struct {
			ID     any `json:"id"`
			Result struct {
				ExitCode float64 `json:"exitCode"`
				Stdout   string  `json:"stdout"`
				Stderr   string  `json:"stderr"`
			} `json:"result"`
		}
		if err := json.Unmarshal([]byte(line), &envelope); err != nil {
			continue
		}
		if fmt.Sprint(envelope.ID) == fmt.Sprint(id) && envelope.Result.ExitCode == 0 && envelope.Result.Stdout == "" && envelope.Result.Stderr == "" {
			return true
		}
	}
	return false
}
