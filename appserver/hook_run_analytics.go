package appserver

import (
	"context"
	"strings"

	"codex_go/telemetry"
)

type hookRunAnalyticsContext struct {
	ConnectionID string
	Model        string
}

func (r *RuntimeRouter) emitHookRunAnalyticsEvent(ctx context.Context, notification *HookRunCompletedNotification) {
	if r == nil || r.services.Analytics == nil || notification == nil || notification.TurnID == nil {
		return
	}
	sink, ok := r.services.Analytics.(telemetry.HookRunEventSink)
	if !ok {
		return
	}
	threadID := strings.TrimSpace(notification.ThreadID)
	turnID := strings.TrimSpace(*notification.TurnID)
	if threadID == "" || turnID == "" {
		return
	}
	active := r.hookRunAnalyticsContext(threadID, turnID)
	if active == nil || strings.TrimSpace(active.Model) == "" {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	event := telemetry.NewCodexHookRunEvent(telemetry.CodexHookRunMetadataV1{
		ThreadID:        stringPtrIfNotEmpty(threadID),
		TurnID:          stringPtrIfNotEmpty(turnID),
		ProductClientID: stringPtrIfNotEmpty(r.analyticsProductClientID(active.ConnectionID)),
		ModelSlug:       stringPtrIfNotEmpty(active.Model),
		HookName:        stringPtrIfNotEmpty(hookRunAnalyticsEventName(notification.Run.EventName)),
		HookSource:      stringPtrIfNotEmpty(hookRunAnalyticsSource(notification.Run.Source)),
		Status:          stringPtrIfNotEmpty(hookRunAnalyticsStatus(notification.Run.Status)),
	})
	sink.TrackCodexHookRunEvent(ctx, event)
}

func (r *RuntimeRouter) hookRunAnalyticsContext(threadID string, turnID string) *hookRunAnalyticsContext {
	if r == nil || strings.TrimSpace(threadID) == "" || strings.TrimSpace(turnID) == "" {
		return nil
	}
	active := r.threads.ActiveTurn(threadID)
	if active == nil || active.TurnID != turnID || active.RunConfig == nil {
		return nil
	}
	return &hookRunAnalyticsContext{
		ConnectionID: active.ConnectionID,
		Model:        active.RunConfig.Model,
	}
}

func hookRunAnalyticsEventName(eventName HookEventName) string {
	switch eventName {
	case HookEventPreToolUse:
		return "PreToolUse"
	case HookEventPermissionRequest:
		return "PermissionRequest"
	case HookEventPostToolUse:
		return "PostToolUse"
	case HookEventPreCompact:
		return "PreCompact"
	case HookEventPostCompact:
		return "PostCompact"
	case HookEventSessionStart:
		return "SessionStart"
	case HookEventUserPromptSubmit:
		return "UserPromptSubmit"
	case HookEventSubagentStart:
		return "SubagentStart"
	case HookEventSubagentStop:
		return "SubagentStop"
	case HookEventStop:
		return "Stop"
	default:
		return strings.TrimSpace(string(eventName))
	}
}

func hookRunAnalyticsSource(source HookSource) string {
	switch source {
	case HookSourceSessionFlags:
		return "session_flags"
	case HookSourceCloudRequirements:
		return "cloud_requirements"
	case HookSourceCloudManagedConfig:
		return "cloud_managed_config"
	case HookSourceLegacyConfigFile:
		return "legacy_managed_config_file"
	case HookSourceLegacyConfigMDM:
		return "legacy_managed_config_mdm"
	default:
		return strings.TrimSpace(string(source))
	}
}

func hookRunAnalyticsStatus(status HookRunStatus) string {
	if status == HookRunRunning {
		return string(HookRunFailed)
	}
	return strings.TrimSpace(string(status))
}
