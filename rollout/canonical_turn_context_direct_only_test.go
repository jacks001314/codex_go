package rollout

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCanonicalTurnContextPreservesDirectOnlyRoutingLikeRust(t *testing.T) {
	source := json.RawMessage(`{"direct_only_tool_namespaces":[],"sandbox_policy":{"type":"read-only"},"approval_policy":"on-request","model":"gpt-5","cwd":"C:/work","root_turn_id":"root","turn_id":"turn-1"}`)
	payload, ok, err := canonicalTurnContextPayload(source)
	if err != nil || !ok {
		t.Fatalf("canonicalTurnContextPayload() = ok:%v err:%v", ok, err)
	}
	if !strings.Contains(string(payload), `"turn_id":"turn-1","root_turn_id":"root","direct_only_tool_namespaces":[]`) {
		t.Fatalf("canonical turn context = %s", payload)
	}
}
