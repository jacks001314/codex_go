package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
)

type testWriteCloser struct {
	bytes.Buffer
}

func (w *testWriteCloser) Close() error {
	return nil
}

func TestWriteMCPFrameUsesNewlineDelimitedJSON(t *testing.T) {
	var output bytes.Buffer
	if err := writeMCPFrame(&output, map[string]any{"jsonrpc": "2.0", "id": 1}); err != nil {
		t.Fatalf("writeMCPFrame() error = %v", err)
	}
	got := output.String()
	if strings.Contains(strings.ToLower(got), "content-length") {
		t.Fatalf("writeMCPFrame() emitted legacy headers: %q", got)
	}
	if !strings.HasSuffix(got, "\n") || strings.Count(got, "\n") != 1 {
		t.Fatalf("writeMCPFrame() = %q, want one newline-delimited JSON message", got)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(got)), &decoded); err != nil {
		t.Fatalf("writeMCPFrame() emitted invalid JSON: %v", err)
	}
}

func TestReadMCPFrameSupportsNewlineDelimitedJSON(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader("{\"jsonrpc\":\"2.0\",\"id\":1}\r\n"))
	data, err := readMCPFrame(reader)
	if err != nil {
		t.Fatalf("readMCPFrame() error = %v", err)
	}
	if got, want := string(data), "{\"jsonrpc\":\"2.0\",\"id\":1}"; got != want {
		t.Fatalf("readMCPFrame() = %q, want %q", got, want)
	}
}

func TestReadMCPFrameSupportsLegacyContentLength(t *testing.T) {
	payload := "{\"jsonrpc\":\"2.0\",\"id\":1}"
	input := "Content-Type: application/json\r\nContent-Length: " + strconv.Itoa(len(payload)) + "\r\n\r\n" + payload
	data, err := readMCPFrame(bufio.NewReader(strings.NewReader(input)))
	if err != nil {
		t.Fatalf("readMCPFrame() error = %v", err)
	}
	if got := string(data); got != payload {
		t.Fatalf("readMCPFrame() = %q, want %q", got, payload)
	}
}

func TestMCPClientInitializeParamsUseCurrentProtocol(t *testing.T) {
	params := mcpClientInitializeParams(false)
	if params["protocolVersion"] != defaultMCPProtocol {
		t.Fatalf("protocolVersion = %#v, want %q", params["protocolVersion"], defaultMCPProtocol)
	}
	capabilities, ok := params["capabilities"].(map[string]any)
	if !ok {
		t.Fatalf("capabilities = %#v", params["capabilities"])
	}
	roots, ok := capabilities["roots"].(map[string]any)
	if !ok || roots["listChanged"] != false {
		t.Fatalf("roots capability = %#v", capabilities["roots"])
	}
	if _, ok := capabilities["extensions"]; ok {
		t.Fatalf("extensions capability should be absent by default: %#v", capabilities["extensions"])
	}
}

func TestMCPCustomCAEnvPairsForwardInheritedSettings(t *testing.T) {
	t.Setenv("SSL_CERT_FILE", "certs/ca.pem")
	t.Setenv("CURL_CA_BUNDLE", "")
	t.Setenv("NODE_EXTRA_CA_CERTS", "")
	pairs := mcpCustomCAEnvPairs(nil)
	got := map[string]string{}
	for _, entry := range pairs {
		name, value, _ := strings.Cut(entry, "=")
		got[name] = value
	}
	// Relative inherited paths are converted to absolute paths
	// (Rust b2543af02b).
	if value := got["SSL_CERT_FILE"]; value == "" || !filepath.IsAbs(value) || filepath.Base(value) != "ca.pem" {
		t.Fatalf("SSL_CERT_FILE = %q, want absolute path to ca.pem", value)
	}
	if _, ok := got["CURL_CA_BUNDLE"]; ok {
		t.Fatalf("empty CURL_CA_BUNDLE should not be forwarded: %#v", got)
	}
	if _, ok := got["NODE_EXTRA_CA_CERTS"]; ok {
		t.Fatalf("empty NODE_EXTRA_CA_CERTS should not be forwarded: %#v", got)
	}
}

func TestMCPCustomCAEnvPairsExplicitOverridesWin(t *testing.T) {
	t.Setenv("SSL_CERT_FILE", "certs/inherited.pem")
	for _, launchEnv := range []map[string]string{
		{"SSL_CERT_FILE": "certs/explicit.pem"},
	} {
		pairs := mcpCustomCAEnvPairs(launchEnv)
		for _, entry := range pairs {
			name, _, _ := strings.Cut(entry, "=")
			if name == "SSL_CERT_FILE" {
				t.Fatalf("inherited SSL_CERT_FILE forwarded despite explicit override: %#v", pairs)
			}
		}
	}
	if runtime.GOOS == "windows" {
		pairs := mcpCustomCAEnvPairs(map[string]string{"ssl_cert_file": "certs/explicit.pem"})
		for _, entry := range pairs {
			name, _, _ := strings.Cut(entry, "=")
			if name == "SSL_CERT_FILE" {
				t.Fatalf("inherited SSL_CERT_FILE forwarded despite case-insensitive override: %#v", pairs)
			}
		}
	}
}

func TestMCPClientInitializeParamsAdvertisesOpenAIFormExtension(t *testing.T) {
	params := mcpClientInitializeParams(true)
	capabilities, ok := params["capabilities"].(map[string]any)
	if !ok {
		t.Fatalf("capabilities = %#v", params["capabilities"])
	}
	extensions, ok := capabilities["extensions"].(map[string]any)
	if !ok {
		t.Fatalf("extensions capability = %#v", capabilities["extensions"])
	}
	openAIForm, ok := extensions["openai/form"].(map[string]any)
	if !ok || len(openAIForm) != 0 {
		t.Fatalf("openai/form extension = %#v", extensions["openai/form"])
	}
}

func TestReadMCPResultRespondsToServerRequests(t *testing.T) {
	var input bytes.Buffer
	if err := writeMCPFrame(&input, map[string]any{
		"jsonrpc": "2.0",
		"id":      int64(91),
		"method":  "roots/list",
		"params":  map[string]any{},
	}); err != nil {
		t.Fatalf("write roots/list frame error = %v", err)
	}
	if err := writeMCPFrame(&input, map[string]any{
		"jsonrpc": "2.0",
		"id":      int64(92),
		"method":  "elicitation/create",
		"params":  map[string]any{"message": "Approve?"},
	}); err != nil {
		t.Fatalf("write elicitation frame error = %v", err)
	}
	if err := writeMCPFrame(&input, map[string]any{
		"jsonrpc": "2.0",
		"id":      int64(2),
		"result":  map[string]any{"ok": true},
	}); err != nil {
		t.Fatalf("write target response frame error = %v", err)
	}

	var output bytes.Buffer
	var result struct {
		OK bool `json:"ok"`
	}
	if err := readMCPResult(context.Background(), bufio.NewReader(&input), &output, 2, "tools/list", "docs", nil, nil, &result); err != nil {
		t.Fatalf("readMCPResult() error = %v", err)
	}
	if !result.OK {
		t.Fatalf("result = %#v", result)
	}

	responseReader := bufio.NewReader(&output)
	roots := readMCPClientResponse(t, responseReader)
	if roots["id"] != float64(91) {
		t.Fatalf("roots response id = %#v", roots["id"])
	}
	rootsResult, ok := roots["result"].(map[string]any)
	if !ok {
		t.Fatalf("roots result = %#v", roots["result"])
	}
	if list, ok := rootsResult["roots"].([]any); !ok || len(list) != 0 {
		t.Fatalf("roots = %#v", rootsResult["roots"])
	}

	elicitation := readMCPClientResponse(t, responseReader)
	if elicitation["id"] != float64(92) {
		t.Fatalf("elicitation response id = %#v", elicitation["id"])
	}
	elicitationResult, ok := elicitation["result"].(map[string]any)
	if !ok || elicitationResult["action"] != "cancel" {
		t.Fatalf("elicitation result = %#v", elicitation["result"])
	}
}

func TestReadMCPResultRespondsToRootsListWithContextRoots(t *testing.T) {
	var input bytes.Buffer
	if err := writeMCPFrame(&input, map[string]any{
		"jsonrpc": "2.0",
		"id":      int64(91),
		"method":  "roots/list",
		"params":  map[string]any{},
	}); err != nil {
		t.Fatalf("write roots/list frame error = %v", err)
	}
	if err := writeMCPFrame(&input, map[string]any{
		"jsonrpc": "2.0",
		"id":      int64(2),
		"result":  map[string]any{"ok": true},
	}); err != nil {
		t.Fatalf("write target response frame error = %v", err)
	}

	var output bytes.Buffer
	var result struct {
		OK bool `json:"ok"`
	}
	ctx := contextWithMCPClientContextAndRoots(context.Background(), "thread-1", "turn-1", "item-1", []MCPRoot{{
		URI:  "file:///repo",
		Name: "repo",
	}})
	if err := readMCPResult(ctx, bufio.NewReader(&input), &output, 2, "tools/list", "docs", nil, nil, &result); err != nil {
		t.Fatalf("readMCPResult() error = %v", err)
	}
	if !result.OK {
		t.Fatalf("result = %#v", result)
	}
	roots := readMCPClientResponse(t, bufio.NewReader(&output))
	rootsResult, ok := roots["result"].(map[string]any)
	if !ok {
		t.Fatalf("roots result = %#v", roots["result"])
	}
	list, ok := rootsResult["roots"].([]any)
	if !ok || len(list) != 1 {
		t.Fatalf("roots = %#v", rootsResult["roots"])
	}
	root, ok := list[0].(map[string]any)
	if !ok || root["uri"] != "file:///repo" || root["name"] != "repo" {
		t.Fatalf("root = %#v", list[0])
	}
}

func TestReadMCPResultRemoteErrorIsStructured(t *testing.T) {
	var input bytes.Buffer
	if err := writeMCPFrame(&input, map[string]any{
		"jsonrpc": "2.0",
		"id":      int64(7),
		"error": map[string]any{
			"code":    int64(-32603),
			"message": "boom",
			"data":    map[string]any{"reason": "test"},
		},
	}); err != nil {
		t.Fatalf("write error frame returned error: %v", err)
	}

	var output bytes.Buffer
	err := readMCPResult(context.Background(), bufio.NewReader(&input), &output, 7, "resources/read", "docs", nil, nil, nil)
	var remoteErr *MCPRemoteError
	if !errors.As(err, &remoteErr) {
		t.Fatalf("readMCPResult() error = %v, want MCPRemoteError", err)
	}
	if remoteErr.Method != "resources/read" || remoteErr.Code != -32603 || remoteErr.Message != "boom" || !bytes.Contains(remoteErr.Data, []byte("reason")) {
		t.Fatalf("remote error = %#v", remoteErr)
	}
	if output.Len() != 0 {
		t.Fatalf("unexpected client response output: %q", output.String())
	}
}

func TestReadMCPResultUsesElicitationHandler(t *testing.T) {
	var input bytes.Buffer
	if err := writeMCPFrame(&input, map[string]any{
		"jsonrpc": "2.0",
		"id":      int64(11),
		"method":  "elicitation/create",
		"params": map[string]any{
			"message":         "Approve?",
			"requestedSchema": map[string]any{"type": "object"},
			"_meta":           map[string]any{"source": "server"},
		},
	}); err != nil {
		t.Fatalf("write elicitation frame error = %v", err)
	}
	if err := writeMCPFrame(&input, map[string]any{
		"jsonrpc": "2.0",
		"id":      int64(2),
		"result":  map[string]any{"ok": true},
	}); err != nil {
		t.Fatalf("write target response frame error = %v", err)
	}

	handler := MCPElicitationHandlerFunc(func(ctx context.Context, request *MCPElicitationRequest) (*MCPElicitationResponse, error) {
		if request.ServerName != "docs" || request.ThreadID != "thread-1" || request.TurnID != "turn-1" || request.Method != "elicitation/create" || request.Message != "Approve?" {
			t.Fatalf("elicitation request = %#v", request)
		}
		return &MCPElicitationResponse{
			Action:  MCPElicitationActionAccept,
			Content: map[string]any{"approved": true},
			Meta:    map[string]any{"handled": true},
		}, nil
	})

	var output bytes.Buffer
	var result struct {
		OK bool `json:"ok"`
	}
	ctx := contextWithMCPElicitationContext(context.Background(), "thread-1", "turn-1")
	if err := readMCPResult(ctx, bufio.NewReader(&input), &output, 2, "tools/list", "docs", handler, nil, &result); err != nil {
		t.Fatalf("readMCPResult() error = %v", err)
	}
	if !result.OK {
		t.Fatalf("result = %#v", result)
	}
	response := readMCPClientResponse(t, bufio.NewReader(&output))
	responseResult, ok := response["result"].(map[string]any)
	if !ok || responseResult["action"] != "accept" {
		t.Fatalf("elicitation response = %#v", response["result"])
	}
	content, ok := responseResult["content"].(map[string]any)
	if !ok || content["approved"] != true {
		t.Fatalf("elicitation content = %#v", responseResult["content"])
	}
	meta, ok := responseResult["_meta"].(map[string]any)
	if !ok || meta["handled"] != true {
		t.Fatalf("elicitation meta = %#v", responseResult["_meta"])
	}
}

func TestReadMCPResultHandlesProgressNotification(t *testing.T) {
	var input bytes.Buffer
	if err := writeMCPFrame(&input, map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/progress",
		"params": map[string]any{
			"progressToken": "token-1",
			"progress":      float64(1),
			"total":         float64(2),
			"message":       "Working",
		},
	}); err != nil {
		t.Fatalf("write progress frame error = %v", err)
	}
	if err := writeMCPFrame(&input, map[string]any{
		"jsonrpc": "2.0",
		"id":      int64(2),
		"result":  map[string]any{"ok": true},
	}); err != nil {
		t.Fatalf("write target response frame error = %v", err)
	}

	var progressNotification *MCPProgressNotification
	progress := MCPProgressHandlerFunc(func(ctx context.Context, notification *MCPProgressNotification) {
		progressNotification = notification
	})
	var output bytes.Buffer
	var result struct {
		OK bool `json:"ok"`
	}
	ctx := contextWithMCPClientContext(context.Background(), "thread-1", "turn-1", "item-1")
	if err := readMCPResult(ctx, bufio.NewReader(&input), &output, 2, "tools/list", "docs", nil, progress, &result); err != nil {
		t.Fatalf("readMCPResult() error = %v", err)
	}
	if !result.OK {
		t.Fatalf("result = %#v", result)
	}
	if output.Len() != 0 {
		t.Fatalf("unexpected client response output: %q", output.String())
	}
	if progressNotification == nil {
		t.Fatalf("progress notification was not delivered")
	}
	if progressNotification.ServerName != "docs" || progressNotification.ThreadID != "thread-1" || progressNotification.TurnID != "turn-1" || progressNotification.ItemID != "item-1" || progressNotification.ProgressToken != "token-1" || progressNotification.Message != "Working" {
		t.Fatalf("progress notification = %#v", progressNotification)
	}
	if progressNotification.Progress == nil || *progressNotification.Progress != 1 || progressNotification.Total == nil || *progressNotification.Total != 2 {
		t.Fatalf("progress values = %#v", progressNotification)
	}
}

func TestMCPServiceReusesStdioClientSession(t *testing.T) {
	if os.Getenv("MCP_STDIO_REUSE_HELPER") == "1" {
		runStdioReuseHelper(t)
		return
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("Executable() error = %v", err)
	}
	countFile := filepath.Join(t.TempDir(), "starts.txt")
	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"docs": {Config: ServerConfig{
			Enabled: true,
			Command: executable,
			Args:    []string{"-test.run=TestMCPServiceReusesStdioClientSession", "--"},
			Env: map[string]string{
				"MCP_STDIO_REUSE_HELPER": "1",
				"MCP_STDIO_COUNT_FILE":   countFile,
			},
		}},
	}})
	if _, err := service.ListStatusChecked(&MCPListServerStatusParams{}); err != nil {
		t.Fatalf("ListStatusChecked() error = %v", err)
	}
	call, err := service.CallTool(&MCPToolCallParams{Server: "docs", Tool: "echo", Arguments: map[string]any{"text": "hi"}})
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	if len(call.Content) != 1 || call.Content[0].Text != "ok" {
		t.Fatalf("CallTool() = %#v", call)
	}
	starts := readStdioReuseStartCount(t, countFile)
	if starts != 1 {
		t.Fatalf("stdio server starts = %d, want one reused session", starts)
	}
	service.Refresh()
}

func TestStdioClientMultiplexesConcurrentToolCalls(t *testing.T) {
	if os.Getenv("MCP_STDIO_MULTIPLEX_HELPER") == "1" {
		runStdioMultiplexHelper(t)
		return
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("Executable() error = %v", err)
	}
	client := newMCPStdioClient(&ServerConfig{
		Command: executable,
		Args:    []string{"-test.run=TestStdioClientMultiplexesConcurrentToolCalls", "--"},
		Env: map[string]string{
			"MCP_STDIO_MULTIPLEX_HELPER": "1",
		},
	})
	defer client.Close()

	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make([]MCPToolCallResponse, 2)
	errs := make([]error, 2)
	for i, text := range []string{"first", "second"} {
		wg.Add(1)
		go func(index int, value string) {
			defer wg.Done()
			<-start
			errs[index] = client.CallWithOptions(nil, "tools/call", map[string]any{
				"name":      "echo",
				"arguments": map[string]any{"text": value},
			}, &results[index])
		}(i, text)
	}
	close(start)
	wg.Wait()
	for i, want := range []string{"first", "second"} {
		if errs[i] != nil {
			t.Fatalf("CallWithOptions(%d) error = %v", i, errs[i])
		}
		if len(results[i].Content) != 1 || results[i].Content[0].Text != want {
			t.Fatalf("result[%d] = %#v, want %q", i, results[i], want)
		}
	}
}

func TestStdioOutputBufferSupportsConcurrentCaptureAndInspection(t *testing.T) {
	buffer := &stdioOutputBuffer{}
	const writes = 500

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < writes; i++ {
			if _, err := buffer.Write([]byte("stderr\n")); err != nil {
				t.Errorf("Write() error = %v", err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < writes; i++ {
			_ = buffer.String()
		}
	}()
	wg.Wait()

	if got, want := len(buffer.String()), writes*len("stderr\n"); got != want {
		t.Fatalf("captured stderr length = %d, want %d", got, want)
	}
}

func TestStdioClientStderrCaptureIsScopedToProcess(t *testing.T) {
	client := &stdioClient{}
	first := &stdioOutputBuffer{}
	client.stderr = first

	if _, err := first.Write([]byte("first process")); err != nil {
		t.Fatalf("first Write() error = %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if client.currentStderr() != nil {
		t.Fatal("current stderr was not cleared")
	}
	second := &stdioOutputBuffer{}
	client.stderr = second
	if _, err := first.Write([]byte(" still draining")); err != nil {
		t.Fatalf("draining Write() error = %v", err)
	}
	if _, err := second.Write([]byte("second process")); err != nil {
		t.Fatalf("second Write() error = %v", err)
	}

	if got := first.String(); got != "first process still draining" {
		t.Fatalf("first stderr = %q", got)
	}
	if got := client.currentStderr().String(); got != "second process" {
		t.Fatalf("current stderr = %q", got)
	}
}

func TestStdioClientIgnoresFailureFromSupersededProcess(t *testing.T) {
	oldCommand := &exec.Cmd{}
	currentCommand := &exec.Cmd{}
	stdin := &testWriteCloser{}
	client := &stdioClient{
		cmd:         currentCommand,
		stdin:       stdin,
		started:     true,
		initialized: true,
		pending:     map[int64]*stdioPendingCall{},
	}

	client.failTransportFor(oldCommand, io.ErrUnexpectedEOF)

	if client.currentCommand() != currentCommand {
		t.Fatal("superseded process failure cleared the current command")
	}
	if client.stdin != stdin || !client.started || !client.initialized {
		t.Fatal("superseded process failure changed the current transport state")
	}
}

func TestStdioClientIgnoresFrameFromSupersededProcess(t *testing.T) {
	oldCommand := &exec.Cmd{}
	currentCommand := &exec.Cmd{}
	pending := &stdioPendingCall{ID: 7, Result: make(chan stdioCallResult, 1)}
	client := &stdioClient{
		cmd:          currentCommand,
		pending:      map[int64]*stdioPendingCall{7: pending},
		pendingOrder: []int64{7},
	}
	var input bytes.Buffer
	if err := writeMCPFrame(&input, map[string]any{
		"jsonrpc": "2.0",
		"id":      7,
		"result":  map[string]any{"from": "old process"},
	}); err != nil {
		t.Fatalf("writeMCPFrame() error = %v", err)
	}

	client.readLoop(oldCommand, &input)

	select {
	case result := <-pending.Result:
		t.Fatalf("superseded process delivered response: %#v", result)
	default:
	}
	if client.pending[7] != pending {
		t.Fatal("superseded process removed the current pending request")
	}
}

func TestResolveMCPStdioCommandWindowsPrefersCmdOverShellShims(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows command resolution")
	}
	dir := t.TempDir()
	for _, name := range []string{"npx", "npx.ps1", "npx.cmd"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("test"), 0o600); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", name, err)
		}
	}

	got := resolveMCPStdioCommand("npx", map[string]string{"PATH": dir})
	want := filepath.Join(dir, "npx.cmd")
	if !strings.EqualFold(got, want) {
		t.Fatalf("resolveMCPStdioCommand() = %q, want %q", got, want)
	}
	if got := resolveMCPStdioCommand(filepath.Join(dir, "npx"), map[string]string{"PATH": dir}); got != filepath.Join(dir, "npx") {
		t.Fatalf("path command was rewritten to %q", got)
	}
}

func readMCPClientResponse(t *testing.T, reader *bufio.Reader) map[string]any {
	t.Helper()
	data, err := readMCPFrame(reader)
	if err != nil {
		t.Fatalf("readMCPFrame() error = %v", err)
	}
	var response map[string]any
	if err := json.Unmarshal(data, &response); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	return response
}

type stdioMultiplexHelperRequest struct {
	ID   json.RawMessage
	Text string
}

func runStdioMultiplexHelper(t *testing.T) {
	t.Helper()
	reader := bufio.NewReader(os.Stdin)
	var first *stdioMultiplexHelperRequest
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
			if err := writeMCPFrame(os.Stdout, map[string]any{
				"jsonrpc": "2.0",
				"id":      envelope.ID,
				"result": map[string]any{
					"protocolVersion": defaultMCPProtocol,
					"capabilities":    map[string]any{},
					"serverInfo":      map[string]any{"name": "stdio-multiplex-helper", "version": "1.0.0"},
				},
			}); err != nil {
				t.Fatalf("write initialize response error = %v", err)
			}
		case "tools/call":
			request := &stdioMultiplexHelperRequest{
				ID:   append(json.RawMessage(nil), envelope.ID...),
				Text: stdioMultiplexHelperText(envelope.Params),
			}
			if first == nil {
				first = request
				continue
			}
			writeStdioMultiplexToolResponse(t, request)
			writeStdioMultiplexToolResponse(t, first)
			first = nil
		default:
			if err := writeMCPFrame(os.Stdout, map[string]any{
				"jsonrpc": "2.0",
				"id":      envelope.ID,
				"result":  map[string]any{},
			}); err != nil {
				t.Fatalf("write helper fallback response error = %v", err)
			}
		}
	}
}

func stdioMultiplexHelperText(params json.RawMessage) string {
	var payload struct {
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(params, &payload); err != nil {
		return ""
	}
	if text, ok := payload.Arguments["text"].(string); ok {
		return text
	}
	return ""
}

func writeStdioMultiplexToolResponse(t *testing.T, request *stdioMultiplexHelperRequest) {
	t.Helper()
	if err := writeMCPFrame(os.Stdout, map[string]any{
		"jsonrpc": "2.0",
		"id":      request.ID,
		"result": map[string]any{
			"content": []any{map[string]any{"type": "text", "text": request.Text}},
		},
	}); err != nil {
		t.Fatalf("write multiplex tool response error = %v", err)
	}
}

func runStdioReuseHelper(t *testing.T) {
	t.Helper()
	countFile := os.Getenv("MCP_STDIO_COUNT_FILE")
	incrementStdioReuseStartCount(t, countFile)
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
		result := stdioReuseHelperResult(envelope.Method)
		if err := writeMCPFrame(os.Stdout, map[string]any{
			"jsonrpc": "2.0",
			"id":      envelope.ID,
			"result":  result,
		}); err != nil {
			t.Fatalf("write helper response error = %v", err)
		}
		if envelope.Method == "shutdown" {
			return
		}
	}
}

func stdioReuseHelperResult(method string) any {
	switch method {
	case "initialize":
		return map[string]any{
			"protocolVersion": defaultMCPProtocol,
			"capabilities":    map[string]any{},
			"serverInfo":      map[string]any{"name": "stdio-reuse-helper", "version": "1.0.0"},
		}
	case "tools/list":
		return map[string]any{"tools": []any{map[string]any{"name": "echo", "inputSchema": map[string]any{}}}}
	case "resources/list":
		return map[string]any{"resources": []any{}}
	case "resources/templates/list":
		return map[string]any{"resourceTemplates": []any{}}
	case "tools/call":
		return map[string]any{"content": []any{map[string]any{"type": "text", "text": "ok"}}}
	default:
		return map[string]any{}
	}
}

func incrementStdioReuseStartCount(t *testing.T, path string) {
	t.Helper()
	if strings.TrimSpace(path) == "" {
		t.Fatalf("MCP_STDIO_COUNT_FILE is empty")
	}
	count := readStdioReuseStartCount(t, path)
	count++
	if err := os.WriteFile(path, []byte(strconv.Itoa(count)), 0o600); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", path, err)
	}
}

func readStdioReuseStartCount(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", path, err)
	}
	count, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("Atoi(%q) error = %v", string(data), err)
	}
	return count
}
