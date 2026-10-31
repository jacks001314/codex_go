package cli

import (
	"errors"
	"strings"
	"testing"
)

// TestMCPEmptyNameIsPresentLikeClap mirrors clap's String positional: an
// explicitly empty NAME is a present value, so the parser must not report the
// required-argument block and the runtime answers instead.
func TestMCPEmptyNameIsPresentLikeClap(t *testing.T) {
	for _, action := range []string{"get", "remove", "login", "logout"} {
		parsed, err := Parse([]string{"mcp", action, ""})
		if err != nil {
			t.Fatalf("Parse([mcp %s \"\"]) error = %v", action, err)
		}
		if parsed.MCP.Action != action {
			t.Fatalf("MCP.Action = %q, want %q", parsed.MCP.Action, action)
		}
		if parsed.MCP.Name != "" {
			t.Fatalf("MCP.Name = %q, want empty", parsed.MCP.Name)
		}
	}
}

// TestMCPExtraPositionalIsUnexpectedArgumentLikeClap: clap rejects a second
// positional NAME with the subcommand usage block.
func TestMCPExtraPositionalIsUnexpectedArgumentLikeClap(t *testing.T) {
	for _, action := range []string{"get", "remove", "login", "logout"} {
		_, err := Parse([]string{"mcp", action, "x", "y"})
		var usage *UsageError
		if !errors.As(err, &usage) {
			t.Fatalf("Parse([mcp %s x y]) error = %T %v, want UsageError", action, err, err)
		}
		if usage.Message != "unexpected argument 'y' found" {
			t.Fatalf("message = %q", usage.Message)
		}
		if want := "Usage: codex mcp " + action + " [OPTIONS] <NAME>"; usage.Usage != want {
			t.Fatalf("usage = %q, want %q", usage.Usage, want)
		}
	}
}

// TestMCPAddEmptyURLSatisfiesTransportLikeClap: `--url=` is a present value, so
// only the remaining requirements are reported.
func TestMCPAddEmptyURLSatisfiesTransportLikeClap(t *testing.T) {
	parsed, err := Parse([]string{"mcp", "add", "x", "--url="})
	if err != nil {
		t.Fatalf("Parse(mcp add x --url=) error = %v", err)
	}
	if !parsed.MCP.URLSet || parsed.MCP.URL != "" {
		t.Fatalf("MCP URL = %q set=%t", parsed.MCP.URL, parsed.MCP.URLSet)
	}
	if _, err := Parse([]string{"mcp", "add", "--url="}); err == nil {
		t.Fatal("Parse(mcp add --url=) succeeded, want missing <NAME>")
	}
}

// TestMCPAddOAuthRequiresLikeClap pins the merged `requires` report and the
// root-scoped singular form clap uses when --url is the only missing argument.
func TestMCPAddOAuthRequiresLikeClap(t *testing.T) {
	for _, test := range []struct {
		args    []string
		message string
		usage   string
	}{
		{
			args:    []string{"mcp", "add", "fs", "--oauth-client-secret", "s", "--", "mcp-fs"},
			message: "the following required arguments were not provided:\n  --url <URL>\n  --oauth-client-id <CLIENT_ID>",
			usage:   "Usage: codex mcp add [OPTIONS] <NAME> (--url <URL> | -- <COMMAND>...)",
		},
		{
			args:    []string{"mcp", "add", "fs", "--oauth-client-secret", "s"},
			message: "the following required arguments were not provided:\n  --oauth-client-id <CLIENT_ID>\n  <COMMAND|--url <URL>>",
			usage:   "Usage: codex mcp add [OPTIONS] <NAME> (--url <URL> | -- <COMMAND>...)",
		},
		{
			args:    []string{"mcp", "add", "fs", "--oauth-client-id", "c", "--", "mcp-fs"},
			message: "the following required argument was not provided: url",
			usage:   rootUsageText,
		},
	} {
		_, err := Parse(test.args)
		var usage *UsageError
		if !errors.As(err, &usage) {
			t.Fatalf("Parse(%v) error = %T %v, want UsageError", test.args, err, err)
		}
		if usage.Message != test.message {
			t.Fatalf("Parse(%v) message = %q, want %q", test.args, usage.Message, test.message)
		}
		if usage.Usage != test.usage {
			t.Fatalf("Parse(%v) usage = %q, want %q", test.args, usage.Usage, test.usage)
		}
	}
}

// TestQueueEmptyMessageIsClapEmptyValueLikeRust: --message uses
// NonEmptyStringValueParser, so an empty value is clap's empty-value diagnostic
// while whitespace is a normal message.
func TestQueueEmptyMessageIsClapEmptyValueLikeRust(t *testing.T) {
	for _, args := range [][]string{
		{"queue", "--thread", "t", "--message", ""},
		{"queue", "--thread", "t", "--message="},
	} {
		_, err := Parse(args)
		var usage *UsageError
		if !errors.As(err, &usage) {
			t.Fatalf("Parse(%v) error = %T %v, want UsageError", args, err, err)
		}
		if !strings.Contains(usage.Message, "a value is required for '--message <TEXT>'") {
			t.Fatalf("Parse(%v) message = %q", args, usage.Message)
		}
	}
}
