package telemetry

import (
	"context"
	"strconv"

	"codex_go/model"
)

// Rust parity: codex-otel's SessionTelemetry::record_auth_recovery, which core's
// client reports when a request recovers from a 401 (the recovery plan's mode,
// step, and outcome plus the failed response's debug context).

// RecordAuthRecovery emits the recovery record on both the log record and the
// span event, with the session identity.
func (t *SessionTelemetry) RecordAuthRecovery(ctx context.Context, record model.AuthRecoveryRecord) {
	if t == nil || t.Logs == nil {
		return
	}
	fields := map[string]string{
		"auth.mode":    record.Mode,
		"auth.step":    record.Step,
		"auth.outcome": record.Outcome,
	}
	for _, optional := range []struct {
		key   string
		value string
	}{
		{"auth.request_id", record.RequestID},
		{"auth.cf_ray", record.CFRay},
		{"auth.error", record.AuthError},
		{"auth.error_code", record.AuthErrorCode},
		{"auth.recovery_reason", record.RecoveryReason},
	} {
		if optional.value != "" {
			fields[optional.key] = optional.value
		}
	}
	if record.AuthStateChanged != nil {
		fields["auth.state_changed"] = strconv.FormatBool(*record.AuthStateChanged)
	}
	t.LogAndTraceEvent(ctx, "codex.auth_recovery", fields, nil, nil)
}
