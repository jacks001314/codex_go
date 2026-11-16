package cloudbackend

import (
	"encoding/json"
	"testing"

	"codex_go/appserver"
	"codex_go/cloudclient"
)

// Rust #61737: the phase decoded from a cloud agent message survives conversion
// into the app-server item; an absent phase stays null.
func TestCloudAgentMessagePhaseSurvivesLikeRust(t *testing.T) {
	cases := []struct {
		name  string
		phase cloudclient.AgentMessagePhase
		want  any
	}{
		{name: "commentary", phase: cloudclient.AgentMessagePhaseCommentary, want: string(appserver.MessagePhaseCommentary)},
		{name: "final answer", phase: cloudclient.AgentMessagePhaseFinalAnswer, want: string(appserver.MessagePhaseFinalAnswer)},
		{name: "partial answer", phase: cloudclient.AgentMessagePhasePartialAnswer, want: string(appserver.MessagePhasePartialAnswer)},
		{name: "unset", phase: cloudclient.AgentMessagePhaseUnset, want: nil},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			payload, ok := itemSummaryPayload(cloudclient.ItemSummary{
				Kind:  cloudclient.ItemAgentMessage,
				Agent: cloudclient.AgentMessageSummary{ID: "reply", Text: "done", Phase: test.phase},
			})
			if !ok {
				t.Fatal("agent message was not reported")
			}
			encoded, err := json.Marshal(payload)
			if err != nil {
				t.Fatalf("marshal payload: %v", err)
			}
			decoded := map[string]any{}
			if err := json.Unmarshal(encoded, &decoded); err != nil {
				t.Fatalf("decode payload: %v", err)
			}
			if decoded["type"] != "agentMessage" || decoded["id"] != "reply" || decoded["text"] != "done" {
				t.Fatalf("payload = %s", encoded)
			}
			got, present := decoded["phase"]
			if !present || got != test.want {
				t.Fatalf("phase = %v (present=%v), want %v in %s", got, present, test.want, encoded)
			}
		})
	}
}
