package telemetry

import "context"

const (
	CodexOnboardingExternalAgentImportCompleteEventType = "codex_onboarding_external_agent_import_complete"
	CodexOnboardingExternalAgentImportFailureEventType  = "codex_onboarding_external_agent_import_failure"
)

type ExternalAgentConfigImportEventSink interface {
	TrackCodexOnboardingExternalAgentImportCompleteEvent(context.Context, CodexOnboardingExternalAgentImportCompleteEventRequest)
	TrackCodexOnboardingExternalAgentImportFailureEvent(context.Context, CodexOnboardingExternalAgentImportFailureEventRequest)
}

type CodexOnboardingExternalAgentImportCompleteEventRequest struct {
	EventType   string                                             `json:"event_type"`
	EventParams CodexOnboardingExternalAgentImportCompleteMetadata `json:"event_params"`
}

type CodexOnboardingExternalAgentImportCompleteMetadata struct {
	ImportID        string  `json:"import_id"`
	Source          string  `json:"source"`
	ItemType        string  `json:"type"`
	SuccessCount    int     `json:"success_count"`
	FailedCount     int     `json:"failed_count"`
	ProductClientID *string `json:"product_client_id"`
}

type CodexOnboardingExternalAgentImportFailureEventRequest struct {
	EventType   string                                            `json:"event_type"`
	EventParams CodexOnboardingExternalAgentImportFailureMetadata `json:"event_params"`
}

type CodexOnboardingExternalAgentImportFailureMetadata struct {
	ImportID        string  `json:"import_id"`
	Source          string  `json:"source"`
	ItemType        string  `json:"type"`
	FailureStage    string  `json:"failure_stage"`
	ErrorType       string  `json:"error_type"`
	ProductClientID *string `json:"product_client_id"`
}

func NewCodexOnboardingExternalAgentImportCompleteEvent(metadata CodexOnboardingExternalAgentImportCompleteMetadata) CodexOnboardingExternalAgentImportCompleteEventRequest {
	return CodexOnboardingExternalAgentImportCompleteEventRequest{
		EventType:   CodexOnboardingExternalAgentImportCompleteEventType,
		EventParams: metadata,
	}
}

func NewCodexOnboardingExternalAgentImportFailureEvent(metadata CodexOnboardingExternalAgentImportFailureMetadata) CodexOnboardingExternalAgentImportFailureEventRequest {
	return CodexOnboardingExternalAgentImportFailureEventRequest{
		EventType:   CodexOnboardingExternalAgentImportFailureEventType,
		EventParams: metadata,
	}
}
