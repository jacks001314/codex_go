package mcp

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

const (
	defaultMCPServerName    = "codex-mcp-server"
	defaultMCPServerTitle   = "Codex"
	defaultMCPServerVersion = "go-port"
	defaultMCPProtocol      = "2025-06-18"
)

type StdioServerOptions struct {
	Name      string
	Title     string
	Version   string
	UserAgent string
	Runner    CodexToolRunner
}

type CodexToolCall struct {
	Prompt                string         `json:"prompt"`
	Model                 *string        `json:"model,omitempty"`
	CWD                   *string        `json:"cwd,omitempty"`
	ApprovalPolicy        *string        `json:"approval-policy,omitempty"`
	Sandbox               *string        `json:"sandbox,omitempty"`
	Config                map[string]any `json:"config,omitempty"`
	BaseInstructions      *string        `json:"base-instructions,omitempty"`
	DeveloperInstructions *string        `json:"developer-instructions,omitempty"`
	CompactPrompt         *string        `json:"compact-prompt,omitempty"`
}

type CodexToolReplyCall struct {
	ConversationID *string `json:"conversationId,omitempty"`
	ThreadID       *string `json:"threadId,omitempty"`
	Prompt         string  `json:"prompt"`
}

type CodexToolResult struct {
	ThreadID string `json:"threadId"`
	Content  string `json:"content"`
	IsError  *bool  `json:"-"`
}

type CodexToolRunner interface {
	RunCodexTool(ctx context.Context, params *CodexToolCall) (*CodexToolResult, error)
	ReplyCodexTool(ctx context.Context, params *CodexToolReplyCall) (*CodexToolResult, error)
}

type unavailableCodexToolRunner struct{}

func (unavailableCodexToolRunner) RunCodexTool(context.Context, *CodexToolCall) (*CodexToolResult, error) {
	return nil, errors.New("codex MCP tool runner is not configured")
}

func (unavailableCodexToolRunner) ReplyCodexTool(context.Context, *CodexToolReplyCall) (*CodexToolResult, error) {
	return nil, errors.New("codex MCP tool runner is not configured")
}

type MemoryCodexToolRunner struct {
	mu      sync.Mutex
	threads map[string][]string
	now     func() time.Time
}

func NewMemoryCodexToolRunner() *MemoryCodexToolRunner {
	return &MemoryCodexToolRunner{threads: map[string][]string{}, now: time.Now}
}

func (r *MemoryCodexToolRunner) SetClock(clock func() time.Time) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if clock == nil {
		r.now = time.Now
		return
	}
	r.now = clock
}

func (r *MemoryCodexToolRunner) RunCodexTool(ctx context.Context, params *CodexToolCall) (*CodexToolResult, error) {
	_ = ctx
	if params == nil || strings.TrimSpace(params.Prompt) == "" {
		return nil, invalidCodexToolCall("prompt is required")
	}
	if r == nil {
		r = NewMemoryCodexToolRunner()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensureLocked()
	threadID := newMCPThreadID()
	r.threads[threadID] = []string{params.Prompt}
	return &CodexToolResult{
		ThreadID: threadID,
		Content:  "Codex session started.",
	}, nil
}

func (r *MemoryCodexToolRunner) ReplyCodexTool(ctx context.Context, params *CodexToolReplyCall) (*CodexToolResult, error) {
	_ = ctx
	if params == nil || strings.TrimSpace(params.Prompt) == "" {
		return nil, invalidCodexToolCall("prompt is required")
	}
	threadID := strings.TrimSpace(stringValue(params.ThreadID))
	if threadID == "" {
		threadID = strings.TrimSpace(stringValue(params.ConversationID))
	}
	if threadID == "" {
		return nil, invalidCodexToolCall("either threadId or conversationId must be provided")
	}
	if r == nil {
		r = NewMemoryCodexToolRunner()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensureLocked()
	r.threads[threadID] = append(r.threads[threadID], params.Prompt)
	return &CodexToolResult{
		ThreadID: threadID,
		Content:  "Codex session continued.",
	}, nil
}

func (r *MemoryCodexToolRunner) ensureLocked() {
	if r.threads == nil {
		r.threads = map[string][]string{}
	}
	if r.now == nil {
		r.now = time.Now
	}
}

type codexToolCallError struct {
	message string
}

func (e *codexToolCallError) Error() string {
	if e == nil {
		return ""
	}
	return e.message
}

func invalidCodexToolCall(message string) error {
	return &codexToolCallError{message: message}
}

type stdioMCPServer struct {
	name      string
	title     string
	version   string
	userAgent string
	runner    CodexToolRunner

	initialized bool
	shutdown    bool
}

type stdioMCPRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *stdioRPCError  `json:"error,omitempty"`
}

func (e *stdioRPCError) Error() string {
	if e == nil {
		return ""
	}
	if strings.TrimSpace(e.Message) != "" {
		return e.Message
	}
	return fmt.Sprintf("MCP %d", e.Code)
}

type initializeParams struct {
	ProtocolVersion string `json:"protocolVersion"`
	ClientInfo      struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"clientInfo"`
}

type callToolParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

func ServeStdio(ctx context.Context, options *StdioServerOptions, stdin io.Reader, stdout io.Writer) error {
	if ctx == nil {
		ctx = context.Background()
	}
	server := newStdioMCPServer(options)
	return server.Serve(ctx, stdin, stdout)
}

func newStdioMCPServer(options *StdioServerOptions) *stdioMCPServer {
	if options == nil {
		options = &StdioServerOptions{}
	}
	server := &stdioMCPServer{
		name:      firstNonEmptyMCP(options.Name, defaultMCPServerName),
		title:     firstNonEmptyMCP(options.Title, defaultMCPServerTitle),
		version:   firstNonEmptyMCP(options.Version, defaultMCPServerVersion),
		userAgent: strings.TrimSpace(options.UserAgent),
		runner:    options.Runner,
	}
	if server.runner == nil {
		server.runner = unavailableCodexToolRunner{}
	}
	return server
}

func (s *stdioMCPServer) Serve(ctx context.Context, stdin io.Reader, stdout io.Writer) error {
	if s == nil {
		return errors.New("mcp stdio server is nil")
	}
	if stdin == nil {
		return errors.New("mcp stdio stdin is nil")
	}
	if stdout == nil {
		return errors.New("mcp stdio stdout is nil")
	}
	reader := bufio.NewReader(stdin)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		data, err := readMCPServerMessage(reader)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if len(bytes.TrimSpace(data)) == 0 {
			continue
		}
		response, exit, err := s.handleMessage(ctx, data)
		if err != nil {
			return err
		}
		if response != nil {
			if err := writeMCPFrame(stdout, response); err != nil {
				return err
			}
		}
		if exit {
			return nil
		}
	}
}

func readMCPServerMessage(reader *bufio.Reader) ([]byte, error) {
	for {
		peeked, err := reader.Peek(1)
		if err != nil {
			return nil, err
		}
		switch peeked[0] {
		case ' ', '\t', '\r', '\n':
			if _, err := reader.ReadByte(); err != nil {
				return nil, err
			}
			continue
		case '{':
			line, err := reader.ReadBytes('\n')
			if err != nil && !errors.Is(err, io.EOF) {
				return nil, err
			}
			return bytes.TrimSpace(line), nil
		default:
			return readMCPFrame(reader)
		}
	}
}

func (s *stdioMCPServer) handleMessage(ctx context.Context, data []byte) (any, bool, error) {
	var request stdioMCPRequest
	if err := json.Unmarshal(data, &request); err != nil {
		return mcpErrorResponse(nil, -32700, "parse error: "+err.Error()), false, nil
	}
	if request.JSONRPC != "" && request.JSONRPC != "2.0" {
		return mcpErrorResponse(requestIDPtr(request.ID), -32600, "invalid jsonrpc version"), false, nil
	}
	if request.Method == "" {
		return nil, false, nil
	}
	if request.Method == "exit" && !hasRequestID(request.ID) {
		return nil, true, nil
	}
	if !hasRequestID(request.ID) {
		return nil, false, nil
	}
	result, err := s.handleRequest(ctx, &request)
	if err != nil {
		code := int64(-32603)
		var rpcErr *stdioRPCError
		if errors.As(err, &rpcErr) {
			code = rpcErr.Code
		}
		return mcpErrorResponse(&request.ID, code, err.Error()), false, nil
	}
	return map[string]any{
		"jsonrpc": "2.0",
		"id":      json.RawMessage(append([]byte(nil), request.ID...)),
		"result":  result,
	}, false, nil
}

func (s *stdioMCPServer) handleRequest(ctx context.Context, request *stdioMCPRequest) (any, error) {
	switch request.Method {
	case "initialize":
		return s.handleInitialize(request)
	case "ping":
		return map[string]any{}, nil
	case "shutdown":
		s.shutdown = true
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": codexMCPTools()}, nil
	case "tools/call":
		return s.handleCallTool(ctx, request.Params)
	case "resources/list":
		return map[string]any{"resources": []any{}}, nil
	case "resources/templates/list":
		return map[string]any{"resourceTemplates": []any{}}, nil
	case "prompts/list":
		return map[string]any{"prompts": []any{}}, nil
	default:
		return nil, &stdioRPCError{Code: -32601, Message: "method not found: " + request.Method}
	}
}

func (s *stdioMCPServer) handleInitialize(request *stdioMCPRequest) (any, error) {
	if s.initialized {
		return nil, &stdioRPCError{Code: -32600, Message: "initialize called more than once"}
	}
	var params initializeParams
	if len(request.Params) > 0 {
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, &stdioRPCError{Code: -32602, Message: "invalid initialize params: " + err.Error()}
		}
	}
	protocolVersion := strings.TrimSpace(params.ProtocolVersion)
	if protocolVersion == "" {
		protocolVersion = defaultMCPProtocol
	}
	userAgent := s.userAgent
	if userAgent == "" && strings.TrimSpace(params.ClientInfo.Name) != "" {
		userAgent = params.ClientInfo.Name
		if strings.TrimSpace(params.ClientInfo.Version) != "" {
			userAgent += "; " + params.ClientInfo.Version
		}
	}
	serverInfo := map[string]any{
		"name":    s.name,
		"title":   s.title,
		"version": s.version,
	}
	if userAgent != "" {
		serverInfo["user_agent"] = userAgent
	}
	s.initialized = true
	return map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities": map[string]any{
			"tools": map[string]any{
				"listChanged": true,
			},
		},
		"serverInfo": serverInfo,
	}, nil
}

func (s *stdioMCPServer) handleCallTool(ctx context.Context, raw json.RawMessage) (any, error) {
	var params callToolParams
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, &stdioRPCError{Code: -32602, Message: "invalid tools/call params: " + err.Error()}
		}
	}
	switch params.Name {
	case "codex":
		if len(bytes.TrimSpace(params.Arguments)) == 0 {
			return callToolErrorResult("Missing arguments for codex tool-call; the `prompt` field is required."), nil
		}
		var args CodexToolCall
		if err := decodeToolArguments(params.Arguments, &args); err != nil {
			return callToolErrorResult("Failed to parse configuration for Codex tool: " + err.Error()), nil
		}
		result, err := s.runner.RunCodexTool(ctx, &args)
		if err != nil {
			return callToolErrorResult(codexToolCallErrorMessage(err, "Missing arguments for codex tool-call; the `prompt` field is required.")), nil
		}
		return callToolResult(result), nil
	case "codex-reply":
		if len(bytes.TrimSpace(params.Arguments)) == 0 {
			return callToolErrorResult("Missing arguments for codex-reply tool-call; the `threadId` and `prompt` fields are required."), nil
		}
		var args CodexToolReplyCall
		if err := decodeToolArguments(params.Arguments, &args); err != nil {
			return callToolErrorResult("Failed to parse configuration for Codex tool: " + err.Error()), nil
		}
		result, err := s.runner.ReplyCodexTool(ctx, &args)
		if err != nil {
			return callToolErrorResult(codexToolCallErrorMessage(err, "Missing arguments for codex-reply tool-call; the `threadId` and `prompt` fields are required.")), nil
		}
		return callToolResult(result), nil
	default:
		return callToolErrorResult(fmt.Sprintf("Unknown tool '%s'", params.Name)), nil
	}
}

func decodeToolArguments(raw json.RawMessage, target any) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		return errors.New("missing arguments")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

func codexToolCallErrorMessage(err error, fallback string) string {
	if err == nil {
		return ""
	}
	var invalid *codexToolCallError
	if errors.As(err, &invalid) {
		if strings.TrimSpace(invalid.message) != "" {
			return invalid.message
		}
		return fallback
	}
	return err.Error()
}

func codexMCPTools() []map[string]any {
	return []map[string]any{codexToolSchema(), codexReplyToolSchema()}
}

func codexToolSchema() map[string]any {
	return map[string]any{
		"name":        "codex",
		"title":       "Codex",
		"description": "Run a Codex session. Accepts configuration parameters matching the Codex Config struct.",
		"inputSchema": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"prompt"},
			"properties": map[string]any{
				"prompt": map[string]any{
					"type":        "string",
					"description": "The *initial user prompt* to start the Codex conversation.",
				},
				"model": map[string]any{
					"type":        "string",
					"description": "Optional override for the model name (e.g. 'gpt-5.2', 'gpt-5.2-codex').",
				},
				"cwd": map[string]any{
					"type":        "string",
					"description": "Working directory for the session. If relative, it is resolved against the server process's current working directory.",
				},
				"approval-policy": map[string]any{
					"type":        "string",
					"description": "Approval policy for shell commands generated by the model: `untrusted`, `on-request`, `never`.",
					"enum":        []string{"untrusted", "on-request", "never"},
				},
				"sandbox": map[string]any{
					"type":        "string",
					"description": "Sandbox mode: `read-only`, `workspace-write`, or `danger-full-access`.",
					"enum":        []string{"read-only", "workspace-write", "danger-full-access"},
				},
				"config": map[string]any{
					"type":                 "object",
					"description":          "Individual config settings that will override what is in CODEX_HOME/config.toml.",
					"additionalProperties": true,
				},
				"base-instructions": map[string]any{
					"type":        "string",
					"description": "The set of instructions to use instead of the default ones.",
				},
				"developer-instructions": map[string]any{
					"type":        "string",
					"description": "Developer instructions that should be injected as a developer role message.",
				},
				"compact-prompt": map[string]any{
					"type":        "string",
					"description": "Prompt used when compacting the conversation.",
				},
			},
		},
		"outputSchema": codexToolOutputSchema(),
	}
}

func codexReplyToolSchema() map[string]any {
	return map[string]any{
		"name":        "codex-reply",
		"title":       "Codex Reply",
		"description": "Continue a Codex conversation by providing the thread id and prompt.",
		"inputSchema": map[string]any{
			"type":     "object",
			"required": []string{"prompt"},
			"properties": map[string]any{
				"conversationId": map[string]any{
					"type":        "string",
					"description": "DEPRECATED: use threadId instead.",
				},
				"threadId": map[string]any{
					"type":        "string",
					"description": "The thread id for this Codex session. This field is required, but we keep it optional here for backward compatibility for clients that still use conversationId.",
				},
				"prompt": map[string]any{
					"type":        "string",
					"description": "The *next user prompt* to continue the Codex conversation.",
				},
			},
		},
		"outputSchema": codexToolOutputSchema(),
	}
}

func codexToolOutputSchema() map[string]any {
	return map[string]any{
		"type":     "object",
		"required": []string{"threadId", "content"},
		"properties": map[string]any{
			"threadId": map[string]any{"type": "string"},
			"content":  map[string]any{"type": "string"},
		},
	}
}

func callToolResult(result *CodexToolResult) map[string]any {
	if result == nil {
		result = &CodexToolResult{}
	}
	content := result.Content
	response := map[string]any{
		"content": []map[string]any{{
			"type": "text",
			"text": content,
		}},
		"structuredContent": map[string]any{
			"threadId": result.ThreadID,
			"content":  content,
		},
	}
	if result.IsError != nil {
		response["isError"] = *result.IsError
	}
	return response
}

func callToolErrorResult(message string) map[string]any {
	return map[string]any{
		"content": []map[string]any{{
			"type": "text",
			"text": message,
		}},
		"isError": true,
	}
}

func mcpErrorResponse(id *json.RawMessage, code int64, message string) map[string]any {
	response := map[string]any{
		"jsonrpc": "2.0",
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	}
	if id != nil {
		response["id"] = json.RawMessage(append([]byte(nil), (*id)...))
	} else {
		response["id"] = nil
	}
	return response
}

func requestIDPtr(id json.RawMessage) *json.RawMessage {
	if !hasRequestID(id) {
		return nil
	}
	return &id
}

func hasRequestID(id json.RawMessage) bool {
	return len(bytes.TrimSpace(id)) > 0
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func newMCPThreadID() string {
	var random [16]byte
	if _, err := rand.Read(random[:]); err == nil {
		random[6] = (random[6] & 0x0f) | 0x40
		random[8] = (random[8] & 0x3f) | 0x80
		encoded := hex.EncodeToString(random[:])
		return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32]
	}
	return fmt.Sprintf("thread-%d", time.Now().UTC().UnixNano())
}
