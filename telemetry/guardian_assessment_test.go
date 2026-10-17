package telemetry

import (
	"context"
	"strconv"
	"strings"
	"testing"
)

func TestGuardianAssessmentLogsBoundedRationaleLikeRust(t *testing.T) {
	client := NewLogsClient(LogsClientOptions{
		Endpoint:       "http://127.0.0.1:1/v1/logs",
		ExportInterval: -1,
	})
	session := NewSessionTelemetry(SessionTelemetryMetadata{ConversationID: "thread-1"})
	session.Logs = client
	rationale := strings.Repeat("é", MaxGuardianAssessmentRationaleBytes)
	session.GuardianAssessment(context.Background(), GuardianAssessmentRecord{
		ID:                "review-1",
		TurnID:            "turn-1",
		TargetItemID:      "item-1",
		Status:            "denied",
		Outcome:           "deny",
		RiskLevel:         "high",
		UserAuthorization: "medium",
		StartedAtMS:       10,
		CompletedAtMS:     20,
		Rationale:         rationale,
	})
	batch := client.snapshot()
	if len(batch.ResourceLogs) != 1 || len(batch.ResourceLogs[0].ScopeLogs) != 1 ||
		len(batch.ResourceLogs[0].ScopeLogs[0].LogRecords) != 1 {
		t.Fatalf("assessment log batch = %#v", batch)
	}
	record := batch.ResourceLogs[0].ScopeLogs[0].LogRecords[0]
	if record.Target != OtelLogOnlyTarget {
		t.Fatalf("assessment target = %q, want log-only", record.Target)
	}
	attributes := map[string]string{}
	for _, attribute := range record.Attributes {
		attributes[attribute.Key] = attribute.Value
	}
	if attributes["event.name"] != GuardianAssessmentEventName ||
		attributes["review.id"] != "review-1" ||
		attributes["turn.id"] != "turn-1" ||
		attributes["item.id"] != "item-1" ||
		attributes["status"] != "denied" ||
		attributes["outcome"] != "deny" ||
		attributes["risk_level"] != "high" ||
		attributes["user_authorization"] != "medium" {
		t.Fatalf("assessment attributes = %#v", attributes)
	}
	if attributes["rationale_length"] != strconv.Itoa(len(rationale)) ||
		attributes["rationale_truncated"] != "true" ||
		len(attributes["rationale"]) > MaxGuardianAssessmentRationaleBytes {
		t.Fatalf("rationale metadata = length %q truncated %q bytes %d", attributes["rationale_length"], attributes["rationale_truncated"], len(attributes["rationale"]))
	}

	session.GuardianAssessment(context.Background(), GuardianAssessmentRecord{Status: "in_progress"})
	if extra := client.snapshot(); len(extra.ResourceLogs) != 0 {
		t.Fatalf("in-progress assessment was logged: %#v", extra)
	}
}
