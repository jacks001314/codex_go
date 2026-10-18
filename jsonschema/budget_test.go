package jsonschema

import (
	"strings"
	"testing"
)

func TestNormalizeWithBudgetPreservesGuidanceLikeRust(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "Use this query for the exact account lookup. " + strings.Repeat("detail ", 1800),
			},
		},
	}
	defaultSchema := NormalizeWithBudget(cloneJSON(schema).(map[string]any), DefaultCompactToolSchemaBytes)
	queryDefault, _ := defaultSchema["properties"].(map[string]any)["query"].(map[string]any)
	if _, ok := queryDefault["description"]; ok {
		t.Fatalf("default budget retained oversized description: %#v", queryDefault)
	}

	largeSchema := NormalizeWithBudget(cloneJSON(schema).(map[string]any), 32_000)
	queryLarge, _ := largeSchema["properties"].(map[string]any)["query"].(map[string]any)
	if description, _ := queryLarge["description"].(string); !strings.Contains(description, "exact account lookup") {
		t.Fatalf("large budget lost parameter guidance: %#v", queryLarge)
	}
}
