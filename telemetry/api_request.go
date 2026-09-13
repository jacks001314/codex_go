package telemetry

import (
	"context"
	"strconv"

	"codex_go/model"
)

// Rust parity: codex-otel's SessionTelemetry::record_api_request. The metric
// half is recorded by the model client (which cannot import this package); the
// diagnostic record is emitted here from the client's per-attempt values.

// RecordWebsocketConnect mirrors SessionTelemetry::record_websocket_connect: the
// handshake's duration and outcome, the auth observability the attempt carried,
// the endpoint, and the response's request id / cf-ray / auth error, on both
// records.
func (t *SessionTelemetry) RecordWebsocketConnect(ctx context.Context, record model.WebsocketConnectRecord) {
	if t == nil || t.Logs == nil {
		return
	}
	authEnv := t.Metadata.AuthEnv
	success := record.ErrorMessage == ""
	if record.Status != nil {
		success = success && *record.Status >= 200 && *record.Status <= 299
	}
	fields := map[string]string{
		"duration_ms":                                 strconv.FormatInt(record.Duration.Milliseconds(), 10),
		"success":                                     strconv.FormatBool(success),
		"endpoint":                                    record.Endpoint,
		"auth.header_attached":                        strconv.FormatBool(record.AuthHeaderAttached),
		"auth.retry_after_unauthorized":               strconv.FormatBool(record.RetryAfterUnauthorized),
		"auth.connection_reused":                      strconv.FormatBool(record.ConnectionReused),
		"auth.env_openai_api_key_present":             strconv.FormatBool(authEnv.OpenAIAPIKeyEnvPresent),
		"auth.env_codex_api_key_present":              strconv.FormatBool(authEnv.CodexAPIKeyEnvPresent),
		"auth.env_codex_api_key_enabled":              strconv.FormatBool(authEnv.CodexAPIKeyEnvEnabled),
		"auth.env_refresh_token_url_override_present": strconv.FormatBool(authEnv.RefreshTokenURLOverridePresent),
	}
	if record.Status != nil {
		fields["http.response.status_code"] = strconv.Itoa(*record.Status)
	}
	if record.ErrorMessage != "" {
		fields["error.message"] = record.ErrorMessage
	}
	if authEnv.ProviderEnvKeyPresent != nil {
		fields["auth.env_provider_key_present"] = strconv.FormatBool(*authEnv.ProviderEnvKeyPresent)
	}
	for _, optional := range []struct {
		key   string
		value string
	}{
		{"auth.env_provider_key_name", authEnv.ProviderEnvKeyName},
		{"auth.header_name", record.AuthHeaderName},
		{"auth.recovery_mode", record.RecoveryMode},
		{"auth.recovery_phase", record.RecoveryPhase},
		{"auth.request_id", record.RequestID},
		{"auth.cf_ray", record.CFRay},
		{"auth.error", record.AuthError},
		{"auth.error_code", record.AuthErrorCode},
		{"auth.agent_id", record.AgentID},
		{"auth.task_id", record.TaskID},
	} {
		if optional.value != "" {
			fields[optional.key] = optional.value
		}
	}
	t.LogAndTraceEvent(ctx, "codex.websocket_connect", fields, nil, nil)
}

// RecordSSEEventCompleted mirrors SessionTelemetry::sse_event_completed: the
// token counts and timing of a completed model response, on both the log record
// and the span event.
func (t *SessionTelemetry) RecordSSEEventCompleted(ctx context.Context, record model.SSECompletedRecord) {
	if t == nil || t.Logs == nil {
		return
	}
	usage := record.Usage
	fields := map[string]string{
		"event.kind":         "response.completed",
		"input_token_count":  strconv.FormatInt(usage.InputTokens, 10),
		"output_token_count": strconv.FormatInt(usage.OutputTokens, 10),
		"tool_token_count":   strconv.FormatInt(usage.TotalTokens, 10),
	}
	// Rust reports these counts only when the response carried them; Go's usage
	// keeps a zero value for an absent count, so zero stays absent.
	for _, optional := range []struct {
		key   string
		value int64
	}{
		{"cached_token_count", usage.CachedInputTokens},
		{"cache_write_token_count", usage.CacheWriteInputTokens},
		{"reasoning_token_count", usage.ReasoningOutputTokens},
	} {
		if optional.value != 0 {
			fields[optional.key] = strconv.FormatInt(optional.value, 10)
		}
	}
	if record.TTFTMillis != nil {
		fields["ttft_ms"] = strconv.FormatInt(*record.TTFTMillis, 10)
	}
	if record.ServiceTier != "" {
		fields["service_tier"] = record.ServiceTier
	}
	if record.ReasoningEffort != "" {
		fields["model_reasoning_effort"] = record.ReasoningEffort
	}
	t.LogAndTraceEvent(ctx, "codex.sse_event", fields, nil, nil)
}

// RecordWebsocketRequest emits the diagnostic records for one websocket request
// send (Rust's SessionTelemetry::record_websocket_request): the duration and
// outcome, the auth environment, whether the connection was reused, and the
// agent identity. The metric half lives in the model client.
func (t *SessionTelemetry) RecordWebsocketRequest(ctx context.Context, record model.WebsocketRequestRecord) {
	if t == nil || t.Logs == nil {
		return
	}
	authEnv := t.Metadata.AuthEnv
	fields := map[string]string{
		"duration_ms":                                 strconv.FormatInt(record.Duration.Milliseconds(), 10),
		"success":                                     strconv.FormatBool(record.ErrorMessage == ""),
		"auth.connection_reused":                      strconv.FormatBool(record.ConnectionReused),
		"auth.env_openai_api_key_present":             strconv.FormatBool(authEnv.OpenAIAPIKeyEnvPresent),
		"auth.env_codex_api_key_present":              strconv.FormatBool(authEnv.CodexAPIKeyEnvPresent),
		"auth.env_codex_api_key_enabled":              strconv.FormatBool(authEnv.CodexAPIKeyEnvEnabled),
		"auth.env_refresh_token_url_override_present": strconv.FormatBool(authEnv.RefreshTokenURLOverridePresent),
	}
	if record.ErrorMessage != "" {
		fields["error.message"] = record.ErrorMessage
	}
	for _, optional := range []struct {
		key   string
		value string
	}{
		{"auth.env_provider_key_name", authEnv.ProviderEnvKeyName},
		{"auth.agent_id", record.AgentID},
		{"auth.task_id", record.TaskID},
	} {
		if optional.value != "" {
			fields[optional.key] = optional.value
		}
	}
	if authEnv.ProviderEnvKeyPresent != nil {
		fields["auth.env_provider_key_present"] = strconv.FormatBool(*authEnv.ProviderEnvKeyPresent)
	}
	t.LogAndTraceEvent(ctx, "codex.websocket_request", fields, nil, nil)
}

// RecordAPIRequest emits the diagnostic records for one model HTTP attempt: the
// event fields plus the session identity and the auth environment on both the
// log record and the span event, exactly as Rust's log_and_trace_event! does.
func (t *SessionTelemetry) RecordAPIRequest(ctx context.Context, record model.APIRequestRecord) {
	if t == nil || t.Logs == nil {
		return
	}
	authEnv := t.Metadata.AuthEnv
	fields := map[string]string{
		"duration_ms":                                 strconv.FormatInt(record.Duration.Milliseconds(), 10),
		"attempt":                                     strconv.FormatUint(record.Attempt, 10),
		"auth.header_attached":                        strconv.FormatBool(record.AuthHeaderAttached),
		"auth.retry_after_unauthorized":               strconv.FormatBool(record.RetryAfterUnauthorized),
		"endpoint":                                    record.Endpoint,
		"auth.env_openai_api_key_present":             strconv.FormatBool(authEnv.OpenAIAPIKeyEnvPresent),
		"auth.env_codex_api_key_present":              strconv.FormatBool(authEnv.CodexAPIKeyEnvPresent),
		"auth.env_codex_api_key_enabled":              strconv.FormatBool(authEnv.CodexAPIKeyEnvEnabled),
		"auth.env_refresh_token_url_override_present": strconv.FormatBool(authEnv.RefreshTokenURLOverridePresent),
	}
	if record.Status != nil {
		fields["http.response.status_code"] = strconv.Itoa(*record.Status)
	}
	if record.ErrorMessage != "" {
		fields["error.message"] = record.ErrorMessage
	}
	for _, optional := range []struct {
		key   string
		value string
	}{
		{"auth.env_provider_key_name", authEnv.ProviderEnvKeyName},
		{"auth.header_name", record.AuthHeaderName},
		{"auth.recovery_mode", record.RecoveryMode},
		{"auth.recovery_phase", record.RecoveryPhase},
		{"auth.request_id", record.RequestID},
		{"auth.cf_ray", record.CFRay},
		{"auth.error", record.AuthError},
		{"auth.error_code", record.AuthErrorCode},
		{"auth.agent_id", record.AgentID},
		{"auth.task_id", record.TaskID},
	} {
		if optional.value != "" {
			fields[optional.key] = optional.value
		}
	}
	if authEnv.ProviderEnvKeyPresent != nil {
		fields["auth.env_provider_key_present"] = strconv.FormatBool(*authEnv.ProviderEnvKeyPresent)
	}
	t.LogAndTraceEvent(ctx, "codex.api_request", fields, nil, nil)
}
