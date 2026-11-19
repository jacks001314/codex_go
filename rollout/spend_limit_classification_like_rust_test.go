package rollout

import "testing"

// TestScopedSpendLimitClassificationSurvivesLikeRust mirrors Rust #62329's
// CodexErrorInfo variants: saved turn errors keep their scoped spend limit
// instead of collapsing into `other`.
func TestScopedSpendLimitClassificationSurvivesLikeRust(t *testing.T) {
	for _, tag := range []string{
		"api_key_spend_limit_exceeded",
		"apiKeySpendLimitExceeded",
		"project_spend_limit_exceeded",
		"projectSpendLimitExceeded",
		"organization_spend_limit_exceeded",
		"organizationSpendLimitExceeded",
	} {
		classified, ok := normalizeCodexErrorInfoClassification(tag)
		if !ok || classified != tag {
			t.Fatalf("normalizeCodexErrorInfoClassification(%q) = %#v, %t; want preserved", tag, classified, ok)
		}
		classified, ok = normalizeCodexErrorInfoClassification(map[string]any{tag: nil})
		if !ok {
			t.Fatalf("normalizeCodexErrorInfoClassification({%q: nil}) is unreadable", tag)
		}
		encoded, isMap := classified.(map[string]any)
		if !isMap {
			t.Fatalf("classification = %#v, want the tagged object", classified)
		}
		if _, exists := encoded[tag]; !exists {
			t.Fatalf("classification = %#v, want the %q tag", encoded, tag)
		}
	}
}
