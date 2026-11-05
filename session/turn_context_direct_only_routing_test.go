package session

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestRecoveredTurnStartOptionsRestoresDirectOnlyRoutingLikeRust(t *testing.T) {
	record := &Record{Metadata: Metadata{
		TurnContext: json.RawMessage(`{"turn_id":"turn-1","root_turn_id":"root-context","direct_only_tool_namespaces":["calendar"]}`),
	}}
	options := record.RecoveredTurnStartOptions("turn-1")
	if options.RootTurnID != "root-context" || options.DirectOnlyToolNamespaces == nil || !reflect.DeepEqual(*options.DirectOnlyToolNamespaces, []string{"calendar"}) {
		t.Fatalf("recovered options = %#v", options)
	}

	record.Metadata.TurnContext = json.RawMessage(`{"turn_id":"turn-1","root_turn_id":"root-context","direct_only_tool_namespaces":[]}`)
	options = record.RecoveredTurnStartOptions("turn-1")
	if options.DirectOnlyToolNamespaces == nil || len(*options.DirectOnlyToolNamespaces) != 0 {
		t.Fatalf("empty recovered routing = %#v", options.DirectOnlyToolNamespaces)
	}

	record.Metadata.TurnContext = json.RawMessage(`{"turn_id":"turn-1","root_turn_id":"root-context"}`)
	options = record.RecoveredTurnStartOptions("turn-1")
	if options.DirectOnlyToolNamespaces != nil || options.RootTurnID != "root-context" {
		t.Fatalf("omitted recovered routing = %#v", options)
	}
}

func TestRecoveredTurnStartOptionsKeepsAttributionRootAndDirectRoutingLikeRust(t *testing.T) {
	trigger := "delegated"
	record := &Record{Metadata: Metadata{
		TurnAttribution: &TurnAttribution{TurnID: "turn-1", TurnTrigger: &trigger, RootTurnID: stringPointer("root-attribution")},
		TurnContext:     json.RawMessage(`{"turn_id":"turn-1","root_turn_id":"root-context","direct_only_tool_namespaces":["calendar"]}`),
	}}
	options := record.RecoveredTurnStartOptions("turn-1")
	if options.TurnTrigger != "delegated" || options.RootTurnID != "root-attribution" || options.DirectOnlyToolNamespaces == nil {
		t.Fatalf("recovered options = %#v", options)
	}
}

func stringPointer(value string) *string {
	return &value
}
