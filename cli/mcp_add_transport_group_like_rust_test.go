package cli

import (
	"errors"
	"testing"
)

// TestMCPAddTransportGroupLikeRust pins clap's group selection for the
// streamable-HTTP options: --url and a COMMAND conflict inside the transport
// group, while --env selects the stdio transport and reports the missing
// command at run time.
func TestMCPAddTransportGroupLikeRust(t *testing.T) {
	const mcpAddUsage = "Usage: codex mcp add [OPTIONS] <NAME> (--url <URL> | -- <COMMAND>...)"
	conflict := "the argument '--url <URL>' cannot be used with '[COMMAND]...'"
	cases := []struct {
		name    string
		args    []string
		message string
	}{
		{
			name:    "url conflicts with command",
			args:    []string{"mcp", "add", "fs", "--url", "https://x", "--", "mcp-fs"},
			message: conflict,
		},
		{
			name:    "url conflicts with command after env",
			args:    []string{"mcp", "add", "fs", "--url", "https://x", "--env", "K=V", "--", "echo", "hi"},
			message: conflict,
		},
		{
			name:    "url conflicts with command after oauth",
			args:    []string{"mcp", "add", "fs", "--url", "https://x", "--oauth-client-id", "c1", "--", "echo", "hi"},
			message: conflict,
		},
		{
			name:    "env selects stdio and requires a command",
			args:    []string{"mcp", "add", "fs", "--url", "https://x", "--env", "K=V"},
			message: "command is required",
		},
		{
			name:    "env before url requires a command",
			args:    []string{"mcp", "add", "fs", "--env", "K=V", "--url", "https://x"},
			message: "command is required",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			parsed, err := Parse(testCase.args)
			if err == nil {
				t.Fatalf("Parse(%v) returned %#v, want a transport diagnostic", testCase.args, parsed)
			}
			if err.Error() != testCase.message {
				t.Fatalf("Parse(%v) error = %q, want %q", testCase.args, err.Error(), testCase.message)
			}
			var usage *UsageError
			isUsage := errors.As(err, &usage)
			if testCase.message == conflict {
				if !isUsage || usage.Usage != mcpAddUsage {
					t.Fatalf("Parse(%v) = %#v, want the mcp add usage block", testCase.args, err)
				}
				return
			}
			if isUsage {
				t.Fatalf("Parse(%v) = %#v, want a run-time error", testCase.args, err)
			}
		})
	}
}
