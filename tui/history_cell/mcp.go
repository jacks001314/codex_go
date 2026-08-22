package historycell

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	"codex_go/tui"
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
	return c.renderLines(width, false)
}

// TranscriptLines renders the full invocation and result used by the Ctrl+T
// transcript view (Rust #38044): node_repl.js calls keep their complete
// invocation and untruncated output in transcript mode.
func (c McpToolCallCell) TranscriptLines(width int) []string {
	return c.renderLines(width, true)
}

func (c McpToolCallCell) renderLines(width int, transcript bool) []string {
	width = max(width, 1)
	header := "Calling"
	if c.Result != nil {
		header = "Called"
	}
	headerLine := "\u2022 " + header
	nodeREPL := c.Invocation.Server == "node_repl" && c.Invocation.Tool == "js"
	compact := nodeREPL && !transcript
	invocation := formatMcpInvocation(c.Invocation)
	if compact {
		invocation = mcpNodeREPLTitle(c.Invocation.Arguments)
	}
	inlineInvocation := tui.DisplayWidth(headerLine+" "+invocation) <= width
	lines := []string{}
	if inlineInvocation {
		lines = append(lines, headerLine+" "+invocation)
	} else {
		lines = append(lines, headerLine)
		lines = append(lines, tui.AdaptiveWrapLine(invocation, tui.WrapOptions{
			Width:            width,
			InitialIndent:    "  \u2514 ",
			SubsequentIndent: "    ",
			BreakWords:       true,
		})...)
	}
	detailPrefix := "  \u2514 "
	if !inlineInvocation {
		detailPrefix = "    "
	}
	for _, detail := range c.detailLines(width, nodeREPL, compact) {
		lines = append(lines, tui.AdaptiveWrapLine(detail, tui.WrapOptions{
			Width:            width,
			InitialIndent:    detailPrefix,
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
	nodeREPL := c.Invocation.Server == "node_repl" && c.Invocation.Tool == "js"
	lines = append(lines, c.detailLines(120, nodeREPL, false)...)
	return lines
}

func (c McpToolCallCell) detailLines(width int, nodeREPL bool, compact bool) []string {
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
		if nodeREPL && !compact && !c.Result.IsError {
			// Transcript/raw mode: full untruncated output.
			for _, segment := range strings.Split(strings.TrimRight(block, "\n"), "\n") {
				out = append(out, segment)
			}
			continue
		}
		if compact && !c.Result.IsError {
			if meaningful, ok := mcpNodeREPLMeaningfulOutput(block); ok {
				if meaningful == "" {
					continue
				}
				out = append(out, truncateToolResultLines(meaningful, width)...)
				continue
			}
		}
		out = append(out, truncateToolResultLines(block, width)...)
	}
	return out
}

// mcpNodeREPLTitle extracts the short title for a compact node_repl.js history
// entry (Rust #38044): the arguments "title" field, whitespace-normalized and
// truncated to 80 runes, falling back to "node_repl.js".
func mcpNodeREPLTitle(arguments string) string {
	var args map[string]any
	if err := json.Unmarshal([]byte(arguments), &args); err == nil {
		if title, ok := args["title"].(string); ok {
			title = strings.Join(strings.Fields(title), " ")
			if title != "" {
				runes := []rune(title)
				if len(runes) > 80 {
					title = string(runes[:80])
				}
				return title
			}
		}
	}
	return "node_repl.js"
}

// mcpNodeREPLMeaningfulOutput extracts the meaningful output of a successful
// node_repl.js execution (Rust #38044): the portion after "Script completed /
// Output:" or the "output" field of a zero-exit-code NodeReplExecOutput JSON
// payload. The second result reports whether the block is a node_repl
// structured result at all.
func mcpNodeREPLMeaningfulOutput(text string) (string, bool) {
	if strings.HasPrefix(text, "Script completed\n") {
		if _, after, ok := strings.Cut(text, "\nOutput:\n"); ok {
			return after, true
		}
		return "", false
	}
	var output struct {
		ExitCode int    `json:"exit_code"`
		Output   string `json:"output"`
	}
	if err := json.Unmarshal([]byte(text), &output); err == nil && output.ExitCode == 0 {
		return output.Output, true
	}
	return "", false
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
	RuntimeStatus     string
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

type McpToolsOutputCell struct {
	Lines []string
}

func (c McpToolsOutputCell) DisplayLines(width int) []string {
	width = max(width, 1)
	out := make([]string, 0, len(c.Lines))
	for _, line := range c.Lines {
		out = append(out, wrapMcpInventoryLine(line, width)...)
	}
	return out
}

func (c McpToolsOutputCell) RawLines() []string {
	return plainLines(c.Lines)
}

func NewMCPToolsOutputFromStatuses(statuses []McpServerStatus, detail bool) McpToolsOutputCell {
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
	if !hasTools && detail {
		lines = append(lines, "  \u2022 No MCP tools available.", "")
	}
	for _, status := range statuses {
		label := mcpRuntimeStatusLabel(status.RuntimeStatus, status.Auth)
		count := len(status.Tools)
		unit := "tools"
		if count == 1 {
			unit = "tool"
		}
		lines = append(lines, "  \u2022 "+strings.TrimSpace(status.Name)+": "+label+" ("+strconv.Itoa(count)+" "+unit+")")
		if !detail {
			continue
		}
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
	if !detail {
		lines = append(lines, "")
		lines = append(lines, "  Use /mcp verbose for tools and resources.")
	}
	return McpToolsOutputCell{Lines: lines}
}

// mcpRuntimeStatusLabel renders the connection state label for the compact
// /mcp view (Rust #40068). A server with no runtimeStatus falls back to the
// authentication state, and otherwise reports an unknown state.
func mcpRuntimeStatusLabel(runtimeStatus string, auth string) string {
	switch runtimeStatus {
	case "connected":
		return "connected"
	case "starting":
		return "starting"
	case "authenticationRequired":
		return "authentication required"
	case "failed":
		return "failed"
	case "notStarted":
		return "not started"
	case "disabled":
		return "disabled"
	case "cancelled":
		return "cancelled"
	}
	if auth == "Not logged in" {
		return "authentication required"
	}
	return "unknown"
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
	return strings.Join(parts, ", ")
}

func wrapMcpInventoryLine(line string, width int) []string {
	for _, prefix := range []string{
		"    \u2022 Tools: ",
		"    \u2022 Resources: ",
		"    \u2022 Resource templates: ",
	} {
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		return tui.AdaptiveWrapLine(strings.TrimPrefix(line, prefix), tui.WrapOptions{
			Width:            width,
			InitialIndent:    prefix,
			SubsequentIndent: strings.Repeat(" ", len([]rune(prefix))),
			BreakWords:       true,
		})
	}
	return []string{line}
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
