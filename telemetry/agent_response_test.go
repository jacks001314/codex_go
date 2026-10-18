package telemetry

import (
	"context"
	"strconv"
	"strings"
	"testing"
)

func TestAgentResponseLogsBoundedCitationFreeTextLikeRust(t *testing.T) {
	client := NewLogsClient(LogsClientOptions{
		Endpoint:       "http://127.0.0.1:1/v1/logs",
		ExportInterval: -1,
	})
	session := NewSessionTelemetry(SessionTelemetryMetadata{ConversationID: "thread-1"})
	session.Logs = client
	text := "answer <oai-mem-citation>private memory</oai-mem-citation>" + strings.Repeat("é", MaxAgentResponseBytes)
	session.AgentResponse(context.Background(), AgentResponseEvent{
		AgentType:            "subagent",
		TurnID:               "turn-1",
		ItemID:               "item-1",
		ParentConversationID: "root-thread",
		ParentTurnID:         "parent-turn",
		RootTurnID:           "root-turn",
		InitiatingAgentPath:  "/root",
		Response:             text,
	})
	batch := client.snapshot()
	if len(batch.ResourceLogs) != 1 || len(batch.ResourceLogs[0].ScopeLogs) != 1 ||
		len(batch.ResourceLogs[0].ScopeLogs[0].LogRecords) != 1 {
		t.Fatalf("agent response log batch = %#v", batch)
	}
	record := batch.ResourceLogs[0].ScopeLogs[0].LogRecords[0]
	if record.Target != OtelLogOnlyTarget {
		t.Fatalf("agent response target = %q, want log-only", record.Target)
	}
	attributes := map[string]string{}
	for _, attribute := range record.Attributes {
		attributes[attribute.Key] = attribute.Value
	}
	strippedLength := len("answer " + strings.Repeat("é", MaxAgentResponseBytes))
	if attributes["event.name"] != AgentResponseEventName ||
		attributes["agent.type"] != "subagent" ||
		attributes["turn.id"] != "turn-1" ||
		attributes["item.id"] != "item-1" ||
		attributes["parent.conversation.id"] != "root-thread" ||
		attributes["parent.turn.id"] != "parent-turn" ||
		attributes["root.turn.id"] != "root-turn" ||
		attributes["initiating.agent.path"] != "/root" {
		t.Fatalf("agent response attributes = %#v", attributes)
	}
	if strings.Contains(attributes["response"], "<oai-mem-citation>") {
		t.Fatalf("memory citation leaked into exported response: %q", attributes["response"])
	}
	if attributes["response_length"] != strconv.Itoa(strippedLength) ||
		attributes["response_truncated"] != "true" ||
		len(attributes["response"]) > MaxAgentResponseBytes {
		t.Fatalf("response metadata = length %q truncated %q bytes %d", attributes["response_length"], attributes["response_truncated"], len(attributes["response"]))
	}

	session.AgentResponse(context.Background(), AgentResponseEvent{
		AgentType: "main", TurnID: "turn-2", ItemID: "item-2", Response: "  <oai-mem-citation>only</oai-mem-citation> ",
	})
	if extra := client.snapshot(); len(extra.ResourceLogs) != 0 {
		t.Fatalf("citation-only response was logged: %#v", extra)
	}
}
