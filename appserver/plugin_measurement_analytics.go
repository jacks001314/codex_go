package appserver

import (
	"context"
	"strings"

	"codex_go/plugin"
	"codex_go/session"
	"codex_go/telemetry"
)

// emitPluginMeasurements publishes one validated measurement batch. Rust #45445
// attributes the measurement to the model that invoked the measured command, so
// the labels the executing tool captured when the command started travel with
// the batch; a batch without them reports absent labels.
func (r *RuntimeRouter) emitPluginMeasurements(ctx context.Context, threadID string, turnID string, batch plugin.PluginMeasurementBatch) {
	if r == nil {
		return
	}
	sink, ok := r.services.Analytics.(telemetry.PluginEventSink)
	if !ok {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	rows := make([]telemetry.PluginMeasurementRow, 0, len(batch.Rows))
	for _, row := range batch.Rows {
		rows = append(rows, telemetry.PluginMeasurementRow{
			MeasurementName: row.MeasurementName,
			NumberValue:     row.NumberValue,
			Dimensions:      row.Dimensions,
		})
	}
	originator := ""
	if record, recordErr := r.threadRecord(session.ThreadID(threadID), true, false); recordErr == nil && record != nil {
		originator = strings.TrimSpace(record.Metadata.Originator)
	}
	input := telemetry.CodexPluginMeasurementsInput{
		ThreadID:    strings.TrimSpace(threadID),
		TurnID:      strings.TrimSpace(turnID),
		PluginID:    batch.PluginID,
		ExecutionID: batch.ExecutionID,
		Operation:   batch.Operation,
		Originator:  originator,
		Rows:        rows,
	}
	if modelSlug := strings.TrimSpace(batch.ModelSlug); modelSlug != "" {
		input.ModelSlug = &modelSlug
	}
	if reasoningEffort := strings.TrimSpace(batch.ReasoningEffort); reasoningEffort != "" {
		input.ReasoningEffort = &reasoningEffort
	}
	sink.TrackCodexPluginMeasurementsEvent(ctx, input)
}
