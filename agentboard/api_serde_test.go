package agentboard

import (
	"encoding/json"
	"testing"

	"codex_go/agent"
)

// Rust parity (#48077): the board's request types serialize with serde's field
// names, and a subscription target uses serde's externally tagged enum form
// (which is also the key the local backend stores).
func TestAgentBoardRequestTypesSerializeLikeRust(t *testing.T) {
	encoded, err := json.Marshal(CreateChannelRequest{ChannelName: "work", Subscription: Subscribe})
	if err != nil {
		t.Fatalf("Marshal(CreateChannelRequest) error = %v", err)
	}
	if string(encoded) != `{"channel_name":"work","subscription":"subscribe"}` {
		t.Fatalf("CreateChannelRequest = %s", encoded)
	}

	encoded, err = json.Marshal(ReadPostRequest{MessageID: "message-1", OffsetChars: 3, LimitChars: 5})
	if err != nil {
		t.Fatalf("Marshal(ReadPostRequest) error = %v", err)
	}
	if string(encoded) != `{"message_id":"message-1","offset_chars":3,"limit_chars":5}` {
		t.Fatalf("ReadPostRequest = %s", encoded)
	}

	target := SubscriptionTarget{Kind: "channel", ChannelName: "work"}
	encoded, err = json.Marshal(target)
	if err != nil {
		t.Fatalf("Marshal(SubscriptionTarget) error = %v", err)
	}
	if string(encoded) != `{"Channel":"work"}` {
		t.Fatalf("channel target = %s", encoded)
	}
	thread := SubscriptionTarget{Kind: "thread", ThreadID: "11111111-1111-4111-8111-111111111111"}
	encoded, err = json.Marshal(thread)
	if err != nil {
		t.Fatalf("Marshal(thread target) error = %v", err)
	}
	if string(encoded) != `{"Thread":"11111111-1111-4111-8111-111111111111"}` {
		t.Fatalf("thread target = %s", encoded)
	}

	targetAgent := agent.AgentPath("/root/worker")
	encoded, err = json.Marshal(SubscriptionRequest{Target: target, TargetAgent: &targetAgent, Change: Unsubscribe})
	if err != nil {
		t.Fatalf("Marshal(SubscriptionRequest) error = %v", err)
	}
	if string(encoded) != `{"target":{"Channel":"work"},"target_agent":"/root/worker","change":"unsubscribe"}` {
		t.Fatalf("SubscriptionRequest = %s", encoded)
	}

	// The stored target key uses the same form, so a Rust binary can read it.
	key, err := subscriptionTargetKey(thread)
	if err != nil {
		t.Fatalf("subscriptionTargetKey() error = %v", err)
	}
	if key != `{"Thread":"11111111-1111-4111-8111-111111111111"}` {
		t.Fatalf("target key = %s", key)
	}

	// Unmarshalling reconstructs the same target.
	var decoded SubscriptionTarget
	if err := json.Unmarshal([]byte(`{"Thread":"11111111-1111-4111-8111-111111111111"}`), &decoded); err != nil {
		t.Fatalf("Unmarshal(SubscriptionTarget) error = %v", err)
	}
	if decoded != thread {
		t.Fatalf("decoded target = %#v", decoded)
	}
	if err := json.Unmarshal([]byte(`{}`), &decoded); err == nil {
		t.Fatal("an empty target unmarshalled")
	}
}
