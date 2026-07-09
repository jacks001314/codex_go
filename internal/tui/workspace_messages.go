package tui

import (
	"strings"
	"time"
)

// Rust parity: codex-rs/tui/src/workspace_messages.rs.

const WorkspaceHeadlineRefreshInterval = 5 * time.Minute

type WorkspaceMessageType string

const (
	WorkspaceMessageHeadline     WorkspaceMessageType = "headline"
	WorkspaceMessageAnnouncement WorkspaceMessageType = "announcement"
	WorkspaceMessageUnknown      WorkspaceMessageType = "unknown"
)

type WorkspaceMessage struct {
	Level string
	Text  string

	MessageID   string               `json:"messageId,omitempty"`
	MessageType WorkspaceMessageType `json:"messageType,omitempty"`
	MessageBody string               `json:"messageBody,omitempty"`
	CreatedAt   *int64               `json:"createdAt,omitempty"`
	ArchivedAt  *int64               `json:"archivedAt,omitempty"`
}

type GetWorkspaceMessagesResponse struct {
	FeatureEnabled bool               `json:"featureEnabled"`
	Messages       []WorkspaceMessage `json:"messages"`
}

type WorkspaceHeadlineFetchKind string

const (
	WorkspaceHeadlineFetchAvailable       WorkspaceHeadlineFetchKind = "available"
	WorkspaceHeadlineFetchFeatureDisabled WorkspaceHeadlineFetchKind = "feature_disabled"
)

type WorkspaceHeadlineFetchResult struct {
	Kind     WorkspaceHeadlineFetchKind
	Headline *string
}

func WorkspaceHeadlineFromResponse(response GetWorkspaceMessagesResponse) WorkspaceHeadlineFetchResult {
	if !response.FeatureEnabled {
		return WorkspaceHeadlineFetchResult{Kind: WorkspaceHeadlineFetchFeatureDisabled}
	}
	for _, message := range response.Messages {
		if message.MessageType != WorkspaceMessageHeadline {
			continue
		}
		headline := strings.TrimSpace(message.MessageBody)
		if headline == "" {
			continue
		}
		return WorkspaceHeadlineFetchResult{
			Kind:     WorkspaceHeadlineFetchAvailable,
			Headline: stringPtr(headline),
		}
	}
	return WorkspaceHeadlineFetchResult{Kind: WorkspaceHeadlineFetchAvailable}
}
