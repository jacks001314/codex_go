package appserver

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"codex_go/config"
	"codex_go/daemonrecovery"
	"codex_go/rollout"
	"codex_go/turn"
)

func TestDirectOnlyToolNamespacesForTurnLikeRust(t *testing.T) {
	cfg := &config.Config{Values: map[string]any{
		"features": map[string]any{
			"code_mode": map[string]any{
				"direct_only_tool_namespaces": []any{"configured"},
			},
		},
	}}
	override := []string{"turn-only"}
	empty := []string{}
	tests := []struct {
		name   string
		params *turn.TurnStartParams
		want   []string
	}{
		{name: "omitted inherits thread", want: []string{"configured"}},
		{name: "explicit override", params: &turn.TurnStartParams{DirectOnlyToolNamespaces: &override}, want: []string{"turn-only"}},
		{name: "explicit empty clears", params: &turn.TurnStartParams{DirectOnlyToolNamespaces: &empty}, want: []string{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := directOnlyToolNamespacesForTurn(cfg, tc.params); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("directOnlyToolNamespacesForTurn() = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestTurnContextRecordPersistsDirectOnlyRoutingLikeRust(t *testing.T) {
	override := []string{"turn-only"}
	runConfig := &appTurnRunConfig{Model: "gpt-test", DirectOnlyToolNamespaces: &override}
	record, ok := (&RuntimeRouter{}).turnContextRecordForRunConfig("turn-1", "root-1", "C:/work", runConfig)
	if !ok {
		t.Fatal("turnContextRecordForRunConfig() returned no record")
	}
	if record.DirectOnlyToolNamespaces == nil || !reflect.DeepEqual(*record.DirectOnlyToolNamespaces, override) {
		t.Fatalf("direct-only routing = %#v, want %#v", record.DirectOnlyToolNamespaces, override)
	}
	payload, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(payload), `"direct_only_tool_namespaces":["turn-only"]`) {
		t.Fatalf("turn context JSON = %s", payload)
	}

	empty := []string{}
	record, ok = (&RuntimeRouter{}).turnContextRecordForRunConfig("turn-2", "root-1", "C:/work", &appTurnRunConfig{
		Model: "gpt-test", DirectOnlyToolNamespaces: &empty,
	})
	if !ok || record.DirectOnlyToolNamespaces == nil || len(*record.DirectOnlyToolNamespaces) != 0 {
		t.Fatalf("empty direct-only routing = %#v", record.DirectOnlyToolNamespaces)
	}
	payload, err = json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(payload), `"direct_only_tool_namespaces":[]`) {
		t.Fatalf("empty turn context JSON = %s", payload)
	}

	record, ok = (&RuntimeRouter{}).turnContextRecordForRunConfig("turn-3", "root-1", "C:/work", &appTurnRunConfig{Model: "gpt-test"})
	if !ok || record.DirectOnlyToolNamespaces != nil {
		t.Fatalf("omitted direct-only routing = %#v", record.DirectOnlyToolNamespaces)
	}
}

func TestTurnStartDirectOnlyRoutingIsHostOnlyAndCloneSafe(t *testing.T) {
	values := []string{"calendar"}
	params := &turn.TurnStartParams{ThreadID: "thread-1", DirectOnlyToolNamespaces: &values}
	payload, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), "direct") {
		t.Fatalf("host-only direct-only routing leaked to the wire: %s", payload)
	}
	clone := cloneTurnStartParams(params)
	if clone.DirectOnlyToolNamespaces == nil || clone.DirectOnlyToolNamespaces == params.DirectOnlyToolNamespaces {
		t.Fatalf("direct-only routing was not cloned: %#v", clone.DirectOnlyToolNamespaces)
	}
	(*clone.DirectOnlyToolNamespaces)[0] = "changed"
	if values[0] != "calendar" {
		t.Fatalf("mutating the clone changed the source: %#v", values)
	}
}

func TestDaemonRecoveryRestoresStandaloneTurnDirectOnlyRoutingLikeRust(t *testing.T) {
	lines := []rollout.Line{{
		Type:        "turn_context",
		TurnContext: json.RawMessage(`{"turn_id":"standalone-tool-turn","direct_only_tool_namespaces":[]}`),
	}}
	previous := daemonRecoveryLastTurnContext(lines, "standalone-tool-turn")
	if previous == nil || previous.DirectOnlyToolNamespaces == nil || len(*previous.DirectOnlyToolNamespaces) != 0 {
		t.Fatalf("recovered turn context = %#v", previous)
	}
	params := daemonRecoveryContinuationParams("thread-1", previous, daemonrecovery.InterruptedTurn{})
	if params.DirectOnlyToolNamespaces == nil || len(*params.DirectOnlyToolNamespaces) != 0 {
		t.Fatalf("continuation direct-only routing = %#v", params.DirectOnlyToolNamespaces)
	}
}
