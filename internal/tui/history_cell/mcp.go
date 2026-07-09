package historycell

import (
	"sort"
	"strings"

	"codex_go/internal/tui"
)

// Rust parity: codex-rs/tui/src/history_cell/mcp.rs.

type McpInvocation struct {
	Server    string
	Tool      string
	Arguments string
}

type McpToolResult struct {
	Content []string
	Error   string
	IsError bool
}

type McpToolCallCell struct {
	CallID     string
	Invocation McpInvocation
	Result     *McpToolResult
}

func NewActiveMcpToolCall(callID string, invocation McpInvocation) McpToolCallCell {
	return McpToolCallCell{CallID: strings.TrimSpace(callID), Invocation: invocation}
}

func NewMcpToolCall(callID string, invocation McpInvocation, result McpToolResult) McpToolCallCell {
	return McpToolCallCell{
		CallID:     strings.TrimSpace(callID),
		Invocation: invocation,
		Result:     cloneMcpToolResult(&result),
	}
}

func (c *McpToolCallCell) Complete(result McpToolResult) {
	if c == nil {
		return
	}
	c.Result = cloneMcpToolResult(&result)
}

func (c *McpToolCallCell) MarkFailed(message string) {
	if c == nil {
		return
	}
	c.Result = &McpToolResult{Error: firstNonEmptyHistory(strings.TrimSpace(message), "interrupted"), IsError: true}
}

func (c McpToolCallCell) DisplayLines(width int) []string {
	width = max(width, 1)
	header := "Calling"
	if c.Result != nil {
		header = "Called"
	}
	line := "\u2022 " + header + " " + formatMcpInvocation(c.Invocation)
	lines := tui.AdaptiveWrapLine(line, tui.WrapOptions{
		Width:            width,
		SubsequentIndent: "  ",
		BreakWords:       true,
	})
	for _, detail := range c.detailLines(width) {
		lines = append(lines, tui.AdaptiveWrapLine(detail, tui.WrapOptions{
			Width:            width,
			InitialIndent:    "  \u2514 ",
			SubsequentIndent: "    ",
			BreakWords:       true,
		})...)
	}
	return lines
}

func (c McpToolCallCell) RawLines() []string {
	header := "Calling"
	if c.Result != nil {
		header = "Called"
	}
	lines := []string{header + " " + formatMcpInvocation(c.Invocation)}
	lines = append(lines, c.detailLines(120)...)
	return lines
}

func (c McpToolCallCell) detailLines(width int) []string {
	if c.Result == nil {
		return nil
	}
	if strings.TrimSpace(c.Result.Error) != "" {
		return []string{"Error: " + strings.TrimSpace(c.Result.Error)}
	}
	out := []string{}
	for _, block := range c.Result.Content {
		block = strings.TrimRight(block, "\r\n")
		if block == "" {
			continue
		}
		out = append(out, truncateToolResultLines(block, width)...)
	}
	return out
}

func NewCompletedMcpToolCallWithImageOutput() PlainHistoryCell {
	return NewPlainHistoryCell([]string{"tool result (image output)"})
}

func EmptyMCPOutput() PlainHistoryCell {
	return NewPlainHistoryCell([]string{
		"/mcp",
		"",
		"\U0001f50c  MCP Tools",
		"",
		"  \u2022 No MCP servers configured.",
		"    See the MCP docs to configure them: https://developers.openai.com/codex/mcp",
	})
}

type McpServerStatus struct {
	Name              string
	Auth              string
	Tools             []string
	Resources         []McpResource
	ResourceTemplates []McpResourceTemplate
}

type McpResource struct {
	Name  string
	Title string
	URI   string
}

type McpResourceTemplate struct {
	Name        string
	Title       string
	URITemplate string
}

func NewMCPToolsOutputFromStatuses(statuses []McpServerStatus, detail bool) PlainHistoryCell {
	lines := []string{"/mcp", "", "\U0001f50c  MCP Tools", ""}
	statuses = append([]McpServerStatus(nil), statuses...)
	sort.Slice(statuses, func(i, j int) bool {
		return statuses[i].Name < statuses[j].Name
	})
	hasTools := false
	for _, status := range statuses {
		if len(status.Tools) > 0 {
			hasTools = true
			break
		}
	}
	if !hasTools {
		lines = append(lines, "  \u2022 No MCP tools available.", "")
	}
	for _, status := range statuses {
		lines = append(lines, "  \u2022 "+strings.TrimSpace(status.Name))
		lines = append(lines, "    \u2022 Auth: "+firstNonEmptyHistory(strings.TrimSpace(status.Auth), "Unsupported"))
		tools := append([]string(nil), status.Tools...)
		sort.Strings(tools)
		if len(tools) == 0 {
			lines = append(lines, "    \u2022 Tools: (none)")
		} else {
			lines = append(lines, "    \u2022 Tools: "+strings.Join(tools, ", "))
		}
		if detail {
			lines = append(lines, "    \u2022 Resources: "+formatMcpResources(status.Resources))
			lines = append(lines, "    \u2022 Resource templates: "+formatMcpResourceTemplates(status.ResourceTemplates))
		}
		lines = append(lines, "")
	}
	return NewPlainHistoryCell(lines)
}

func NewMcpInventoryLoading() PlainHistoryCell {
	return NewPlainHistoryCell([]string{"\u2022 Loading MCP inventory..."})
}

func formatMcpInvocation(invocation McpInvocation) string {
	args := strings.TrimSpace(invocation.Arguments)
	return strings.TrimSpace(invocation.Server) + "." + strings.TrimSpace(invocation.Tool) + "(" + args + ")"
}

func formatMcpResources(resources []McpResource) string {
	if len(resources) == 0 {
		return "(none)"
	}
	parts := make([]string, 0, len(resources))
	for _, resource := range resources {
		label := firstNonEmptyHistory(strings.TrimSpace(resource.Title), strings.TrimSpace(resource.Name))
		if strings.TrimSpace(resource.URI) != "" {
			label += " (" + strings.TrimSpace(resource.URI) + ")"
		}
		parts = append(parts, label)
	}
	sort.Strings(parts)
	return strings.Join(parts, ", ")
}

func formatMcpResourceTemplates(templates []McpResourceTemplate) string {
	if len(templates) == 0 {
		return "(none)"
	}
	parts := make([]string, 0, len(templates))
	for _, template := range templates {
		label := firstNonEmptyHistory(strings.TrimSpace(template.Title), strings.TrimSpace(template.Name))
		if strings.TrimSpace(template.URITemplate) != "" {
			label += " (" + strings.TrimSpace(template.URITemplate) + ")"
		}
		parts = append(parts, label)
	}
	sort.Strings(parts)
	return strings.Join(parts, ", ")
}

func truncateToolResultLines(text string, width int) []string {
	const maxLines = 50
	lines := rawLinesFromSource(text)
	if len(lines) > maxLines {
		lines = append(lines[:maxLines], "... truncated")
	}
	out := []string{}
	for _, line := range lines {
		out = append(out, tui.AdaptiveWrapLine(line, tui.WrapOptions{Width: max(width, 1), BreakWords: true})...)
	}
	return out
}

func cloneMcpToolResult(result *McpToolResult) *McpToolResult {
	if result == nil {
		return nil
	}
	return &McpToolResult{
		Content: append([]string(nil), result.Content...),
		Error:   result.Error,
		IsError: result.IsError,
	}
}

func firstNonEmptyHistory(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
