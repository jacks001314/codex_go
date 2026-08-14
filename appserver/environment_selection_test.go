package appserver

import (
	"reflect"
	"testing"

	"codex_go/turn"
)

func TestSelectedEnvironmentIDsDeduplicatesAndNormalizes(t *testing.T) {
	got := selectedEnvironmentIDs(&turn.TurnStartParams{Environments: []map[string]any{
		{"environmentId": "primary"},
		{"environment_id": "primary"},
		{"environmentId": "secondary"},
	}})
	if want := []string{"primary", "secondary"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("selectedEnvironmentIDs() = %#v, want %#v", got, want)
	}
	if got := selectedEnvironmentIDs(nil); got != nil {
		t.Fatalf("selectedEnvironmentIDs(nil) = %#v", got)
	}
}

func TestEnvironmentSelectionsFromAnyNormalizesSliceShapes(t *testing.T) {
	got := environmentSelectionsFromAny([]any{
		map[string]any{"environmentId": "a"},
		map[string]any{"environment_id": "b"},
	})
	if len(got) != 2 || got[0]["environmentId"] != "a" || got[1]["environment_id"] != "b" {
		t.Fatalf("environmentSelectionsFromAny() = %#v", got)
	}
	if got := environmentSelectionsFromAny("bad"); got != nil {
		t.Fatalf("environmentSelectionsFromAny(bad) = %#v", got)
	}
}
