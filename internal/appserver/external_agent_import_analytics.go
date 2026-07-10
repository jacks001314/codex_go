package appserver

import (
	"context"
	"strings"

	"codex_go/internal/config"
	"codex_go/internal/telemetry"
)

func (r *RuntimeRouter) emitExternalAgentConfigImportAnalytics(ctx context.Context, connectionID string, params *config.ExternalAgentConfigImportParams, notification *config.ExternalAgentConfigImportCompletedNotification) {
	if r == nil || r.services.Analytics == nil || notification == nil || len(notification.ItemTypeResults) == 0 {
		return
	}
	sink, ok := r.services.Analytics.(telemetry.ExternalAgentConfigImportEventSink)
	if !ok {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	source := externalAgentImportAnalyticsSource(params)
	productClientID := stringPtrIfNotEmpty(r.analyticsProductClientID(connectionID))
	for _, result := range notification.ItemTypeResults {
		itemType := string(result.ItemType)
		completeEvent := telemetry.NewCodexOnboardingExternalAgentImportCompleteEvent(telemetry.CodexOnboardingExternalAgentImportCompleteMetadata{
			ImportID:        strings.TrimSpace(notification.ImportID),
			Source:          source,
			ItemType:        itemType,
			SuccessCount:    len(result.Successes),
			FailedCount:     len(result.Failures),
			ProductClientID: productClientID,
		})
		sink.TrackCodexOnboardingExternalAgentImportCompleteEvent(ctx, completeEvent)
		for _, failure := range result.Failures {
			failureEvent := telemetry.NewCodexOnboardingExternalAgentImportFailureEvent(telemetry.CodexOnboardingExternalAgentImportFailureMetadata{
				ImportID:        strings.TrimSpace(notification.ImportID),
				Source:          source,
				ItemType:        itemType,
				FailureStage:    strings.TrimSpace(failure.FailureStage),
				ErrorType:       externalAgentImportFailureErrorType(failure),
				ProductClientID: productClientID,
			})
			sink.TrackCodexOnboardingExternalAgentImportFailureEvent(ctx, failureEvent)
		}
	}
}

func externalAgentImportAnalyticsSource(params *config.ExternalAgentConfigImportParams) string {
	if params == nil || params.Source == nil {
		return ""
	}
	return strings.TrimSpace(*params.Source)
}

func externalAgentImportFailureErrorType(failure config.ExternalAgentConfigImportItemTypeFailure) string {
	if failure.ErrorType != nil && strings.TrimSpace(*failure.ErrorType) != "" {
		return strings.TrimSpace(*failure.ErrorType)
	}
	return strings.TrimSpace(failure.FailureStage)
}
