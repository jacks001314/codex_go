package cli

import (
	"strings"
	"testing"
)

// This table is generated from the 0.166.0 `codex.exe exec-server` output.
// clap validates a subcommand's `requires` against that subcommand's own
// matches, so arguments given before `forward` never satisfy the forward
// requirements; each entry pins the exact stderr text Rust prints.
var execServerForwardScopeDiagnostics = []struct {
	args []string
	want string
}{
	{args: []string{"forward", "--connect", "ws://127.0.0.1:9"}, want: "error: the following required arguments were not provided:\n  --remote <URL>\n  --environment-id <ID>\n\nUsage: codex exec-server forward --remote <URL> --environment-id <ID> --connect <URL>\n\nFor more information, try '--help'."},
	{args: []string{"--remote", "https://example.openai.com", "forward", "--connect", "ws://127.0.0.1:9"}, want: "error: the following required arguments were not provided:\n  --remote <URL>\n  --environment-id <ID>\n\nUsage: codex exec-server forward --remote <URL> --environment-id <ID> --connect <URL>\n\nFor more information, try '--help'."},
	{args: []string{"--remote", "https://example.openai.com", "--environment-id", "env-1", "forward", "--connect", "ws://127.0.0.1:9"}, want: "error: the following required arguments were not provided:\n  --remote <URL>\n  --environment-id <ID>\n\nUsage: codex exec-server forward --remote <URL> --environment-id <ID> --connect <URL>\n\nFor more information, try '--help'."},
	{args: []string{"--environment-id", "env-1", "forward", "--remote", "https://example.openai.com", "--connect", "ws://127.0.0.1:9"}, want: "error: the following required arguments were not provided:\n  --environment-id <ID>\n\nUsage: codex exec-server forward --remote <URL> --environment-id <ID> --connect <URL>\n\nFor more information, try '--help'."},
	{args: []string{"--remote-transport", "direct", "forward", "--connect", "ws://127.0.0.1:9"}, want: "error: the following required arguments were not provided:\n  --remote <URL>\n  --environment-id <ID>\n\nUsage: codex exec-server forward --remote <URL> --environment-id <ID> --connect <URL>\n\nFor more information, try '--help'."},
	{args: []string{"forward", "--connect", "ws://127.0.0.1:9", "--remote-transport", "direct"}, want: "error: the following required arguments were not provided:\n  --remote <URL>\n  --environment-id <ID>\n  --aws-sigv4\n\nUsage: codex exec-server forward --remote <URL> --environment-id <ID> --connect <URL> --aws-sigv4 --remote-transport <REMOTE_TRANSPORT>\n\nFor more information, try '--help'."},
	{args: []string{"--remote-transport", "noise"}, want: "error: the following required arguments were not provided:\n  --environment-id <ID>\n  --remote <URL>\n\nUsage: codex exec-server --environment-id <ID> --remote <URL> --remote-transport <REMOTE_TRANSPORT>\n\nFor more information, try '--help'."},
	{args: []string{"--listen", "stdio", "--remote", "https://example.openai.com", "--environment-id", "env-1", "forward", "--connect", "ws://127.0.0.1:9"}, want: "error: the following required arguments were not provided:\n  --remote <URL>\n  --environment-id <ID>\n\nUsage: codex exec-server forward --remote <URL> --environment-id <ID> --connect <URL>\n\nFor more information, try '--help'."},
	{args: []string{"--remote", "https://example.openai.com"}, want: "error: the following required arguments were not provided:\n  --environment-id <ID>\n\nUsage: codex exec-server --environment-id <ID> --remote <URL>\n\nFor more information, try '--help'."},
	{args: []string{"--remote", "https://example.openai.com", "--listen", "stdio"}, want: "error: the argument '--remote <URL>' cannot be used with '--listen <URL>'\n\nUsage: codex exec-server --environment-id <ID> --remote <URL>\n\nFor more information, try '--help'."},
	{args: []string{"--listen", "stdio", "--remote", "https://example.openai.com"}, want: "error: the argument '--listen <URL>' cannot be used with '--remote <URL>'\n\nUsage: codex exec-server --listen <URL>\n\nFor more information, try '--help'."},
	{args: []string{"forward"}, want: "error: the following required arguments were not provided:\n  --remote <URL>\n  --environment-id <ID>\n  --connect <URL>\n\nUsage: codex exec-server forward --remote <URL> --environment-id <ID> --connect <URL>\n\nFor more information, try '--help'."},
	{args: []string{"forward", "--listen", "stdio", "--connect", "ws://127.0.0.1:9"}, want: "error: unexpected argument '--listen' found\n\nUsage: codex exec-server forward [OPTIONS] --remote <URL> --environment-id <ID> --connect <URL>\n\nFor more information, try '--help'."},
	{args: []string{"--use-agent-identity-auth"}, want: "error: the following required arguments were not provided:\n  --environment-id <ID>\n  --remote <URL>\n\nUsage: codex exec-server --environment-id <ID> --remote <URL> --use-agent-identity-auth\n\nFor more information, try '--help'."},
	{args: []string{"forward", "--aws-profile", "p", "--connect", "ws://127.0.0.1:9"}, want: "error: the following required arguments were not provided:\n  --remote <URL>\n  --environment-id <ID>\n  --aws-sigv4\n\nUsage: codex exec-server forward --remote <URL> --environment-id <ID> --connect <URL> --aws-sigv4 --aws-profile <PROFILE>\n\nFor more information, try '--help'."},
	{args: []string{"--remote", "https://example.openai.com", "--environment-id", "env-1", "--executor-credential-stdin", "forward", "--connect", "ws://127.0.0.1:9"}, want: "error: the following required arguments were not provided:\n  --remote <URL>\n  --environment-id <ID>\n\nUsage: codex exec-server forward --remote <URL> --environment-id <ID> --connect <URL>\n\nFor more information, try '--help'."},
	{args: []string{"forward", "--concurrent-requests", "2", "--connect", "ws://127.0.0.1:9"}, want: "error: unexpected argument '--concurrent-requests' found\n\n  tip: a similar argument exists: '--connect'\n\nUsage: codex exec-server forward --remote <URL> --environment-id <ID> --connect <URL>\n\nFor more information, try '--help'."},
	{args: []string{"--connect", "ws://127.0.0.1:9"}, want: "error: unexpected argument '--connect' found\n\n  tip: a similar argument exists: '--concurrent-requests'\n\nUsage: codex exec-server --concurrent-requests <COUNT>\n\nFor more information, try '--help'."},
	{args: []string{"--remote", "https://a.example.com", "forward", "--remote", "https://b.example.com", "--environment-id", "e", "--connect", "ws://127.0.0.1:9"}, want: "error: the following required arguments were not provided:\n  --environment-id <ID>\n\nUsage: codex exec-server --environment-id <ID> --remote <URL>\n\nFor more information, try '--help'."},
	{args: []string{"--remote-transport", "direct", "--aws-sigv4"}, want: "error: the following required arguments were not provided:\n  --environment-id <ID>\n  --remote <URL>\n\nUsage: codex exec-server --environment-id <ID> --remote <URL> --aws-sigv4 --remote-transport <REMOTE_TRANSPORT>\n\nFor more information, try '--help'."},
	{args: []string{"--remote", "https://example.openai.com", "--aws-profile", "development"}, want: "error: the following required arguments were not provided:\n  --environment-id <ID>\n  --aws-sigv4\n\nUsage: codex exec-server --environment-id <ID> --remote <URL> --aws-sigv4 --aws-profile <PROFILE>\n\nFor more information, try '--help'."},
	{args: []string{"--exit-on-stdin-close", "--remote", "https://example.openai.com"}, want: "error: the following required arguments were not provided:\n  --environment-id <ID>\n\nUsage: codex exec-server --environment-id <ID> --remote <URL> --exit-on-stdin-close\n\nFor more information, try '--help'."},
	{args: []string{"--remote-transport", "direct", "--remote-transport", "noise"}, want: "error: the argument '--remote-transport <REMOTE_TRANSPORT>' cannot be used multiple times\n\nUsage: codex exec-server [OPTIONS] [COMMAND]\n\nFor more information, try '--help'."},
	{args: []string{"forward", "--connect", "ws://127.0.0.1:9", "--connect", "ws://127.0.0.1:9"}, want: "error: the argument '--connect <URL>' cannot be used multiple times\n\nUsage: codex exec-server forward [OPTIONS] --remote <URL> --environment-id <ID> --connect <URL>\n\nFor more information, try '--help'."},
	{args: []string{"--aws-profile", "development"}, want: "error: the following required arguments were not provided:\n  --remote <URL>\n  --environment-id <ID>\n  --aws-sigv4\n\nUsage: codex exec-server --remote <URL> --environment-id <ID> --aws-sigv4 --aws-profile <PROFILE>\n\nFor more information, try '--help'."},
	{args: []string{"forward", "--remote", "https://example.openai.com", "--environment-id", "env-1", "--connect", "ws://127.0.0.1:9", "--remote-transport", "noise", "--aws-profile", "p"}, want: "error: the following required arguments were not provided:\n  --aws-sigv4\n\nUsage: codex exec-server forward --remote <URL> --environment-id <ID> --connect <URL> --aws-sigv4 --remote-transport <REMOTE_TRANSPORT> --aws-profile <PROFILE>\n\nFor more information, try '--help'."},
	{args: []string{"forward", "--use-agent-identity-auth", "--aws-sigv4", "--connect", "ws://127.0.0.1:9"}, want: "error: the argument '--use-agent-identity-auth' cannot be used with '--aws-sigv4'\n\nUsage: codex exec-server forward --remote <URL> --environment-id <ID> --connect <URL> --use-agent-identity-auth\n\nFor more information, try '--help'."},
	{args: []string{"forward", "--remote", "https://example.openai.com", "--environment-id", "env-1", "--connect", "ws://127.0.0.1:9", "--use-agent-identity-auth", "--aws-sigv4"}, want: "error: the argument '--use-agent-identity-auth' cannot be used with '--aws-sigv4'\n\nUsage: codex exec-server forward --remote <URL> --environment-id <ID> --connect <URL> --use-agent-identity-auth\n\nFor more information, try '--help'."},
	{args: []string{"--strict-config", "--remote", "https://example.openai.com"}, want: "error: the following required arguments were not provided:\n  --environment-id <ID>\n\nUsage: codex exec-server --environment-id <ID> --strict-config --remote <URL>\n\nFor more information, try '--help'."},
	{args: []string{"--concurrent-requests", "2", "--remote", "https://example.openai.com"}, want: "error: the following required arguments were not provided:\n  --environment-id <ID>\n\nUsage: codex exec-server --environment-id <ID> --concurrent-requests <COUNT> --remote <URL>\n\nFor more information, try '--help'."},
	{args: []string{"--exit-on-stdin-close", "forward", "--remote", "https://example.openai.com", "--environment-id", "env-1", "--connect", "ws://127.0.0.1:9"}, want: "error: the following required arguments were not provided:\n  --environment-id <ID>\n  --remote <URL>\n\nUsage: codex exec-server --environment-id <ID> --remote <URL> --exit-on-stdin-close\n\nFor more information, try '--help'."},
}

// execServerForwardScopeAccepted lists the combinations Rust lets through
// clap; the remote registration then fails later in the run for lack of
// credentials, so parsing must stay clean.
var execServerForwardScopeAccepted = [][]string{
	{"forward", "--remote", "https://example.openai.com", "--environment-id", "env-1", "--connect", "ws://127.0.0.1:9"},
	{"forward", "--exit-on-stdin-close", "--remote", "https://example.openai.com", "--environment-id", "env-1", "--connect", "ws://127.0.0.1:9"},
	{"--listen", "stdio", "forward", "--remote", "https://example.openai.com", "--environment-id", "env-1", "--connect", "ws://127.0.0.1:9"},
	{"--strict-config", "forward", "--remote", "https://example.openai.com", "--environment-id", "env-1", "--connect", "ws://127.0.0.1:9"},
	{"forward", "--strict-config", "--remote", "https://example.openai.com", "--environment-id", "env-1", "--connect", "ws://127.0.0.1:9"},
	{"--remote", "https://example.openai.com", "--environment-id", "env-1", "--remote-transport", "noise"},
	{"--name", "registry", "--remote", "https://example.openai.com", "--environment-id", "env-1"},
}

func TestExecServerForwardScopeDiagnosticsLikeRust(t *testing.T) {
	for _, test := range execServerForwardScopeDiagnostics {
		argv := append([]string{"exec-server"}, test.args...)
		_, err := Parse(argv)
		usageErr, ok := err.(*UsageError)
		if !ok {
			t.Fatalf("Parse(%v) error = %v, want a clap diagnostic", argv, err)
		}
		if got := strings.TrimRight(clapErrorText(usageErr.Message, usageErr.Usage), "\n"); got != test.want {
			t.Fatalf("Parse(%v) diagnostic =\n%s\nwant:\n%s", argv, got, test.want)
		}
	}
}

func TestExecServerForwardScopeAcceptedLikeRust(t *testing.T) {
	for _, args := range execServerForwardScopeAccepted {
		argv := append([]string{"exec-server"}, args...)
		if _, err := Parse(argv); err != nil {
			t.Fatalf("Parse(%v) error = %v, want a clean parse", argv, err)
		}
	}
}
