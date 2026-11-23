package cli

import (
	"errors"
	"strings"
	"testing"
)

// TestHiddenCommandClapDiagnosticsLikeRust pins clap's blocks for the parsers
// that used to answer in Go wording: `execpolicy check` and the hidden
// `tcp-tunnel`.
func TestHiddenCommandClapDiagnosticsLikeRust(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		message string
		usage   string
	}{
		{
			name:    "execpolicy check missing both",
			args:    []string{"execpolicy", "check"},
			message: "the following required arguments were not provided:\n  --rules <PATH>\n  <COMMAND>...",
			usage:   "Usage: codex execpolicy check --rules <PATH> <COMMAND>...",
		},
		{
			name:    "execpolicy check missing command",
			args:    []string{"execpolicy", "check", "--rules", "policy.rules"},
			message: "the following required arguments were not provided:\n  <COMMAND>...",
			usage:   "Usage: codex execpolicy check --rules <PATH> <COMMAND>...",
		},
		{
			name:    "execpolicy check echoes consumed booleans",
			args:    []string{"execpolicy", "check", "-r", "policy.rules", "--pretty", "--resolve-host-executables"},
			message: "the following required arguments were not provided:\n  <COMMAND>...",
			usage:   "Usage: codex execpolicy check --rules <PATH> --pretty --resolve-host-executables <COMMAND>...",
		},
		{
			name:    "execpolicy check hyphen command",
			args:    []string{"execpolicy", "check", "--dry-run", "--rules", "policy.rules"},
			message: "the following required arguments were not provided:\n  --rules <PATH>",
			usage:   "Usage: codex execpolicy check --rules <PATH> <COMMAND>...",
		},
		{
			name:    "tcp-tunnel missing required",
			args:    []string{"tcp-tunnel"},
			message: "the following required arguments were not provided:\n  --environment-id <ENVIRONMENT_ID>",
			usage:   "Usage: codex tcp-tunnel --environment-id <ENVIRONMENT_ID>",
		},
		{
			name:    "tcp-tunnel port requires environment id",
			args:    []string{"tcp-tunnel", "--port", "22"},
			message: "the following required arguments were not provided:\n  --environment-id <ENVIRONMENT_ID>",
			usage:   "Usage: codex tcp-tunnel --environment-id <ENVIRONMENT_ID> --port <PORT>",
		},
		{
			name:    "tcp-tunnel legacy target requires token file",
			args:    []string{"tcp-tunnel", "--target", "10.0.0.1:22"},
			message: "the following required arguments were not provided:\n  --token-file <TOKEN_FILE>",
			usage:   "Usage: codex tcp-tunnel --token-file <TOKEN_FILE> --target <TARGET>",
		},
		{
			name:    "tcp-tunnel unknown flag",
			args:    []string{"tcp-tunnel", "--bogus"},
			message: "unexpected argument '--bogus' found",
			usage:   "Usage: codex tcp-tunnel [OPTIONS]",
		},
		{
			name:    "tcp-tunnel pending value",
			args:    []string{"tcp-tunnel", "--proxy-url"},
			message: "a value is required for '--proxy-url <PROXY_URL>' but none was supplied",
		},
		{
			name:    "tcp-tunnel relative proxy url",
			args:    []string{"tcp-tunnel", "--proxy-url", "bad", "--environment-id", "env"},
			message: "invalid value 'bad' for '--proxy-url <PROXY_URL>': relative URL without a base",
		},
		{
			name:    "tcp-tunnel empty host",
			args:    []string{"tcp-tunnel", "--proxy-url", "https://", "--environment-id", "env"},
			message: "invalid value 'https://' for '--proxy-url <PROXY_URL>': empty host",
		},
		{
			name:    "tcp-tunnel invalid port",
			args:    []string{"tcp-tunnel", "--proxy-url", "https://p.example:abc", "--environment-id", "env"},
			message: "invalid value 'https://p.example:abc' for '--proxy-url <PROXY_URL>': invalid port number",
		},
		{
			name:    "tcp-tunnel invalid ipv6",
			args:    []string{"tcp-tunnel", "--proxy-url", "https://[::1", "--environment-id", "env"},
			message: "invalid value 'https://[::1' for '--proxy-url <PROXY_URL>': invalid IPv6 address",
		},
		{
			name:    "tcp-tunnel invalid legacy target",
			args:    []string{"tcp-tunnel", "--target", "bad", "--token-file", "f"},
			message: "invalid value 'bad' for '--target <TARGET>': invalid socket address syntax",
		},
		{
			name:    "tcp-tunnel invalid ssh port",
			args:    []string{"tcp-tunnel", "--environment-id", "env", "--port", "0"},
			message: "invalid value '0' for '--port <PORT>': number would be zero for non-zero type",
		},
		{
			name:    "tcp-tunnel environment id conflicts with target",
			args:    []string{"tcp-tunnel", "--environment-id", "env", "--target", "10.0.0.1:22"},
			message: "the argument '--environment-id <ENVIRONMENT_ID>' cannot be used with '--target <TARGET>'",
			usage:   "Usage: codex tcp-tunnel --environment-id <ENVIRONMENT_ID>",
		},
		{
			name:    "tcp-tunnel duplicate flag",
			args:    []string{"tcp-tunnel", "--environment-id", "env", "--port", "22", "--port", "23"},
			message: "the argument '--port <PORT>' cannot be used multiple times",
			usage:   "Usage: codex tcp-tunnel [OPTIONS]",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			parsed, err := Parse(testCase.args)
			if parsed != nil {
				t.Fatalf("Parse(%v) returned %#v with the diagnostic", testCase.args, parsed)
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

// TestStdioToUDSSubcommandRemovedLikeRust pins Rust #62163: the relay moved into
// the CLI crate and the hidden `codex stdio-to-uds` subcommand is gone, so the
// token falls back to the root PROMPT positional instead of selecting a
// command; `codex app-server proxy` keeps the same bridge.
func TestStdioToUDSSubcommandRemovedLikeRust(t *testing.T) {
	prompt, err := Parse([]string{"stdio-to-uds"})
	if err != nil {
		t.Fatalf("Parse(stdio-to-uds) error = %v", err)
	}
	if prompt.Command != CommandInteractive || prompt.Root.Prompt != "stdio-to-uds" {
		t.Fatalf("Parse(stdio-to-uds) = %q prompt %q, want the interactive prompt", prompt.Command, prompt.Root.Prompt)
	}
	parsed, err := Parse([]string{"stdio-to-uds", "a", "b"})
	if err == nil || parsed != nil {
		t.Fatalf("Parse(stdio-to-uds a b) = %#v, %v; want the extra positional rejected", parsed, err)
	}
	if !strings.Contains(err.Error(), "unexpected argument 'a' found") {
		t.Fatalf("error = %v, want clap's extra-positional diagnostic", err)
	}
	proxy, err := Parse([]string{"app-server", "proxy", "--sock", `\\.\pipe\codex-app-server`})
	if err != nil {
		t.Fatalf("Parse(app-server proxy) error = %v", err)
	}
	if proxy.Command != CommandAppServer || len(proxy.AppServer.Subcommand) == 0 || proxy.AppServer.Subcommand[0] != "proxy" {
		t.Fatalf("app-server proxy = %#v", proxy.AppServer)
	}
	if proxy.AppServer.Proxy.SocketPath != `\\.\pipe\codex-app-server` {
		t.Fatalf("proxy socket = %q", proxy.AppServer.Proxy.SocketPath)
	}
}
