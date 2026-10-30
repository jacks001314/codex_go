package agentboard

// MCP transport adapter over the shared board's existing schemas, validation,
// permission checks, budgets, and handlers.
//
// Rust parity: codex-rs/message-board-mcp/src/lib.rs (#58341). The host binds
// board access and caller identity before serving a connection and owns the MCP
// transport, authentication, server capabilities, and notifications; this
// adapter only maps tool discovery and calls onto the existing executors.

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"codex_go/agent"
	"codex_go/tool"
)

// JSON-RPC error codes the adapter reports for protocol failures, matching
// rmcp's ErrorData::invalid_params and ErrorData::internal_error.
const (
	MessageBoardMCPInvalidParamsCode = -32602
	MessageBoardMCPInternalErrorCode = -32603
)

// MessageBoardMCPError is a protocol-level failure. A host must report it as
// the JSON-RPC error for the request instead of a tool result.
type MessageBoardMCPError struct {
	Code    int
	Message string
}

func (e *MessageBoardMCPError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func messageBoardMCPInvalidParams(message string) *MessageBoardMCPError {
	return &MessageBoardMCPError{Code: MessageBoardMCPInvalidParamsCode, Message: message}
}

func messageBoardMCPInternalError(message string) *MessageBoardMCPError {
	return &MessageBoardMCPError{Code: MessageBoardMCPInternalErrorCode, Message: message}
}

// MessageBoardMCPTool is one entry of an MCP tools/list result.
type MessageBoardMCPTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"inputSchema"`
}

// MessageBoardMCPContent is one content block of an MCP tools/call result.
type MessageBoardMCPContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// MessageBoardMCPToolResult is a completed MCP tools/call result. Results that
// are JSON objects carry structuredContent alongside their JSON text; other
// results (for example upsert_channel's null) are text only because legacy MCP
// structuredContent must be an object.
type MessageBoardMCPToolResult struct {
	Content           []MessageBoardMCPContent `json:"content"`
	StructuredContent map[string]any           `json:"structuredContent,omitempty"`
	IsError           bool                     `json:"isError"`
}

// MessageBoardMCPServer is a caller-bound selection of message-board tools
// exposed through the MCP tool protocol.
//
// Create one instance per MCP session. JSON-RPC request IDs are connection
// local, so the adapter scopes board request deduplication to its own
// invocation scope and reused IDs on a new connection never collide with
// earlier posts. The arguments never select an identity, and selecting tools
// does not grant permissions the board connection lacks.
type MessageBoardMCPServer struct {
	tools           []tool.Executor
	invocationScope string
}

// NewMessageBoardMCPServer binds a board connection, caller identity, and tool
// selection to one MCP session.
func NewMessageBoardMCPServer(selected []MessageBoardTool, board Board, caller string, callerPath agent.AgentPath) *MessageBoardMCPServer {
	scope := uuid.NewString()
	if id, err := uuid.NewV7(); err == nil {
		scope = id.String()
	}
	return &MessageBoardMCPServer{
		tools:           NewMessageBoardToolsWithSelection(board, selected, caller, callerPath, "", "", nil),
		invocationScope: scope,
	}
}

// ListTools returns every selected tool with the schema the shared executors
// publish. Message-board tools do not paginate, so a cursor is a protocol
// error rather than an ignored request.
func (s *MessageBoardMCPServer) ListTools(cursor *string) ([]MessageBoardMCPTool, error) {
	if s == nil {
		return nil, messageBoardMCPInternalError("Message-board MCP server is unavailable")
	}
	if cursor != nil {
		return nil, messageBoardMCPInvalidParams("Message-board tools do not use pagination cursors")
	}
	tools := make([]MessageBoardMCPTool, 0, len(s.tools))
	for _, executor := range s.tools {
		if executor == nil {
			return nil, messageBoardMCPInternalError("Message-board tool is not a function")
		}
		spec := executor.Spec()
		if spec.Freeform != nil {
			return nil, messageBoardMCPInternalError("Message-board tool is not a function")
		}
		if len(spec.InputSchema) == 0 {
			return nil, messageBoardMCPInternalError("Invalid message-board schema")
		}
		tools = append(tools, MessageBoardMCPTool{
			Name:        spec.Name.Name,
			Description: spec.Description,
			InputSchema: spec.InputSchema,
		})
	}
	return tools, nil
}

// CallTool runs one selected tool with host-bound identity. Validation and
// permission failures come back as tool errors; unknown tools, malformed
// request IDs, and fatal handler failures are protocol errors.
func (s *MessageBoardMCPServer) CallTool(ctx context.Context, name string, arguments map[string]any, requestID any) (*MessageBoardMCPToolResult, error) {
	if s == nil {
		return nil, messageBoardMCPInternalError("Message-board MCP server is unavailable")
	}
	var executor tool.Executor
	var spec tool.Spec
	for _, candidate := range s.tools {
		if candidate == nil {
			continue
		}
		candidateSpec := candidate.Spec()
		if candidateSpec.Name.Name == name {
			executor = candidate
			spec = candidateSpec
			break
		}
	}
	if executor == nil {
		return nil, messageBoardMCPInvalidParams("Unknown message-board tool")
	}
	callID, err := messageBoardMCPCallID(requestID)
	if err != nil {
		return nil, err
	}
	if arguments == nil {
		arguments = map[string]any{}
	}
	encoded, err := json.Marshal(arguments)
	if err != nil {
		return nil, messageBoardMCPInvalidParams("Invalid message-board arguments")
	}
	invocation := &tool.Invocation{
		CallID:    callID,
		ToolName:  spec.Name,
		Payload:   tool.Payload{Kind: tool.PayloadFunction, Arguments: string(encoded)},
		StartedAt: time.Now(),
		Context:   map[string]any{"turn_id": s.invocationScope},
	}
	output, err := executor.Execute(ctx, invocation)
	if err != nil {
		callErr := tool.FromError(err)
		if callErr == nil || callErr.IsFatal() {
			return nil, messageBoardMCPInternalError("Message-board tool failed")
		}
		return messageBoardMCPToolError(callErr.ModelMessage()), nil
	}
	if output == nil {
		return nil, messageBoardMCPInternalError("Message-board tool returned no result")
	}
	result := &MessageBoardMCPToolResult{
		Content: []MessageBoardMCPContent{{Type: "text", Text: output.Body}},
		IsError: !output.Success,
	}
	var decoded any
	if err := json.Unmarshal([]byte(output.Body), &decoded); err == nil {
		if object, ok := decoded.(map[string]any); ok {
			result.StructuredContent = object
		}
	}
	return result, nil
}

func messageBoardMCPToolError(message string) *MessageBoardMCPToolResult {
	return &MessageBoardMCPToolResult{
		Content: []MessageBoardMCPContent{{Type: "text", Text: message}},
		IsError: true,
	}
}

// messageBoardMCPCallID serializes the JSON-RPC request ID so numeric and
// string IDs stay distinct, exactly as Rust serializes rmcp's RequestId.
func messageBoardMCPCallID(requestID any) (string, error) {
	switch requestID.(type) {
	case string, json.Number,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64:
	default:
		return "", messageBoardMCPInternalError("Invalid MCP request ID")
	}
	encoded, err := json.Marshal(requestID)
	if err != nil {
		return "", messageBoardMCPInternalError("Invalid MCP request ID")
	}
	return string(encoded), nil
}
