package tea

import (
	"strconv"
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"
)

type mcpLoginStartRequest struct {
	requestID string
	name      string
	threadID  string
}

func (m *Model) startMCPLogin(name string) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	name = strings.TrimSpace(name)
	threadID := ""
	if m.State != nil {
		threadID = strings.TrimSpace(m.State.ThreadID)
	}
	if threadID == "" {
		m.addErrorHistoryMessage("MCP sign-in requires an active session.")
		return nil
	}
	if m.pendingMCPLoginStart != nil {
		m.addInfoHistoryMessage("MCP sign-in is starting. Wait for it to finish before trying again.")
		m.refreshTranscript()
		return nil
	}
	if m.onStartMCPLogin == nil {
		m.addErrorHistoryMessage("MCP sign-in is unavailable.")
		return nil
	}
	m.nextMCPLoginRequestID++
	pending := &mcpLoginStartRequest{
		requestID: strconv.FormatUint(m.nextMCPLoginRequestID, 10),
		name:      name,
		threadID:  threadID,
	}
	m.pendingMCPLoginStart = pending
	return func() bubbletea.Msg {
		result, err := m.onStartMCPLogin(name, threadID)
		return MCPLoginStartedMsg{
			RequestID: pending.requestID,
			Name:      name,
			ThreadID:  threadID,
			Result:    result,
			Err:       err,
		}
	}
}

func (m *Model) applyMCPLoginStarted(message MCPLoginStartedMsg) bubbletea.Cmd {
	if m == nil || m.pendingMCPLoginStart == nil ||
		m.pendingMCPLoginStart.requestID != message.RequestID ||
		m.pendingMCPLoginStart.name != message.Name ||
		m.pendingMCPLoginStart.threadID != message.ThreadID {
		return nil
	}
	m.pendingMCPLoginStart = nil
	if message.Err != nil {
		m.addErrorHistoryMessage(message.Err.Error())
		return nil
	}
	if loginID := strings.TrimSpace(message.Result.LoginID); loginID != "" {
		m.activeMCPLoginIDs[message.Name] = loginID
	}
	if message.Result.Completions == nil {
		return nil
	}
	return waitMCPLoginCompletion(message.Name, message.ThreadID, message.Result.Completions)
}

func waitMCPLoginCompletion(name string, threadID string, completions <-chan MCPLoginCompletedMsg) bubbletea.Cmd {
	return func() bubbletea.Msg {
		result, ok := <-completions
		if !ok {
			return MCPLoginCompletedMsg{}
		}
		if strings.TrimSpace(result.Name) == "" {
			result.Name = name
		}
		if strings.TrimSpace(result.ThreadID) == "" {
			result.ThreadID = threadID
		}
		return result
	}
}

func (m *Model) applyMCPLoginCompleted(message MCPLoginCompletedMsg) bubbletea.Cmd {
	if m == nil || strings.TrimSpace(message.Name) == "" {
		return nil
	}
	if loginID := strings.TrimSpace(message.LoginID); loginID != "" {
		activeID, ok := m.activeMCPLoginIDs[message.Name]
		if !ok || activeID != loginID {
			return nil
		}
		delete(m.activeMCPLoginIDs, message.Name)
	}
	currentThreadID := ""
	if m.State != nil {
		currentThreadID = strings.TrimSpace(m.State.ThreadID)
	}
	originThreadID := strings.TrimSpace(message.ThreadID)
	if originThreadID != "" && originThreadID != currentThreadID {
		if m.mcpLoginResultsByThread == nil {
			m.mcpLoginResultsByThread = map[string][]MCPLoginCompletedMsg{}
		}
		m.mcpLoginResultsByThread[originThreadID] = append(m.mcpLoginResultsByThread[originThreadID], message)
		return nil
	}
	m.deliverMCPLoginCompletion(message)
	return nil
}

func (m *Model) deliverMCPLoginCompletion(message MCPLoginCompletedMsg) {
	if m == nil {
		return
	}
	if message.Success {
		m.addInfoHistoryMessage("Signed in to " + message.Name + ".")
		return
	}
	reason := strings.TrimSpace(message.Error)
	if reason == "" {
		reason = "MCP sign-in failed."
	}
	m.addErrorHistoryMessage(reason)
}

func (m *Model) flushMCPLoginResultsForCurrentThread() {
	if m == nil || m.State == nil || len(m.mcpLoginResultsByThread) == 0 {
		return
	}
	threadID := strings.TrimSpace(m.State.ThreadID)
	results := m.mcpLoginResultsByThread[threadID]
	if len(results) == 0 {
		return
	}
	delete(m.mcpLoginResultsByThread, threadID)
	for _, result := range results {
		m.deliverMCPLoginCompletion(result)
	}
}
