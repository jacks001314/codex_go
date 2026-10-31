package cli

import (
	"errors"
	"testing"
)

// TestMCPOAuthRegistrationClapDiagnosticsLikeRust pins clap's diagnostics for
// the `mcp add` streamable-HTTP options and the `mcp login` registration flag:
// a single missing `requires` target is reported against the root command with
// the bare argument id, while `requires_all` violations keep the `mcp add`
// usage block.
func TestMCPOAuthRegistrationClapDiagnosticsLikeRust(t *testing.T) {
	const mcpAddUsage = "Usage: codex mcp add [OPTIONS] <NAME> (--url <URL> | -- <COMMAND>...)"
	cases := []struct {
		name    string
		args    []string
		message string
		usage   string
	}{
		{
			name:    "client id requires url",
			args:    []string{"mcp", "add", "fs", "--oauth-client-id", "c1", "--", "mcp-fs"},
			message: "the following required argument was not provided: url",
			usage:   rootUsageText,
		},
		{
			name:    "client secret requires url and client id",
			args:    []string{"mcp", "add", "fs", "--oauth-client-secret", "s", "--", "mcp-fs"},
			message: "the following required arguments were not provided:\n  --url <URL>\n  --oauth-client-id <CLIENT_ID>",
			usage:   mcpAddUsage,
		},
		{
			name:    "oauth resource requires url",
			args:    []string{"mcp", "add", "fs", "--oauth-resource", "r", "--", "mcp-fs"},
			message: "the following required argument was not provided: url",
			usage:   rootUsageText,
		},
		{
			name:    "client secret requires client id",
			args:    []string{"mcp", "add", "fs", "--url", "https://x", "--oauth-client-secret", "s"},
			message: "the following required arguments were not provided:\n  --oauth-client-id <CLIENT_ID>",
			usage:   mcpAddUsage,
		},
		{
			name:    "registration requires url",
			args:    []string{"mcp", "add", "fs", "--oauth-client-registration", "auto", "--", "mcp-fs"},
			message: "the following required argument was not provided: url",
			usage:   rootUsageText,
		},
		{
			name:    "registration invalid value",
			args:    []string{"mcp", "add", "fs", "--oauth-client-registration", "bogus", "--", "mcp-fs"},
			message: "invalid value 'bogus' for '--oauth-client-registration <AUTO|CIMD|DCR>'\n  [possible values: auto, cimd, dcr]",
		},
		{
			name:    "registration value is case sensitive",
			args:    []string{"mcp", "add", "fs", "--oauth-client-registration", "DCR", "--", "mcp-fs"},
			message: "invalid value 'DCR' for '--oauth-client-registration <AUTO|CIMD|DCR>'\n  [possible values: auto, cimd, dcr]",
		},
		{
			name:    "registration missing value",
			args:    []string{"mcp", "add", "fs", "--oauth-client-registration"},
			message: "a value is required for '--oauth-client-registration <AUTO|CIMD|DCR>' but none was supplied\n  [possible values: auto, cimd, dcr]",
		},
		{
			name:    "registration empty value",
			args:    []string{"mcp", "add", "fs", "--oauth-client-registration=", "--", "mcp-fs"},
			message: "a value is required for '--oauth-client-registration <AUTO|CIMD|DCR>' but none was supplied\n  [possible values: auto, cimd, dcr]",
		},
		{
			name:    "login registration missing value",
			args:    []string{"mcp", "login", "fs", "--oauth-client-registration"},
			message: "a value is required for '--oauth-client-registration <AUTO|CIMD|DCR>' but none was supplied\n  [possible values: auto, cimd, dcr]",
		},
		{
			name:    "login registration invalid value",
			args:    []string{"mcp", "login", "fs", "--oauth-client-registration", "bogus"},
			message: "invalid value 'bogus' for '--oauth-client-registration <AUTO|CIMD|DCR>'\n  [possible values: auto, cimd, dcr]",
		},
		{
			name:    "bearer token requires url",
			args:    []string{"mcp", "add", "fs", "--bearer-token-env-var", "TOKEN", "--", "mcp-fs"},
			message: "the following required argument was not provided: url",
			usage:   rootUsageText,
		},
		{
			name:    "empty client id still requires url",
			args:    []string{"mcp", "add", "fs", "--oauth-client-id", "", "--", "mcp-fs"},
			message: "the following required argument was not provided: url",
			usage:   rootUsageText,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			parsed, err := Parse(testCase.args)
			if err == nil {
				t.Fatalf("Parse(%v) returned %#v, want diagnostic", testCase.args, parsed)
			}
			var usage *UsageError
			if !errors.As(err, &usage) {
				t.Fatalf("Parse(%v) error = %T %v, want UsageError", testCase.args, err, err)
			}
			if usage.Message != testCase.message {
				t.Fatalf("Parse(%v) message = %q, want %q", testCase.args, usage.Message, testCase.message)
			}
			if usage.Usage != testCase.usage {
				t.Fatalf("Parse(%v) usage = %q, want %q", testCase.args, usage.Usage, testCase.usage)
			}
		})
	}
}

// TestMCPOAuthRegistrationParseLikeRust keeps the accepted spellings wired to
// the registration strategy.
func TestMCPOAuthRegistrationParseLikeRust(t *testing.T) {
	parsed, err := Parse([]string{"mcp", "add", "fs", "--url", "https://x", "--oauth-client-id", "c1",
		"--oauth-client-secret", "s", "--oauth-client-registration", "dcr"})
	if err != nil {
		t.Fatalf("Parse(mcp add --oauth-client-registration dcr) error = %v", err)
	}
	if parsed.MCP.OAuthClientRegistration != "dcr" || parsed.MCP.OAuthClientID != "c1" {
		t.Fatalf("MCP = %#v", parsed.MCP)
	}

	parsed, err = Parse([]string{"mcp", "add", "fs", "--oauth-client-registration=auto", "--url", "https://x"})
	if err != nil {
		t.Fatalf("Parse(mcp add --oauth-client-registration=auto) error = %v", err)
	}
	if parsed.MCP.OAuthClientRegistration != "auto" {
		t.Fatalf("registration = %q, want auto", parsed.MCP.OAuthClientRegistration)
	}

	parsed, err = Parse([]string{"mcp", "login", "fs", "--oauth-client-registration", "cimd"})
	if err != nil {
		t.Fatalf("Parse(mcp login --oauth-client-registration cimd) error = %v", err)
	}
	if parsed.MCP.Name != "fs" || parsed.MCP.OAuthClientRegistration != "cimd" {
		t.Fatalf("MCP = %#v", parsed.MCP)
	}
}

// TestMCPAddEmptyClientSecretLikeRust keeps Rust's app-level guard: clap
// accepts the empty value, so the command reports it during the run.
func TestMCPAddEmptyClientSecretLikeRust(t *testing.T) {
	_, err := Parse([]string{"mcp", "add", "fs", "--url", "https://x", "--oauth-client-id", "c1", "--oauth-client-secret", ""})
	if err == nil || err.Error() != "--oauth-client-secret must not be empty" {
		t.Fatalf("Parse error = %v, want --oauth-client-secret must not be empty", err)
	}
	var usage *UsageError
	if errors.As(err, &usage) {
		t.Fatalf("error = %#v, want a plain run-time error", usage)
	}
}
