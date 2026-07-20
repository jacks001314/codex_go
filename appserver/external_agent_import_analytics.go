package appserver

import (
	"context"
	"strings"

	"codex_go/config"
	"codex_go/telemetry"
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
				SubErrorType:    trimmedStringPtr(failure.SubErrorType),
				ProductClientID: productClientID,
			})
			sink.TrackCodexOnboardingExternalAgentImportFailureEvent(ctx, failureEvent)
		}
	}
}

func trimmedStringPtr(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func externalAgentImportAnalyticsSource(params *config.ExternalAgentConfigImportParams) string {
	if params == nil {
		return "cli"
	}
	if params.Source != nil && strings.TrimSpace(*params.Source) != "" {
		return strings.TrimSpace(*params.Source)
	}
	return "cli"
}

func externalAgentImportFailureErrorType(failure config.ExternalAgentConfigImportItemTypeFailure) string {
	if failure.ErrorType != nil && strings.TrimSpace(*failure.ErrorType) != "" {
		return strings.TrimSpace(*failure.ErrorType)
	}
	return strings.TrimSpace(failure.FailureStage)
}
