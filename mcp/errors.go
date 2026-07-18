package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

var ErrMCPRemote = errors.New("mcp remote error")

type MCPRemoteError struct {
	Method  string          `json:"method,omitempty"`
	Code    int64           `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *MCPRemoteError) Error() string {
	if e == nil {
		return ""
	}
	if strings.TrimSpace(e.Message) != "" {
		return fmt.Sprintf("MCP %d: %s", e.Code, e.Message)
	}
	return fmt.Sprintf("MCP %d", e.Code)
}

func (e *MCPRemoteError) Unwrap() error {
	return ErrMCPRemote
}

func (e *MCPRemoteError) JSONRPCErrorData() map[string]any {
	if e == nil {
		return nil
	}
	data := map[string]any{
		"type": "mcp_remote_error",
		"code": e.Code,
	}
	if method := strings.TrimSpace(e.Method); method != "" {
		data["method"] = method
	}
	if message := strings.TrimSpace(e.Message); message != "" {
		data["message"] = message
	}
	if raw := bytes.TrimSpace(e.Data); len(raw) > 0 {
		var decoded any
		if err := json.Unmarshal(raw, &decoded); err == nil {
			data["data"] = decoded
		} else {
			data["data"] = string(raw)
		}
	}
	return data
}

func newMCPRemoteError(method string, rpcErr *stdioRPCError) error {
	if rpcErr == nil {
		return nil
	}
	return &MCPRemoteError{
		Method:  strings.TrimSpace(method),
		Code:    rpcErr.Code,
		Message: rpcErr.Message,
		Data:    append(json.RawMessage(nil), rpcErr.Data...),
	}
}
