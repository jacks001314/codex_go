package tool

import (
	"context"
	"testing"
)

type schemaBudgetTestExecutor struct {
	spec Spec
}

func (e schemaBudgetTestExecutor) Spec() Spec { return e.spec }
func (e schemaBudgetTestExecutor) Execute(context.Context, *Invocation) (*Output, error) {
	return nil, nil
}

func TestRegistryAppliesDefaultCodeModeSchemaBudgetLikeRust(t *testing.T) {
	registry := NewRegistry()
	registry.SetDefaultInputSchemaMaxBytes(24_000)
	if err := registry.Register(schemaBudgetTestExecutor{spec: Spec{Name: PlainName("test-default-budget")}}); err != nil {
		t.Fatalf("Register(default) error = %v", err)
	}
	if err := registry.Register(schemaBudgetTestExecutor{spec: Spec{Name: PlainName("test-explicit-budget"), InputSchemaMaxBytes: 32_000}}); err != nil {
		t.Fatalf("Register(explicit) error = %v", err)
	}
	defaultSpec, ok := registry.Spec(PlainName("test-default-budget"))
	if !ok || defaultSpec.InputSchemaMaxBytes != 24_000 {
		t.Fatalf("default budget = %#v", defaultSpec)
	}
	explicitSpec, ok := registry.Spec(PlainName("test-explicit-budget"))
	if !ok || explicitSpec.InputSchemaMaxBytes != 32_000 {
		t.Fatalf("explicit budget = %#v", explicitSpec)
	}
}
