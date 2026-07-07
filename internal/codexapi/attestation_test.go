package codexapi

import (
	"context"
	"testing"
)

func TestGeneratorSkipsProviderWhenDisabled(t *testing.T) {
	provider := NewCountingAttestationProvider("attest")
	generator := NewAttestationGenerator(false, provider)
	_, ok, err := generator.Header(context.Background(), &AttestationContext{ThreadID: "thread"})
	if err != nil {
		t.Fatalf("header failed: %v", err)
	}
	if ok {
		t.Fatalf("expected no header when disabled")
	}
	if provider.Calls() != 0 {
		t.Fatalf("provider should not be called")
	}
}

func TestGeneratorReturnsHeaderWhenEnabled(t *testing.T) {
	provider := NewCountingAttestationProvider(" attest ")
	generator := NewAttestationGenerator(true, provider)
	value, ok, err := generator.Header(context.Background(), &AttestationContext{ThreadID: "thread"})
	if err != nil {
		t.Fatalf("header failed: %v", err)
	}
	if !ok || value != "attest" {
		t.Fatalf("unexpected header %q %v", value, ok)
	}
	if provider.Calls() != 1 {
		t.Fatalf("provider calls = %d", provider.Calls())
	}
}

func TestAddHeaderIgnoresBlankValues(t *testing.T) {
	headers := AddAttestationHeader(nil, " ")
	if headers != nil {
		t.Fatalf("blank header should not allocate map")
	}
	headers = AddAttestationHeader(map[string]string{"x": "y"}, "token")
	if headers[AttestationHeader] != "token" {
		t.Fatalf("attestation header missing: %#v", headers)
	}
}
