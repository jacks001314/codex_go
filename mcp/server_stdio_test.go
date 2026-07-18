package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestStdioServerInitializeAndListTools(t *testing.T) {
	var stdin bytes.Buffer
	writeMCPTestFrame(t, &stdin, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-03-26",
			"clientInfo": map[string]string{
				"name":    "client",
				"version": "1",
			},
		},
	})
	writeMCPTestFrame(t, &stdin, map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/list",
		"params":  map[string]any{},
	})

	var stdout bytes.Buffer
	if err := ServeStdio(context.Background(), &StdioServerOptions{Version: "0.0.0"}, &stdin, &stdout); err != nil {
		t.Fatalf("ServeStdio() error = %v", err)
	}
	reader := bufio.NewReader(&stdout)
	initialized := readMCPTestFrame(t, reader)
	if initialized["id"] != float64(1) {
		t.Fatalf("initialize id = %#v", initialized["id"])
	}
	result := initialized["result"].(map[string]any)
	serverInfo := result["serverInfo"].(map[string]any)
	if serverInfo["name"] != "codex-mcp-server" || serverInfo["title"] != "Codex" || serverInfo["version"] != "0.0.0" {
		t.Fatalf("serverInfo = %#v", serverInfo)
	}
	capabilities := result["capabilities"].(map[string]any)
	toolsCapability := capabilities["tools"].(map[string]any)
	if toolsCapability["listChanged"] != true {
		t.Fatalf("tools capability = %#v", toolsCapability)
	}

	listed := readMCPTestFrame(t, reader)
	tools := listed["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 2 {
		t.Fatalf("tools len = %d", len(tools))
	}
	first := tools[0].(map[string]any)
	second := tools[1].(map[string]any)
	if first["name"] != "codex" || first["title"] != "Codex" || second["name"] != "codex-reply" {
		t.Fatalf("tools = %#v", tools)
	}
}

func TestStdioServerCallCodexTool(t *testing.T) {
	runner := &recordingCodexToolRunner{}
	var stdin bytes.Buffer
	writeMCPTestFrame(t, &stdin, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "codex",
			"arguments": map[string]any{
				"prompt": "hello",
				"cwd":    ".",
			},
		},
	})

	var stdout bytes.Buffer
	if err := ServeStdio(context.Background(), &StdioServerOptions{Runner: runner}, &stdin, &stdout); err != nil {
		t.Fatalf("ServeStdio() error = %v", err)
	}
	if runner.codex == nil || runner.codex.Prompt != "hello" || runner.codex.CWD == nil || *runner.codex.CWD != "." {
		t.Fatalf("runner codex args = %#v", runner.codex)
	}
	response := readMCPTestFrame(t, bufio.NewReader(&stdout))
	result := response["result"].(map[string]any)
	structured := result["structuredContent"].(map[string]any)
	if structured["threadId"] != "thread-1" || structured["content"] != "done" {
		t.Fatalf("structuredContent = %#v", structured)
	}
	content := result["content"].([]any)[0].(map[string]any)
	if content["type"] != "text" || content["text"] != "done" {
		t.Fatalf("content = %#v", content)
	}
}

func TestStdioServerCallCodexReplyTool(t *testing.T) {
	runner := &recordingCodexToolRunner{}
	var stdin bytes.Buffer
	writeMCPTestFrame(t, &stdin, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "codex-reply",
			"arguments": map[string]any{
				"threadId": "thread-1",
				"prompt":   "again",
			},
		},
	})

	var stdout bytes.Buffer
	if err := ServeStdio(context.Background(), &StdioServerOptions{Runner: runner}, &stdin, &stdout); err != nil {
		t.Fatalf("ServeStdio() error = %v", err)
	}
	if runner.reply == nil || runner.reply.ThreadID == nil || *runner.reply.ThreadID != "thread-1" || runner.reply.Prompt != "again" {
		t.Fatalf("runner reply args = %#v", runner.reply)
	}
	response := readMCPTestFrame(t, bufio.NewReader(&stdout))
	result := response["result"].(map[string]any)
	structured := result["structuredContent"].(map[string]any)
	if structured["threadId"] != "thread-1" || structured["content"] != "continued" {
		t.Fatalf("structuredContent = %#v", structured)
	}
}

func TestStdioServerRejectsUnknownCodexToolField(t *testing.T) {
	var stdin bytes.Buffer
	writeMCPTestFrame(t, &stdin, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "codex",
			"arguments": map[string]any{
				"prompt":  "hello",
				"profile": "removed",
			},
		},
	})

	var stdout bytes.Buffer
	if err := ServeStdio(context.Background(), nil, &stdin, &stdout); err != nil {
		t.Fatalf("ServeStdio() error = %v", err)
	}
	response := readMCPTestFrame(t, bufio.NewReader(&stdout))
	result := response["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("result = %#v", result)
	}
	content := result["content"].([]any)[0].(map[string]any)
	if !strings.Contains(content["text"].(string), "unknown field") {
		t.Fatalf("content = %#v", content)
	}
	if _, ok := result["structuredContent"]; ok {
		t.Fatalf("error result should not include structuredContent: %#v", result)
	}
}

type recordingCodexToolRunner struct {
	codex *CodexToolCall
	reply *CodexToolReplyCall
}

func (r *recordingCodexToolRunner) RunCodexTool(ctx context.Context, params *CodexToolCall) (*CodexToolResult, error) {
	_ = ctx
	r.codex = params
	return &CodexToolResult{ThreadID: "thread-1", Content: "done"}, nil
}

func (r *recordingCodexToolRunner) ReplyCodexTool(ctx context.Context, params *CodexToolReplyCall) (*CodexToolResult, error) {
	_ = ctx
	r.reply = params
	return &CodexToolResult{ThreadID: stringValue(params.ThreadID), Content: "continued"}, nil
}

func writeMCPTestFrame(t *testing.T, writer *bytes.Buffer, value any) {
	t.Helper()
	if err := writeMCPFrame(writer, value); err != nil {
		t.Fatalf("writeMCPFrame() error = %v", err)
	}
}

func readMCPTestFrame(t *testing.T, reader *bufio.Reader) map[string]any {
	t.Helper()
	data, err := readMCPFrame(reader)
	if err != nil {
		t.Fatalf("readMCPFrame() error = %v", err)
	}
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatalf("Unmarshal frame error = %v; data=%q", err, string(data))
	}
	return value
}
