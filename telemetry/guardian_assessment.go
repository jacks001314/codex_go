package telemetry

import (
	"context"
	"strconv"
)

const (
	// GuardianAssessmentEventName is the OTLP log-only event for terminal
	// Guardian reviews.
	GuardianAssessmentEventName = "codex.guardian_assessment"
	// MaxGuardianAssessmentRationaleBytes bounds exported rationale text.
	MaxGuardianAssessmentRationaleBytes = 64 * 1024
)

// GuardianAssessmentRecord is the host-attributed terminal review written by
// the synchronous Guardian reviewer.
type GuardianAssessmentRecord struct {
	ID                string
	TurnID            string
	TargetItemID      string
	Status            string
	Outcome           string
	RiskLevel         string
	UserAuthorization string
	StartedAtMS       int64
	CompletedAtMS     int64
	Rationale         string
}

// GuardianAssessment emits the opt-in log-only review record. In-progress
// events are ignored; failures leave Outcome empty.
func (t *SessionTelemetry) GuardianAssessment(ctx context.Context, record GuardianAssessmentRecord) {
	if t == nil || record.Status == "" || record.Status == "in_progress" {
		return
	}
	rationale := takeBytesAtCharBoundary(record.Rationale, MaxGuardianAssessmentRationaleBytes)
	fields := map[string]string{
		"review.id":       record.ID,
		"turn.id":         record.TurnID,
		"status":          record.Status,
		"started_at_ms":   strconv.FormatInt(record.StartedAtMS, 10),
		"completed_at_ms": strconv.FormatInt(record.CompletedAtMS, 10),
	}
	if record.TargetItemID != "" {
		fields["item.id"] = record.TargetItemID
	}
	if record.Outcome != "" {
		fields["outcome"] = record.Outcome
	}
	if record.RiskLevel != "" {
		fields["risk_level"] = record.RiskLevel
	}
	if record.UserAuthorization != "" {
		fields["user_authorization"] = record.UserAuthorization
	}
	logOnly := map[string]string{
		"rationale":           rationale,
		"rationale_length":    strconv.Itoa(len(record.Rationale)),
		"rationale_truncated": strconv.FormatBool(len(record.Rationale) > MaxGuardianAssessmentRationaleBytes),
	}
	t.LogEvent(ctx, GuardianAssessmentEventName, fields, logOnly)
}
