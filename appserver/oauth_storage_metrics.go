package appserver

import (
	"log/slog"
	"time"

	"codex_go/mcp"
)

// configureOAuthStorageTelemetry mirrors Rust's bounded MCP OAuth credential
// storage metrics (#47901/#49392). Only closed classifications are recorded;
// credential values, server names, paths, and raw errors never enter tags.
func (r *RuntimeRouter) configureOAuthStorageTelemetry() {
	if r == nil || r.services.TurnMetrics == nil {
		return
	}
	sink := r.services.TurnMetrics
	mcp.SetOAuthStorageTelemetryObserver(func(event mcp.OAuthStorageTelemetry) {
		tags := map[string]string{
			"credential_kind": event.CredentialKind,
			"store_mode":      event.StoreMode,
			"selected_store":  event.SelectedStore,
			"actual_store":    event.ActualStore,
			"operation":       event.Operation,
			"secure_outcome":  event.SecureOutcome,
			"outcome":         event.Outcome,
			"fallback_reason": event.FallbackReason,
			"secure_error":    event.SecureError,
			"storage_phase":   event.StoragePhase,
			"originator":      event.Originator,
		}
		sink.Counter("codex.auth_storage.operation", 1, tags)
		sink.RecordDuration("codex.auth_storage.duration", time.Duration(event.DurationMS)*time.Millisecond, tags)
		if event.FallbackReason != "" && event.FallbackReason != "none" {
			slog.Warn("credential storage file fallback completed",
				"credential_kind", event.CredentialKind,
				"store_mode", event.StoreMode,
				"selected_store", event.SelectedStore,
				"actual_store", event.ActualStore,
				"operation", event.Operation,
				"secure_outcome", event.SecureOutcome,
				"outcome", event.Outcome,
				"fallback_reason", event.FallbackReason,
				"secure_error", event.SecureError,
				"storage_phase", event.StoragePhase,
				"originator", event.Originator,
			)
		}
	})
}
