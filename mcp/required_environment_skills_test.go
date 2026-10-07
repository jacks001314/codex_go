package mcp

import (
	"errors"
	"testing"

	"codex_go/tool"
)

// TestRequiredEnvironmentSkillsFollowSelectionsLikeRust mirrors Rust #51157
// (`TurnEnvironmentSnapshot::required_skills`): every selected environment
// contributes its `skills.required` names in selection order, a pending or
// failed selection still contributes (the requirement follows the selection,
// not the connection state), an environment with no requirements contributes
// nothing, and an environment that is no longer selected stops applying.
func TestRequiredEnvironmentSkillsFollowSelectionsLikeRust(t *testing.T) {
	required := map[string][]string{
		"required": {"review"},
		"pending":  {"lint", "format"},
	}
	lookup := func(environmentID string) []string { return required[environmentID] }

	selected := NewSelectedEnvironments([]TurnEnvironmentSelection{
		{EnvironmentID: "primary", State: EnvironmentSelectionReady},
		{EnvironmentID: "required", State: EnvironmentSelectionFailed, Error: "configuration unavailable"},
		{EnvironmentID: "pending", State: EnvironmentSelectionPending},
	})
	got := RequiredEnvironmentSkills(selected, lookup)
	if len(got) != 2 {
		t.Fatalf("requirements = %#v, want two environments", got)
	}
	if got[0].EnvironmentID != "required" || len(got[0].SkillNames) != 1 || got[0].SkillNames[0] != "review" {
		t.Fatalf("first requirement = %#v, want the failed selection's review requirement", got[0])
	}
	if got[1].EnvironmentID != "pending" || len(got[1].SkillNames) != 2 || got[1].SkillNames[0] != "lint" || got[1].SkillNames[1] != "format" {
		t.Fatalf("second requirement = %#v, want the pending selection's lint+format requirement", got[1])
	}
	// The lookup result is copied, so a later mutation cannot change a captured
	// requirement.
	required["required"][0] = "mutated"
	if got[0].SkillNames[0] != "review" {
		t.Fatalf("captured requirement mutated = %#v", got[0].SkillNames)
	}

	// Deselection: the same lookup with only the primary environment selected
	// yields no requirements, so its requirements stop applying.
	deselected := NewSelectedEnvironments([]TurnEnvironmentSelection{{EnvironmentID: "primary", State: EnvironmentSelectionReady}})
	if got := RequiredEnvironmentSkills(deselected, lookup); len(got) != 0 {
		t.Fatalf("deselected requirements = %#v, want none", got)
	}

	// A nil snapshot models threadless discovery (Rust `None`) and requires
	// nothing.
	if got := RequiredEnvironmentSkills(nil, lookup); got != nil {
		t.Fatalf("nil snapshot requirements = %#v, want nil", got)
	}
}

// TestValidateRequiredEnvironmentSkillsGatesInferenceLikeRust mirrors Rust
// #51157's `required_environment_skill_gates_inference` cases: only an enabled
// skill supplied by the requiring environment permits inference; a missing,
// disabled, or wrongly attributed skill fails with the Rust message, which the
// turn surfaces as a fatal error before its model request.
func TestValidateRequiredEnvironmentSkillsGatesInferenceLikeRust(t *testing.T) {
	requirements := []EnvironmentSkillRequirements{
		{EnvironmentID: "required", SkillNames: []string{"review"}},
	}
	const wantMessage = `Fatal error: Required skill "review" from environment "required" is unavailable`
	for _, tc := range []struct {
		name    string
		catalog RequiredSkillsCatalog
		ok      bool
	}{
		{
			name: "available",
			catalog: RequiredSkillsCatalog{Available: true, Entries: []EnvironmentSkillCatalogEntry{
				{Name: "review", EnvironmentID: "required", Enabled: true},
			}},
			ok: true,
		},
		{
			name:    "missing",
			catalog: RequiredSkillsCatalog{Available: true},
		},
		{
			name: "disabled",
			catalog: RequiredSkillsCatalog{Available: true, Entries: []EnvironmentSkillCatalogEntry{
				{Name: "review", EnvironmentID: "required", Enabled: false},
			}},
		},
		{
			name: "other environment",
			catalog: RequiredSkillsCatalog{Available: true, Entries: []EnvironmentSkillCatalogEntry{
				{Name: "review", EnvironmentID: "primary", Enabled: true},
			}},
		},
		{
			name: "no environment provenance",
			catalog: RequiredSkillsCatalog{Available: true, Entries: []EnvironmentSkillCatalogEntry{
				{Name: "review", Enabled: true},
			}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateRequiredEnvironmentSkills(requirements, tc.catalog)
			if tc.ok {
				if err != nil {
					t.Fatalf("ValidateRequiredEnvironmentSkills() error = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("ValidateRequiredEnvironmentSkills() = nil, want the missing-skill failure")
			}
			if err.Error() != wantMessage {
				t.Fatalf("error = %q, want %q", err.Error(), wantMessage)
			}
			var callErr *tool.FunctionCallError
			if !tool.AsFunctionCallError(err, &callErr) || !callErr.IsFatal() {
				t.Fatalf("error = %T, want a fatal function-call error (Rust CodexErr::Fatal)", err)
			}
		})
	}
}

// TestValidateRequiredEnvironmentSkillsSkipsWithoutRequirementsLikeRust
// mirrors Rust's early return: an empty requirement list is always valid, even
// when no skills extension state was found.
func TestValidateRequiredEnvironmentSkillsSkipsWithoutRequirementsLikeRust(t *testing.T) {
	if err := ValidateRequiredEnvironmentSkills(nil, RequiredSkillsCatalog{}); err != nil {
		t.Fatalf("ValidateRequiredEnvironmentSkills(nil) error = %v, want nil", err)
	}
	err := ValidateRequiredEnvironmentSkills([]EnvironmentSkillRequirements{{EnvironmentID: "required", SkillNames: []string{"review"}}}, RequiredSkillsCatalog{})
	if !errors.Is(err, ErrRequiredSkillsExtensionUnavailable) {
		t.Fatalf("ValidateRequiredEnvironmentSkills() error = %v, want ErrRequiredSkillsExtensionUnavailable", err)
	}
	if got, want := err.Error(), "required skills cannot be validated because the skills extension is unavailable"; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}
