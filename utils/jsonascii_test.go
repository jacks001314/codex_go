package utils

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMarshalStringEscapesNonASCIIStrings(t *testing.T) {
	value := map[string]any{
		"workspaces": map[string]any{
			"/tmp/東京": map[string]any{
				"label": "Agentlarım",
				"emoji": "🚀",
			},
		},
	}
	serialized, err := MarshalString(value)
	if err != nil {
		t.Fatal(err)
	}
	if !isASCII(serialized) {
		t.Fatalf("serialized is not ascii: %q", serialized)
	}
	if strings.Contains(serialized, "東京") || strings.Contains(serialized, "Agentlarım") || strings.Contains(serialized, "🚀") {
		t.Fatalf("non-ascii leaked: %q", serialized)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(serialized), &parsed); err != nil {
		t.Fatal(err)
	}
	workspaces := parsed["workspaces"].(map[string]any)
	if _, ok := workspaces["/tmp/東京"]; !ok {
		t.Fatalf("parsed value = %#v", parsed)
	}
}

func isASCII(value string) bool {
	for i := 0; i < len(value); i++ {
		if value[i] > 0x7f {
			return false
		}
	}
	return true
}
