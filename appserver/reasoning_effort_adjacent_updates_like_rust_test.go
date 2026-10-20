package appserver

import "testing"

func TestCoalesceAdjacentReasoningEffortUpdatesLikeRust(t *testing.T) {
	update := func(effort string) map[string]any {
		return map[string]any{"type": "configuration_update", "reasoning": map[string]any{"effort": effort}}
	}
	items := []any{
		update("low"), update("medium"), update("high"),
		map[string]any{"type": "message", "role": "user"},
		update("low"), update("high"),
	}
	got := coalesceAdjacentReasoningEffortUpdates(items)
	if len(got) != 3 {
		t.Fatalf("coalesced items = %#v", got)
	}
	first, _ := got[0].(map[string]any)
	reasoning, _ := first["reasoning"].(map[string]any)
	if reasoning["effort"] != "high" {
		t.Fatalf("first update = %#v, want the latest adjacent effort", got[0])
	}
	last, _ := got[2].(map[string]any)
	reasoning, _ = last["reasoning"].(map[string]any)
	if reasoning["effort"] != "high" {
		t.Fatalf("last update = %#v", got[2])
	}
	if len(items) != 6 {
		t.Fatal("saved history was mutated")
	}
}
