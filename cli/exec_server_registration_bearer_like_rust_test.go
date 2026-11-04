package cli

import (
	"strings"
	"testing"
)

// Rust #59254: --registration-bearer-stdin requires --remote and cannot be
// combined with the other registration auth modes.
func TestExecServerRegistrationBearerStdinFlagLikeRust(t *testing.T) {
	registry := []string{"exec-server", "--remote", "https://registry.example.com", "--environment-id", "env-1"}
	parsed, err := Parse(append(append([]string{}, registry...), "--registration-bearer-stdin"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !parsed.ExecServer.RegistrationBearerStdin {
		t.Fatal("RegistrationBearerStdin = false, want true")
	}

	if _, err := Parse([]string{"exec-server", "--registration-bearer-stdin"}); err == nil || !strings.Contains(err.Error(), "required arguments were not provided") {
		t.Fatalf("bare --registration-bearer-stdin error = %v", err)
	}
	for _, conflict := range []string{"--use-agent-identity-auth", "--executor-credential-stdin", "--aws-sigv4"} {
		_, err := Parse(append(append([]string{}, registry...), "--registration-bearer-stdin", conflict))
		if err == nil || !strings.Contains(err.Error(), "cannot be used with") {
			t.Fatalf("conflict %s error = %v", conflict, err)
		}
	}
}
