package telemetry

import "context"

const ArtifactOperationEventType = "codex_artifact_operation"

const (
	ArtifactOperationLifecycleStarted   = "started"
	ArtifactOperationLifecycleCompleted = "completed"
)

type ArtifactOperationEventSink interface {
	TrackArtifactOperationEvent(context.Context, ArtifactOperationEventRequest)
}

type ArtifactOperationEventRequest struct {
	EventType   string                       `json:"event_type"`
	EventParams ArtifactOperationEventParams `json:"event_params"`
}

// ArtifactOperationEventParams mirrors the Rust codex_artifact_operation event
// payload (Rust #38057).
type ArtifactOperationEventParams struct {
	ThreadID            string               `json:"thread_id"`
	TurnID              string               `json:"turn_id"`
	ItemID              string               `json:"item_id"`
	Lifecycle           string               `json:"lifecycle"`
	OccurredAtMS        uint64               `json:"occurred_at_ms"`
	ProductClientID     string               `json:"product_client_id"`
	Runtime             CodexRuntimeMetadata `json:"runtime"`
	ModelSlug           string               `json:"model_slug"`
	PluginID            string               `json:"plugin_id"`
	ScriptPath          string               `json:"script_path"`
	Skill               string               `json:"skill"`
	ArtifactType        string               `json:"artifact_type"`
	OperationKind       string               `json:"operation_kind"`
	ExpectedOutputCount int                  `json:"expected_output_count"`
	OutputFormat        string               `json:"output_format"`
	ExecutionBackend    string               `json:"execution_backend"`
}

func NewArtifactOperationEvent(params ArtifactOperationEventParams) ArtifactOperationEventRequest {
	return ArtifactOperationEventRequest{
		EventType:   ArtifactOperationEventType,
		EventParams: params,
	}
}
