package agent

import (
	"fmt"
	"strings"
)

const (
	CompletionMessageMaxTokens            = 1000
	CompletionMessageEnvelopeTokenReserve = 100
	ErrorMaxTokens                        = CompletionMessageMaxTokens - CompletionMessageEnvelopeTokenReserve
	ErrorNextAction                       = "This agent's turn failed. If you still need this agent, use the available collaboration tools to give it another task."
)

type AgentMessageStatusKind string

const (
	AgentMessageStatusPendingInit AgentMessageStatusKind = "pending_init"
	AgentMessageStatusRunning     AgentMessageStatusKind = "running"
	AgentMessageStatusInterrupted AgentMessageStatusKind = "interrupted"
	AgentMessageStatusCompleted   AgentMessageStatusKind = "completed"
	AgentMessageStatusErrored     AgentMessageStatusKind = "errored"
	AgentMessageStatusShutdown    AgentMessageStatusKind = "shutdown"
	AgentMessageStatusNotFound    AgentMessageStatusKind = "not_found"
)

type AgentMessageStatus struct {
	Kind    AgentMessageStatusKind
	Message string
}

func (s *AgentMessageStatus) IsFinal() bool {
	if s == nil {
		return false
	}
	return s.Kind == AgentMessageStatusCompleted ||
		s.Kind == AgentMessageStatusErrored ||
		s.Kind == AgentMessageStatusShutdown ||
		s.Kind == AgentMessageStatusNotFound
}

func FormatSubagentNotificationMessage(agentReference string, status AgentMessageStatus) string {
	return strings.Join([]string{
		"<subagent_notification>",
		"agent: " + agentReference,
		"status: " + string(status.Kind),
		status.Message,
		"</subagent_notification>",
	}, "\n")
}

func FormatInterAgentCompletionMessage(taskName string, sender string, status AgentMessageStatus) (string, bool) {
	payload := ""
	switch status.Kind {
	case AgentMessageStatusCompleted:
		payload = status.Message
	case AgentMessageStatusErrored:
		payload = "Agent errored: " + truncateTokens(status.Message, ErrorMaxTokens) + "\n\n" + ErrorNextAction
	case AgentMessageStatusShutdown:
		payload = "Agent shut down."
	case AgentMessageStatusNotFound:
		payload = "Agent was not found."
	case AgentMessageStatusPendingInit, AgentMessageStatusRunning, AgentMessageStatusInterrupted:
		return "", false
	default:
		return "", false
	}
	return strings.Join([]string{
		"<inter_agent_completion>",
		"task: " + taskName,
		"sender: " + sender,
		"payload:",
		payload,
		"</inter_agent_completion>",
	}, "\n"), true
}

func FormatSubagentContextLine(agentReference string, agentNickname string) string {
	if strings.TrimSpace(agentNickname) != "" {
		return fmt.Sprintf("- %s: %s", agentReference, agentNickname)
	}
	return "- " + agentReference
}

func truncateTokens(value string, tokens int) string {
	maxChars := tokens * 4
	if maxChars <= 0 || len(value) <= maxChars {
		return value
	}
	return value[:maxChars] + "..."
}
