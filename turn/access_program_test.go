package turn

import (
	"encoding/json"
	"testing"
)

// Mirrors Rust's two cyber access program enums: the app-server selection is
// camelCase on the wire, and the core value it maps to is snake_case. An unknown
// selection has no core value (Rust's v2 enum rejects it while deserializing).
func TestCyberAccessProgramCoreValueLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		wire string
		want string
	}{
		{wire: "standard", want: "standard"},
		{wire: "daybreakBlue", want: "daybreak_blue"},
		{wire: "daybreakRed", want: "daybreak_red"},
		{wire: "futureProgram", want: ""},
		{wire: "", want: ""},
	} {
		if got := CyberAccessProgram(testCase.wire).CoreValue(); got != testCase.want {
			t.Fatalf("CoreValue(%q) = %q, want %q", testCase.wire, got, testCase.want)
		}
	}
}

func TestTurnStartParamsCyberAccessProgramWireLikeRust(t *testing.T) {
	var params TurnStartParams
	if err := json.Unmarshal([]byte(`{"threadId":"thread-1","cyberAccessProgram":"daybreakRed"}`), &params); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if params.CyberAccessProgram == nil || *params.CyberAccessProgram != CyberAccessProgramDaybreakRed {
		t.Fatalf("decoded program = %#v", params.CyberAccessProgram)
	}
	if got := params.CyberAccessProgram.CoreValue(); got != "daybreak_red" {
		t.Fatalf("core value = %q, want daybreak_red", got)
	}
	encoded, err := json.Marshal(&params)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var object map[string]any
	if err := json.Unmarshal(encoded, &object); err != nil {
		t.Fatal(err)
	}
	if object["cyberAccessProgram"] != "daybreakRed" {
		t.Fatalf("serialized program = %#v", object["cyberAccessProgram"])
	}
}

// An omitted selection stays omitted: it must not be serialized as a program.
func TestTurnStartParamsOmitsAnUnselectedAccessProgram(t *testing.T) {
	encoded, err := json.Marshal(&TurnStartParams{ThreadID: "thread-1", Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err := json.Unmarshal(encoded, &object); err != nil {
		t.Fatal(err)
	}
	if _, present := object["cyberAccessProgram"]; present {
		t.Fatalf("unselected program was serialized: %s", encoded)
	}
}
