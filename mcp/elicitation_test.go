package mcp

import "testing"

func TestElicitationSchemaNormalizeAndValidate(t *testing.T) {
	schema := (&McpElicitationSchema{
		Properties: map[string]McpElicitationPrimitiveSchema{
			" title ": {Title: "Title"},
		},
		Required: []string{"title"},
	}).Normalize()
	if schema.Type != McpElicitationTypeObject {
		t.Fatalf("Type = %q", schema.Type)
	}
	if schema.Properties["title"].Type != "string" {
		t.Fatalf("property = %#v", schema.Properties["title"])
	}
	if err := schema.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestElicitationSchemaRejectsUndeclaredRequiredProperty(t *testing.T) {
	err := (&McpElicitationSchema{
		Properties: map[string]McpElicitationPrimitiveSchema{"name": {Type: "string"}},
		Required:   []string{"missing"},
	}).Validate()
	if err == nil {
		t.Fatalf("expected validation error")
	}
}
