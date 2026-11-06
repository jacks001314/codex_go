package model

import (
	"strings"
	"testing"

	"codex_go/auth"
	"codex_go/codexapi"
)

// TestValidateRequiredCyberAccessProgramsLikeRust mirrors Rust #59754: a thread
// that requires an allowed explicit program rejects omission and disallowed
// selections, while an allowed selection passes.
func TestValidateRequiredCyberAccessProgramsLikeRust(t *testing.T) {
	allowed := []string{"daybreak_blue"}
	if err := ValidateRequiredCyberAccessPrograms(nil, nil); err != nil {
		t.Fatalf("unconstrained thread error = %v", err)
	}
	for _, programs := range []*AccessPrograms{nil, {Cyber: ""}, {Cyber: "daybreak_red"}} {
		err := ValidateRequiredCyberAccessPrograms(allowed, programs)
		apiErr, ok := err.(*codexapi.APIError)
		if !ok || apiErr.Kind != codexapi.ErrorInvalidRequest || !strings.Contains(apiErr.Message, "allowed explicit access program") {
			t.Fatalf("programs %+v error = %v", programs, err)
		}
	}
	selected, err := AccessProgramsForAuth("daybreak_blue", &auth.AuthDotJSON{AuthMode: "chatgpt"}, ApiKeyCyberAccessProgramsUnsupportedProvider)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateRequiredCyberAccessPrograms(allowed, selected); err != nil {
		t.Fatalf("allowed selection error = %v", err)
	}
}
