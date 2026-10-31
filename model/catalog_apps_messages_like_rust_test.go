package model

import (
	"encoding/json"
	"strings"
	"testing"
)

// Mirrors Rust #58441 apps_messages_preserve_nullable_and_literal_wire_values:
// null and missing forms keep no apps object, while every non-null
// usage_instructions string is preserved byte for byte.
func TestAppsMessagesPreserveNullableAndLiteralWireValuesLikeRust(t *testing.T) {
	cases := []string{
		"null",
		"{}",
		`{"usage_instructions":null}`,
		`{"usage_instructions":""}`,
		`{"usage_instructions":" \r\n\t"}`,
		`{"usage_instructions":"  {{literal}}\r\n"}`,
		`{"usage_instructions":"` + strings.Repeat("é", 501) + `"}`,
	}
	for _, apps := range cases {
		var messages ModelMessages
		if err := json.Unmarshal([]byte(`{"apps":`+apps+`}`), &messages); err != nil {
			t.Fatalf("Unmarshal() error = %v", err)
		}
		encoded, err := json.Marshal(messages)
		if err != nil {
			t.Fatalf("Marshal() error = %v", err)
		}
		var got map[string]any
		if err := json.Unmarshal(encoded, &got); err != nil {
			t.Fatalf("decode encoded messages: %v", err)
		}
		appsObject, present := got["apps"]
		if apps == "null" {
			if present {
				t.Fatalf("apps = %#v, want omitted for null", appsObject)
			}
			continue
		}
		var want map[string]any
		if err := json.Unmarshal([]byte(apps), &want); err != nil {
			t.Fatalf("decode expected apps: %v", err)
		}
		if usage, ok := want["usage_instructions"]; !ok || usage == nil {
			delete(want, "usage_instructions")
		}
		gotApps, ok := appsObject.(map[string]any)
		if !ok || len(gotApps) != len(want) {
			t.Fatalf("apps = %#v, want %#v", appsObject, want)
		}
		for key, value := range want {
			if gotApps[key] != value {
				t.Fatalf("apps[%s] = %#v, want %#v", key, gotApps[key], value)
			}
		}
	}
}
