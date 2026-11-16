package cloudclient

import (
	"testing"
)

// Rust #61737: a native agent message carries an optional phase; only the
// alternative whose int32 marker is 1 is recognized.
func TestAgentMessagePhaseDecodingLikeRust(t *testing.T) {
	phase := func(tag int, marker int64) []byte {
		return protoMessage(4, protoMessage(tag, protoVarint(1, marker)))
	}
	agentItem := func(extra []byte) []byte {
		item := append(protoMessage(2, []byte("reply")), protoMessage(6, []byte("done"))...)
		return append(item, extra...)
	}
	cases := []struct {
		name string
		wire []byte
		want AgentMessagePhase
	}{
		{name: "commentary", wire: phase(1, 1), want: AgentMessagePhaseCommentary},
		{name: "final answer", wire: phase(2, 1), want: AgentMessagePhaseFinalAnswer},
		{name: "partial answer", wire: phase(3, 1), want: AgentMessagePhasePartialAnswer},
		{name: "missing", wire: nil, want: AgentMessagePhaseUnset},
		{name: "zero marker", wire: phase(2, 0), want: AgentMessagePhaseUnset},
		{name: "unrecognized marker", wire: phase(2, 7), want: AgentMessagePhaseUnset},
		{name: "unknown alternative", wire: protoMessage(4, protoMessage(5, protoVarint(1, 1))), want: AgentMessagePhaseUnset},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			item, ok, err := decodeItem(protoMessage(3, agentItem(test.wire)))
			if err != nil || !ok || item.Kind != ItemAgentMessage {
				t.Fatalf("decode = %#v ok=%v err=%v", item, ok, err)
			}
			if item.Agent.Phase != test.want {
				t.Fatalf("phase = %v, want %v", item.Agent.Phase, test.want)
			}
		})
	}
}

// A phase never replaces the message text or its identity.
func TestAgentMessagePhaseKeepsTextLikeRust(t *testing.T) {
	item := append(protoMessage(2, []byte("reply")), protoMessage(6, []byte("final text"))...)
	item = append(item, protoMessage(4, protoMessage(2, protoVarint(1, 1)))...)
	decoded, ok, err := decodeItem(protoMessage(3, item))
	if err != nil || !ok {
		t.Fatalf("decode ok=%v err=%v", ok, err)
	}
	if decoded.Agent.ID != "reply" || decoded.Agent.Text != "final text" || decoded.Agent.Phase != AgentMessagePhaseFinalAnswer {
		t.Fatalf("decoded = %#v", decoded.Agent)
	}
}
