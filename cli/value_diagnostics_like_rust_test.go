package cli

import (
	"strings"
	"testing"
)

// TestValueDiagnosticOrderLikeRust pins clap's space-form value handling: a
// value diagnostic fires once the following token is one clap recognises, so
// an undeclared flag right after the value wins, while any earlier value
// error preempts a later undeclared flag. Every want is a 0.166 capture.
func TestValueDiagnosticOrderLikeRust(t *testing.T) {
	cases := []struct {
		args  string
		msg   string
		usage string
	}{
		{"exec --color bogus --json --bogus", "invalid value 'bogus' for '--color <COLOR>'\n  [possible values: always, never, auto]", ""},
		{"exec --color bogus --bogus", "unexpected argument '--bogus' found\n\n  tip: to pass '--bogus' as a value, use '-- --bogus'", "Usage: codex exec [OPTIONS] [PROMPT]\n       codex exec [OPTIONS] <COMMAND> [ARGS]"},
		{"exec --color bogus prompt --bogus", "invalid value 'bogus' for '--color <COLOR>'\n  [possible values: always, never, auto]", ""},
		{"exec --bogus --color bogus", "unexpected argument '--bogus' found\n\n  tip: to pass '--bogus' as a value, use '-- --bogus'", "Usage: codex exec [OPTIONS] [PROMPT]\n       codex exec [OPTIONS] <COMMAND> [ARGS]"},
		{"exec --color=bogus --bogus", "invalid value 'bogus' for '--color <COLOR>'\n  [possible values: always, never, auto]", ""},
		{"exec --color bogus --json", "invalid value 'bogus' for '--color <COLOR>'\n  [possible values: always, never, auto]", ""},
		{"exec --sandbox bogus --json --bogus", "invalid value 'bogus' for '--sandbox <SANDBOX_MODE>'\n  [possible values: read-only, workspace-write, danger-full-access]", ""},
		{"app-server --listen a --strict-config --bogus", "invalid value 'a' for '--listen <URL>': unsupported --listen URL `a`; expected `stdio://`, `unix://`, `unix://PATH`, `ws://IP:PORT`, or `off`", ""},
		{"app-server --listen a --bogus", "unexpected argument '--bogus' found", "Usage: codex app-server --listen <URL>"},
		{"mcp list --json x", "unexpected argument 'x' found", "Usage: codex mcp list [OPTIONS]"},
		{"agents --config c x", "unexpected argument 'x' found", "Usage: codex agents [OPTIONS]"},
		{"agents --remote ws://x x", "unexpected argument 'x' found", "Usage: codex agents [OPTIONS]"},
		{"review --uncommitted x y", "unexpected argument 'y' found", "Usage: codex review [OPTIONS] [PROMPT]"},
		{"logout --config c x", "unexpected argument 'x' found", "Usage: codex logout [OPTIONS]"},
		{"update --config c x", "unexpected argument 'x' found", "Usage: codex update [OPTIONS]"},
		{"agents --no-daemon --bogus", "unexpected argument '--bogus' found", "Usage: codex agents [OPTIONS]"},
		{"agents --no-alt-screen --bogus", "unexpected argument '--bogus' found", "Usage: codex agents --no-alt-screen"},
		{"mcp list --json --bogus", "unexpected argument '--bogus' found", "Usage: codex mcp list --json"},
		{"login --device-code x", "unexpected argument '--device-code' found\n\n  tip: a similar argument exists: '--device-auth'", "Usage: codex login --device-auth"},
	}
	for _, tc := range cases {
		_, err := Parse(strings.Fields(tc.args))
		usageErr, ok := err.(*UsageError)
		if !ok {
			t.Fatalf("%s: error = %v, want clap diagnostic", tc.args, err)
		}
		if usageErr.Message != tc.msg {
			t.Fatalf("%s: message = %q, want %q", tc.args, usageErr.Message, tc.msg)
		}
		if usageErr.Usage != tc.usage {
			t.Fatalf("%s: usage = %q, want %q", tc.args, usageErr.Usage, tc.usage)
		}
	}
}

// TestStartupClientInfoSerdeDiagnosticsLikeRust covers serde_json's
// ClientInfo deserialiser: field presence, field types, the seq form and the
// syntax diagnostics behind --startup-client-info.
func TestStartupClientInfoSerdeDiagnosticsLikeRust(t *testing.T) {
	cases := []struct {
		value  string
		detail string
	}{
		{"{}", "missing field `name` at line 1 column 2"},
		{"{\"name\":\"x\"}", "missing field `version` at line 1 column 12"},
		{"{\"name\":1}", "invalid type: integer `1`, expected a string at line 1 column 9"},
		{"{\"version\":1,\"name\":\"x\"}", "invalid type: integer `1`, expected a string at line 1 column 12"},
		{"{\"name\":null}", "invalid type: null, expected a string at line 1 column 12"},
		{"{\"name\":\"x\",\"version\":null}", "invalid type: null, expected a string at line 1 column 26"},
		{"1.5", "invalid type: number, expected struct ClientInfo at line 1 column 3"},
		{"1", "invalid type: integer `1`, expected struct ClientInfo at line 1 column 1"},
		{"true", "invalid type: boolean `true`, expected struct ClientInfo at line 1 column 4"},
		{"null", "invalid type: null, expected struct ClientInfo at line 1 column 4"},
		{"\"s\"", "invalid type: string \"s\", expected struct ClientInfo at line 1 column 3"},
		{"[1,2]", "invalid type: integer `1`, expected a string at line 1 column 2"},
		{"[\"a\",\"b\"]", "invalid length 2, expected struct ClientInfo with 3 elements at line 1 column 9"},
		{"[]", "invalid length 0, expected struct ClientInfo with 3 elements at line 1 column 2"},
		{"{\"title\":\"t\"}", "missing field `name` at line 1 column 13"},
		{"{\"name\":\"x\",\"version\":\"1\",\"title\":1}", "invalid type: integer `1`, expected a string at line 1 column 35"},
		{"{\"Name\":\"x\"}", "missing field `name` at line 1 column 12"},
		{"{\"name\":\"x\",}", "trailing comma at line 1 column 13"},
		{"{\"name\":\"x\" \"version\":\"1\"}", "expected `,` or `}` at line 1 column 13"},
		{"{\"name\":}", "expected value at line 1 column 9"},
		{"{\"name\"}", "expected `:` at line 1 column 8"},
		{"{\"name\":", "EOF while parsing a value at line 1 column 8"},
		{"[", "EOF while parsing a list at line 1 column 1"},
		{"{", "EOF while parsing an object at line 1 column 1"},
		{"  {}  ", "missing field `name` at line 1 column 4"},
		{"{\"name\":\"x\",\"name\":\"y\"}", "duplicate field `name` at line 1 column 18"},
		{"tru", "EOF while parsing a value at line 1 column 3"},
		{"trux", "expected ident at line 1 column 4"},
		{"nulll", "invalid type: null, expected struct ClientInfo at line 1 column 4"},
		{"[\"a\",\"b\",null]", "invalid type: null, expected a string at line 1 column 13"},
		{"[\"a\",\"b\",\"c\",\"d\"]", "trailing characters at line 1 column 14"},
		{"{\"name\":\"a\\u0041\"}", "missing field `version` at line 1 column 18"},
		{"[1,]", "invalid type: integer `1`, expected a string at line 1 column 2"},
		{"{\"name\":1,}", "invalid type: integer `1`, expected a string at line 1 column 9"},
		{"[\"a\",1]", "invalid type: integer `1`, expected a string at line 1 column 6"},
		{"{\"name\":[1]}", "invalid type: sequence, expected a string at line 1 column 8"},
		{"{\"name\":{\"a\":1}}", "invalid type: map, expected a string at line 1 column 8"},
		{"{\"name\":true}", "invalid type: boolean `true`, expected a string at line 1 column 12"},
		{"{\"name\":\"x\",\"version\":\"1\",}", "trailing comma at line 1 column 27"},
	}
	for _, tc := range cases {
		_, err := Parse([]string{"app-server", "--print-startup-config", "--startup-client-info", tc.value})
		usageErr, ok := err.(*UsageError)
		if !ok {
			t.Fatalf("%s: error = %v, want clap diagnostic", tc.value, err)
		}
		want := "invalid value '" + tc.value + "' for '--startup-client-info <STARTUP_CLIENT_INFO>': " + tc.detail
		if usageErr.Message != want {
			t.Fatalf("%s: message = %q, want %q", tc.value, usageErr.Message, want)
		}
	}
}

// TestStartupClientInfoAcceptsRustShapesLikeRust keeps the values 0.166 accepts
// parseable, including the optional title field and the three-element seq form.
func TestStartupClientInfoAcceptsRustShapesLikeRust(t *testing.T) {
	for _, value := range []string{
		`{"name":"desktop","version":"1"}`,
		`{"name":"desktop","version":"1","title":null}`,
		`{"name":"desktop","version":"1","extra":{"a":1}}`,
		`["desktop","1","title"]`,
		`  {"name":"desktop","version":"1"}  `,
	} {
		if _, err := Parse([]string{"app-server", "--print-startup-config", "--startup-client-info", value}); err != nil {
			t.Fatalf("%s: error = %v, want accepted", value, err)
		}
	}
}
