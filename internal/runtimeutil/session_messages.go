package runtimeutil

import (
	"fmt"
	"strings"
)

const (
	CompletionMessageMaxRunes = 4000
	ErrorNextAction           = "This agent's turn failed. If you still need this agent, use the available collaboration tools to give it another task."
)

type AgentStatusType string

const (
	AgentPendingInit AgentStatusType = "pendingInit"
	AgentRunning     AgentStatusType = "running"
	AgentInterrupted AgentStatusType = "interrupted"
	AgentCompleted   AgentStatusType = "completed"
	AgentErrored     AgentStatusType = "errored"
	AgentShutdown    AgentStatusType = "shutdown"
	AgentNotFound    AgentStatusType = "notFound"
)

type AgentStatus struct {
	Type    AgentStatusType
	Message string
}

func FormatSubagentNotificationMessage(agentReference string, status *AgentStatus) string {
	if status == nil {
		return fmt.Sprintf("<subagent_notification agent=%q status=%q />", agentReference, "")
	}
	if status.Message == "" {
		return fmt.Sprintf("<subagent_notification agent=%q status=%q />", agentReference, status.Type)
	}
	return fmt.Sprintf("<subagent_notification agent=%q status=%q>%s</subagent_notification>", agentReference, status.Type, status.Message)
}

func FormatInterAgentCompletionMessage(taskName string, sender string, status *AgentStatus) *string {
	if status == nil {
		return nil
	}
	var payload string
	switch status.Type {
	case AgentCompleted:
		payload = status.Message
	case AgentErrored:
		payload = "Agent errored: " + truncateRunes(status.Message, CompletionMessageMaxRunes) + "\n\n" + ErrorNextAction
	case AgentShutdown:
		payload = "Agent shut down."
	case AgentNotFound:
		payload = "Agent was not found."
	case AgentPendingInit, AgentRunning, AgentInterrupted:
		return nil
	default:
		return nil
	}
	message := fmt.Sprintf("<inter_agent_completion task=%q sender=%q>\n%s\n</inter_agent_completion>", taskName, sender, payload)
	return &message
}

func FormatSubagentContextLine(agentReference string, agentNickname *string) string {
	if agentNickname != nil && strings.TrimSpace(*agentNickname) != "" {
		return "- " + agentReference + ": " + strings.TrimSpace(*agentNickname)
	}
	return "- " + agentReference
}

func truncateRunes(value string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max])
}
