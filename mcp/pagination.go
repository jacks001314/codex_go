package mcp

import (
	"fmt"
	"strings"
)

const mcpListPaginationMaxPages = 1000

func mcpListParams(cursor string) map[string]any {
	cursor = strings.TrimSpace(cursor)
	if cursor == "" {
		return map[string]any{}
	}
	return map[string]any{"cursor": cursor}
}

func mcpNextCursor(cursor *string) string {
	if cursor == nil {
		return ""
	}
	return strings.TrimSpace(*cursor)
}

func mcpPaginationCursorError(method string, cursor string) error {
	cursor = strings.TrimSpace(cursor)
	if cursor == "" {
		return fmt.Errorf("MCP %s pagination did not finish", method)
	}
	return fmt.Errorf("MCP %s pagination repeated cursor %q", method, cursor)
}

func mcpPaginationPageLimitError(method string) error {
	return fmt.Errorf("MCP %s pagination exceeded %d pages", method, mcpListPaginationMaxPages)
}
