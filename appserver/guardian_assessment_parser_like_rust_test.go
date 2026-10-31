package appserver

import (
	"encoding/json"
	"reflect"
	"testing"
)

// TestGuardianAssessmentOutputSchemaRequiresOnlyOutcomeLikeRust mirrors Rust
// guardian_output_schema: outcome is the only required property, so a bare
// {"outcome":"allow"} answer remains a valid structured response.
func TestGuardianAssessmentOutputSchemaRequiresOnlyOutcomeLikeRust(t *testing.T) {
	raw, ok := guardianAssessmentOutputSchema().(json.RawMessage)
	if !ok {
		t.Fatalf("guardianAssessmentOutputSchema() = %T, want json.RawMessage", guardianAssessmentOutputSchema())
	}
	var schema struct {
		Required             []string       `json:"required"`
		Properties           map[string]any `json:"properties"`
		AdditionalProperties bool           `json:"additionalProperties"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	if !reflect.DeepEqual(schema.Required, []string{"outcome"}) {
		t.Fatalf("required = %#v, want [outcome]", schema.Required)
	}
	if schema.AdditionalProperties {
		t.Fatal("additionalProperties = true, want false")
	}
	for _, field := range []string{"riskLevel", "userAuthorization", "outcome", "rationale"} {
		if _, ok := schema.Properties[field]; !ok {
			t.Fatalf("properties missing %q: %#v", field, schema.Properties)
		}
	}
}
