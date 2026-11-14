package tea

import (
	"strings"

	bubbletea "charm.land/bubbletea/v2"

	historycell "codex_go/tui/history_cell"
)

func (m *Model) applyDebugConfigCommand() bubbletea.Cmd {
	if m == nil {
		return nil
	}
	if m.onReadDebugConfig == nil {
		m.applyDebugConfigLines(m.defaultDebugConfigLines())
		return nil
	}
	m.refreshTranscript()
	return func() bubbletea.Msg {
		lines, err := m.onReadDebugConfig()
		return DebugConfigResultMsg{Lines: lines, Err: err}
	}
}

func (m *Model) applyDebugConfigResult(msg DebugConfigResultMsg) {
	if m == nil {
		return
	}
	if msg.Err != nil {
		m.applyDebugConfigLines([]string{
			"/debug-config",
			"",
			"Failed to read debug config: " + msg.Err.Error(),
		})
		return
	}
	m.applyDebugConfigLines(msg.Lines)
}

func (m *Model) applyDebugConfigLines(lines []string) {
	if m == nil {
		return
	}
	clean := make([]string, 0, len(lines))
	for _, line := range lines {
		clean = append(clean, strings.TrimRight(line, "\r\n"))
	}
	if len(clean) == 0 {
		clean = m.defaultDebugConfigLines()
	}
	m.State.AddHistoryLines(clean, clean)
	m.dismissWelcomeBlossom()
	m.refreshTranscript()
}

func (m *Model) defaultDebugConfigLines() []string {
	state := m.State
	value := func(raw string) string {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return "default"
		}
		return raw
	}
	threadID := "new"
	status := "idle"
	model := "default"
	reasoning := "default"
	approval := "default"
	sandbox := "default"
	personality := "default"
	if state != nil {
		threadID = value(state.ThreadID)
		status = value(state.Status)
		model = value(state.Model)
		reasoning = value(state.EffectiveReasoningEffort())
		approval = value(state.ApprovalPolicy)
		sandbox = value(state.Sandbox)
		personality = value(state.Personality)
	}
	return []string{
		"/debug-config",
		"",
		"Config layer stack (lowest precedence first):",
		"  <unavailable in embedded Tea model>",
		"",
		"Requirements:",
		"  <unavailable in embedded Tea model>",
		"",
		"Session:",
		"  - thread: " + threadID,
		"  - status: " + status,
		"  - model: " + model,
		"  - reasoning: " + reasoning,
		"  - approval: " + approval,
		"  - sandbox: " + sandbox,
		"  - personality: " + personality,
	}
}

// parseMCPLoginArguments accepts `/mcp login <name>` with an optional
// `--environment <id>` selector (Rust #61257).
func parseMCPLoginArguments(args string) (string, string, bool) {
	fields := strings.Fields(args)
	if len(fields) == 0 {
		return "", "", false
	}
	name := strings.TrimSpace(fields[0])
	environmentID := ""
	for i := 1; i < len(fields); i++ {
		switch {
		case fields[i] == "--environment":
			if i+1 >= len(fields) {
				return "", "", false
			}
			environmentID = strings.TrimSpace(fields[i+1])
			i++
		case strings.HasPrefix(fields[i], "--environment="):
			environmentID = strings.TrimSpace(strings.TrimPrefix(fields[i], "--environment="))
		default:
			return "", "", false
		}
	}
	return name, environmentID, true
}

func (m *Model) applyMCPCommand(args string) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	detail := false
	trimmed := strings.TrimSpace(args)
	switch {
	case trimmed == "":
	case strings.EqualFold(trimmed, "verbose"):
		detail = true
	case len(trimmed) > len("login") &&
		strings.EqualFold(trimmed[:len("login")], "login") &&
		(trimmed[len("login")] == ' ' || trimmed[len("login")] == '\t'):
		name, environmentID, ok := parseMCPLoginArguments(trimmed[len("login"):])
		if !ok || name == "" {
			m.notice = "Usage: /mcp [verbose | login <name> [--environment <id>]]"
			m.refreshTranscript()
			return nil
		}
		return m.startMCPLogin(name, environmentID)
	default:
		m.notice = "Usage: /mcp [verbose | login <name> [--environment <id>]]"
		m.refreshTranscript()
		return nil
	}
	if m.onReadMCPInventory != nil {
		m.clearPendingMCPInventoryLoading()
		m.nextMCPInventoryRequestID++
		requestID := m.nextMCPInventoryRequestID
		m.pendingMCPInventoryRequestID = requestID
		m.pendingMCPInventoryMessageIndex = len(m.State.Messages)
		m.pendingMCPInventoryDetail = detail
		m.applyHistoryCell(historycell.NewMcpInventoryLoading())
		return func() bubbletea.Msg {
			servers, err := m.onReadMCPInventory(detail)
			return MCPInventoryResultMsg{RequestID: requestID, Servers: servers, Err: err}
		}
	}
	if len(m.mcpServers) == 0 {
		m.applyHistoryCell(historycell.EmptyMCPOutput())
		return nil
	}
	m.applyHistoryCell(historycell.NewMCPToolsOutputFromStatuses(m.mcpServers, detail))
	return nil
}

func (m *Model) applyMCPInventoryResult(msg MCPInventoryResultMsg) {
	if m == nil || msg.RequestID == 0 || msg.RequestID != m.pendingMCPInventoryRequestID {
		return
	}
	detail := m.pendingMCPInventoryDetail
	m.clearPendingMCPInventoryLoading()
	if msg.Err != nil {
		m.notice = "Failed to load MCP inventory: " + msg.Err.Error()
		m.addErrorHistoryMessage(m.notice)
		m.refreshTranscript()
		return
	}
	m.mcpServers = cloneMcpServerStatuses(msg.Servers)
	if len(m.mcpServers) == 0 {
		m.applyHistoryCell(historycell.EmptyMCPOutput())
	} else {
		m.applyHistoryCell(historycell.NewMCPToolsOutputFromStatuses(m.mcpServers, detail))
	}
}

func (m *Model) clearPendingMCPInventoryLoading() {
	if m == nil {
		return
	}
	index := m.pendingMCPInventoryMessageIndex
	if index >= 0 && index < len(m.State.Messages) {
		m.State.Messages = append(m.State.Messages[:index], m.State.Messages[index+1:]...)
		m.State.BumpMessagesRevision()
	}
	m.pendingMCPInventoryRequestID = 0
	m.pendingMCPInventoryMessageIndex = -1
	m.pendingMCPInventoryDetail = false
	m.refreshTranscript()
}

func cloneMcpServerStatuses(values []historycell.McpServerStatus) []historycell.McpServerStatus {
	if values == nil {
		return nil
	}
	out := make([]historycell.McpServerStatus, len(values))
	for i := range values {
		out[i] = historycell.McpServerStatus{
			Name:              values[i].Name,
			RuntimeStatus:     values[i].RuntimeStatus,
			Auth:              values[i].Auth,
			Tools:             append([]string(nil), values[i].Tools...),
			Resources:         append([]historycell.McpResource(nil), values[i].Resources...),
			ResourceTemplates: append([]historycell.McpResourceTemplate(nil), values[i].ResourceTemplates...),
		}
	}
	return out
}
