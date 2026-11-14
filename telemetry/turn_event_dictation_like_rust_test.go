package telemetry

import (
	"encoding/json"
	"testing"
)

func TestCodexTurnEventDictationLikeRust(t *testing.T) {
	trueValue, falseValue := true, false
	for _, tc := range []struct {
		name  string
		value *bool
		want  any
	}{
		{name: "unknown omits", want: nil},
		{name: "true", value: &trueValue, want: true},
		{name: "false", value: &falseValue, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			event := NewCodexTurnEvent(CodexTurnEventInput{ThreadID: "thread", TurnID: "turn", UsedDictation: tc.value})
			data, err := json.Marshal(event)
			if err != nil {
				t.Fatal(err)
			}
			var decoded struct {
				EventParams map[string]json.RawMessage `json:"event_params"`
			}
			if err := json.Unmarshal(data, &decoded); err != nil {
				t.Fatal(err)
			}
			raw, ok := decoded.EventParams["used_dictation"]
			if tc.want == nil {
				if ok {
					t.Fatalf("unknown dictation serialized as %s", raw)
				}
				return
			}
			if !ok {
				t.Fatal("used_dictation missing")
			}
			var got bool
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatal(err)
			}
			if got != tc.want.(bool) {
				t.Fatalf("used_dictation = %t, want %t", got, tc.want)
			}
		})
	}
}
