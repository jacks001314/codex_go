package auth

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

func TestSPIFFEAssertionSelectionClaims(t *testing.T) {
	jwt := func(sub string, aud any) string {
		payload, _ := json.Marshal(map[string]any{"sub": sub, "aud": aud})
		return "e30." + base64.RawURLEncoding.EncodeToString(payload) + ".synthetic"
	}
	id := "spiffe://example.org/workload"
	for _, aud := range []any{"codex", []string{"other", "codex"}} {
		token := jwt(id, aud)
		got, err := selectSPIFFEAssertion([]string{token}, "codex", id)
		if err != nil || got != token {
			t.Fatalf("selection failed: %v", err)
		}
	}
	for _, tc := range []struct {
		svids    []string
		expected string
	}{
		{nil, ""}, {[]string{jwt(id, "codex"), jwt(id, "codex")}, ""},
		{[]string{jwt(id, "other")}, ""}, {[]string{jwt(id, "codex")}, "spiffe://example.org/other"},
		{[]string{jwt("https://example.org/workload", "codex")}, ""},
		{[]string{jwt(id, 3)}, ""}, {[]string{"bad.jwt"}, ""},
	} {
		if _, err := selectSPIFFEAssertion(tc.svids, "codex", tc.expected); err == nil {
			t.Fatal("invalid selection accepted")
		}
	}
}

func TestSPIFFEIDGrammarAndCanonicalization(t *testing.T) {
	for _, value := range []string{"spiffe://domain/", "spiffe://domain/a//b", "spiffe://domain/a:b", "spiffe://domain/a@b", "spiffe://domain/../b", "spiffe://domain/%61", "spiffe://domain:80/a"} {
		if validSPIFFESelectionID(value) {
			t.Errorf("invalid ID accepted: %s", value)
		}
	}
	for _, value := range []string{"SPIFFE://EXAMPLE.ORG/Workload", "spiffe://example_org/a-b.c_0", "spiffe://example.org"} {
		if !validSPIFFESelectionID(value) {
			t.Errorf("valid ID rejected: %s", value)
		}
	}
	payload, _ := json.Marshal(map[string]any{"sub": "SPIFFE://EXAMPLE.ORG/Workload", "aud": "codex"})
	token := "e30." + base64.RawURLEncoding.EncodeToString(payload) + ".synthetic"
	if _, err := selectSPIFFEAssertion([]string{token}, "codex", "spiffe://example.org/Workload"); err != nil {
		t.Fatalf("canonical identity mismatch: %v", err)
	}
}
