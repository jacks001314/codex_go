package cli

import (
	"errors"
	"testing"
)

// TestHiddenCommandClapDiagnosticsLikeRust pins clap's blocks for the three
// parsers that used to answer in Go wording: `execpolicy check`,
// `stdio-to-uds` and the hidden `tcp-tunnel`.
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
			name:    "stdio-to-uds missing path",
			args:    []string{"stdio-to-uds"},
			message: "the following required arguments were not provided:\n  <SOCKET_PATH>",
			usage:   "Usage: codex stdio-to-uds <SOCKET_PATH>",
		},
		{
			name:    "stdio-to-uds unknown flag",
			args:    []string{"stdio-to-uds", "--bogus"},
			message: "unexpected argument '--bogus' found\n\n  tip: to pass '--bogus' as a value, use '-- --bogus'",
			usage:   "Usage: codex stdio-to-uds [OPTIONS] <SOCKET_PATH>",
		},
		{
			name:    "stdio-to-uds extra positional",
			args:    []string{"stdio-to-uds", "a", "b"},
			message: "unexpected argument 'b' found",
			usage:   "Usage: codex stdio-to-uds [OPTIONS] <SOCKET_PATH>",
		},
		{
			name:    "tcp-tunnel missing required",
			args:    []string{"tcp-tunnel"},
			message: "the following required arguments were not provided:\n  --proxy-url <PROXY_URL>\n  --proxy-origins-file <PROXY_ORIGINS_FILE>\n  --target <TARGET>",
			usage:   "Usage: codex tcp-tunnel --proxy-url <PROXY_URL> --proxy-origins-file <PROXY_ORIGINS_FILE> --target <TARGET>",
		},
		{
			name:    "tcp-tunnel echoes consumed flags",
			args:    []string{"tcp-tunnel", "--connect-headers-stdin", "--listen-addr", "127.0.0.1:80"},
			message: "the following required arguments were not provided:\n  --proxy-url <PROXY_URL>\n  --proxy-origins-file <PROXY_ORIGINS_FILE>\n  --target <TARGET>\n  --auth-token-stdin",
			usage:   "Usage: codex tcp-tunnel --proxy-url <PROXY_URL> --proxy-origins-file <PROXY_ORIGINS_FILE> --target <TARGET> --auth-token-stdin --connect-headers-stdin --listen-addr <LISTEN_ADDR>",
		},
		{
			name:    "tcp-tunnel requires auth for updates",
			args:    []string{"tcp-tunnel", "--target", "z", "--auth-token-updates-stdin"},
			message: "the following required arguments were not provided:\n  --proxy-url <PROXY_URL>\n  --proxy-origins-file <PROXY_ORIGINS_FILE>\n  --auth-token-stdin",
			usage:   "Usage: codex tcp-tunnel --proxy-url <PROXY_URL> --proxy-origins-file <PROXY_ORIGINS_FILE> --target <TARGET> --auth-token-stdin --auth-token-updates-stdin",
		},
		{
			name:    "tcp-tunnel unknown flag",
			args:    []string{"tcp-tunnel", "--bogus"},
			message: "unexpected argument '--bogus' found",
			usage:   "Usage: codex tcp-tunnel [OPTIONS] --proxy-url <PROXY_URL> --proxy-origins-file <PROXY_ORIGINS_FILE> --target <TARGET>",
		},
		{
			name:    "tcp-tunnel pending value",
			args:    []string{"tcp-tunnel", "--proxy-url"},
			message: "a value is required for '--proxy-url <PROXY_URL>' but none was supplied",
		},
		{
			name:    "tcp-tunnel relative proxy url",
			args:    []string{"tcp-tunnel", "--proxy-url", "bad", "--proxy-origins-file", "y", "--target", "z"},
			message: "invalid value 'bad' for '--proxy-url <PROXY_URL>': relative URL without a base",
		},
		{
			name:    "tcp-tunnel empty host",
			args:    []string{"tcp-tunnel", "--proxy-url", "https://", "--proxy-origins-file", "y", "--target", "z"},
			message: "invalid value 'https://' for '--proxy-url <PROXY_URL>': empty host",
		},
		{
			name:    "tcp-tunnel invalid port",
			args:    []string{"tcp-tunnel", "--proxy-url", "https://p.example:abc", "--proxy-origins-file", "y", "--target", "z"},
			message: "invalid value 'https://p.example:abc' for '--proxy-url <PROXY_URL>': invalid port number",
		},
		{
			name:    "tcp-tunnel invalid ipv6",
			args:    []string{"tcp-tunnel", "--proxy-url", "https://[::1", "--proxy-origins-file", "y", "--target", "z"},
			message: "invalid value 'https://[::1' for '--proxy-url <PROXY_URL>': invalid IPv6 address",
		},
		{
			name:    "tcp-tunnel invalid listen address",
			args:    []string{"tcp-tunnel", "--proxy-url", "https://p.example", "--proxy-origins-file", "y", "--target", "z", "--listen-addr", "bad"},
			message: "invalid value 'bad' for '--listen-addr <LISTEN_ADDR>': invalid socket address syntax",
		},
		{
			name:    "tcp-tunnel boolean value",
			args:    []string{"tcp-tunnel", "--auth-token-stdin=x"},
			message: "unexpected value 'x' for '--auth-token-stdin' found; no more were expected",
			usage:   "Usage: codex tcp-tunnel --proxy-url <PROXY_URL> --proxy-origins-file <PROXY_ORIGINS_FILE> --target <TARGET> --auth-token-stdin",
		},
		{
			name:    "tcp-tunnel boolean value with consumed options",
			args:    []string{"tcp-tunnel", "--proxy-url", "https://p.example", "--proxy-origins-file", "y", "--auth-token-stdin=x"},
			message: "unexpected value 'x' for '--auth-token-stdin' found; no more were expected",
			usage:   "Usage: codex tcp-tunnel <--proxy-url <PROXY_URL>|--proxy-origins-file <PROXY_ORIGINS_FILE>|--target <TARGET>|--listen-addr <LISTEN_ADDR>|--auth-token-stdin|--auth-token-updates-stdin|--connect-headers-stdin|--diagnostics-json>",
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

// TestStdioToUDSSeparatorLikeRust keeps `--` binding the socket path instead of
// ending argument parsing with an error.
func TestStdioToUDSSeparatorLikeRust(t *testing.T) {
	parsed, err := Parse([]string{"stdio-to-uds", "--", "x"})
	if err != nil {
		t.Fatalf("Parse(stdio-to-uds -- x) error = %v", err)
	}
	if parsed.StdioToUDS.SocketPath != "x" {
		t.Fatalf("SocketPath = %q, want x", parsed.StdioToUDS.SocketPath)
	}
}
