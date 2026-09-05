package telemetry

import (
	"context"
	"math"
	"sort"
	"strings"
)

const (
	CodexPluginInstalledEventType     = "codex_plugin_installed"
	CodexPluginUninstalledEventType   = "codex_plugin_uninstalled"
	CodexPluginEnabledEventType       = "codex_plugin_enabled"
	CodexPluginDisabledEventType      = "codex_plugin_disabled"
	CodexPluginInstallFailedEventType = "codex_plugin_install_failed"
	CodexPluginMeasurementEventType   = "codex_plugin_measurement_event"
)

type PluginEventSink interface {
	TrackCodexPluginInstalledEvent(context.Context, CodexPluginEventRequest)
	TrackCodexPluginUninstalledEvent(context.Context, CodexPluginEventRequest)
	TrackCodexPluginEnabledEvent(context.Context, CodexPluginEventRequest)
	TrackCodexPluginDisabledEvent(context.Context, CodexPluginEventRequest)
	TrackCodexPluginInstallFailedEvent(context.Context, CodexPluginInstallFailedEventRequest)
	TrackCodexPluginMeasurementsEvent(context.Context, CodexPluginMeasurementsInput)
}

type PluginMeasurementRow struct {
	MeasurementName string            `json:"measurement_name"`
	NumberValue     float64           `json:"number_value"`
	Dimensions      map[string]string `json:"dimensions,omitempty"`
}

type CodexPluginMeasurementsInput struct {
	ThreadID    string                 `json:"thread_id"`
	TurnID      string                 `json:"turn_id"`
	ItemID      string                 `json:"item_id"`
	PluginID    string                 `json:"plugin_id"`
	ExecutionID string                 `json:"execution_id"`
	Operation   string                 `json:"operation"`
	Originator  string                 `json:"originator,omitempty"`
	Rows        []PluginMeasurementRow `json:"rows"`
}

type CodexPluginMeasurementEventParams struct {
	ThreadID        string            `json:"thread_id"`
	TurnID          string            `json:"turn_id"`
	ItemID          string            `json:"item_id"`
	PluginID        string            `json:"plugin_id"`
	ExecutionID     string            `json:"execution_id"`
	Operation       string            `json:"operation"`
	Originator      string            `json:"originator,omitempty"`
	MeasurementName string            `json:"measurement_name"`
	NumberValue     float64           `json:"number_value"`
	Dimensions      map[string]string `json:"dimensions,omitempty"`
}

type CodexPluginMeasurementEventRequest struct {
	EventType   string                            `json:"event_type"`
	EventParams CodexPluginMeasurementEventParams `json:"event_params"`
}

const (
	maxPluginMeasurementsPerBatch       = 100
	maxPluginMeasurementDimensions      = 8
	maxPluginMeasurementIdentifierBytes = 64
)

func ValidPluginMeasurementIdentifier(value string) bool {
	if value == "" || len(value) > maxPluginMeasurementIdentifierBytes || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for index := 1; index < len(value); index++ {
		character := value[index]
		if !(character >= 'a' && character <= 'z') && !(character >= '0' && character <= '9') && character != '_' {
			return false
		}
	}
	return true
}

func ValidPluginMeasurementRow(row PluginMeasurementRow) bool {
	if math.IsNaN(row.NumberValue) || math.IsInf(row.NumberValue, 0) || !ValidPluginMeasurementIdentifier(row.MeasurementName) || len(row.Dimensions) > maxPluginMeasurementDimensions {
		return false
	}
	for name, value := range row.Dimensions {
		if !ValidPluginMeasurementIdentifier(name) || !ValidPluginMeasurementIdentifier(value) {
			return false
		}
	}
	return true
}

func PluginMeasurementEvents(input CodexPluginMeasurementsInput) []CodexPluginMeasurementEventRequest {
	if len(input.Rows) == 0 || len(input.Rows) > maxPluginMeasurementsPerBatch || !ValidPluginMeasurementIdentifier(input.Operation) {
		return nil
	}
	events := make([]CodexPluginMeasurementEventRequest, 0, len(input.Rows))
	for _, row := range input.Rows {
		if !ValidPluginMeasurementRow(row) {
			continue
		}
		dimensions := cloneStringMapTelemetry(row.Dimensions)
		if len(dimensions) == 0 {
			dimensions = nil
		}
		events = append(events, CodexPluginMeasurementEventRequest{
			EventType: CodexPluginMeasurementEventType,
			EventParams: CodexPluginMeasurementEventParams{
				ThreadID:        input.ThreadID,
				TurnID:          input.TurnID,
				ItemID:          input.ItemID,
				PluginID:        input.PluginID,
				ExecutionID:     input.ExecutionID,
				Operation:       input.Operation,
				Originator:      strings.TrimSpace(input.Originator),
				MeasurementName: row.MeasurementName,
				NumberValue:     row.NumberValue,
				Dimensions:      dimensions,
			},
		})
	}
	sort.SliceStable(events, func(i int, j int) bool {
		left, right := events[i].EventParams, events[j].EventParams
		if left.MeasurementName != right.MeasurementName {
			return left.MeasurementName < right.MeasurementName
		}
		return left.NumberValue < right.NumberValue
	})
	return events
}

func cloneStringMapTelemetry(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
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
	EventType   string              `json:"event_type"`
	EventParams CodexPluginMetadata `json:"event_params"`
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
