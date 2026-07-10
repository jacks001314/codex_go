package telemetry

import "context"

const (
	CodexPluginInstalledEventType     = "codex_plugin_installed"
	CodexPluginUninstalledEventType   = "codex_plugin_uninstalled"
	CodexPluginEnabledEventType       = "codex_plugin_enabled"
	CodexPluginDisabledEventType      = "codex_plugin_disabled"
	CodexPluginInstallFailedEventType = "codex_plugin_install_failed"
)

type PluginEventSink interface {
	TrackCodexPluginInstalledEvent(context.Context, CodexPluginEventRequest)
	TrackCodexPluginUninstalledEvent(context.Context, CodexPluginEventRequest)
	TrackCodexPluginEnabledEvent(context.Context, CodexPluginEventRequest)
	TrackCodexPluginDisabledEvent(context.Context, CodexPluginEventRequest)
	TrackCodexPluginInstallFailedEvent(context.Context, CodexPluginInstallFailedEventRequest)
}

type CodexPluginMetadata struct {
	PluginID        *string  `json:"plugin_id"`
	RemotePluginID  *string  `json:"remote_plugin_id"`
	PluginName      *string  `json:"plugin_name"`
	MarketplaceName *string  `json:"marketplace_name"`
	HasSkills       *bool    `json:"has_skills"`
	MCPServerCount  *int     `json:"mcp_server_count"`
	ConnectorIDs    []string `json:"connector_ids"`
	ProductClientID *string  `json:"product_client_id"`
}

type CodexPluginEventRequest struct {
	EventType   string               `json:"event_type"`
	EventParams CodexPluginMetadata  `json:"event_params"`
}

type CodexPluginInstallFailedMetadata struct {
	CodexPluginMetadata
	ErrorType string `json:"error_type"`
}

type CodexPluginInstallFailedEventRequest struct {
	EventType   string                           `json:"event_type"`
	EventParams CodexPluginInstallFailedMetadata `json:"event_params"`
}

func NewCodexPluginEvent(eventType string, metadata CodexPluginMetadata) CodexPluginEventRequest {
	return CodexPluginEventRequest{
		EventType:   firstNonEmptyTelemetry(eventType, CodexPluginInstalledEventType),
		EventParams: metadata,
	}
}

func NewCodexPluginInstallFailedEvent(metadata CodexPluginMetadata, errorType string) CodexPluginInstallFailedEventRequest {
	return CodexPluginInstallFailedEventRequest{
		EventType: CodexPluginInstallFailedEventType,
		EventParams: CodexPluginInstallFailedMetadata{
			CodexPluginMetadata: metadata,
			ErrorType:           firstNonEmptyTelemetry(errorType, "store_io"),
		},
	}
}
