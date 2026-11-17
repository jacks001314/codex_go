package tcptunnel

import "testing"

// TestTcpTunnelClapDiagnosticsLikeRust pins the clap blocks captured from the
// Rust `codex-tcp-tunnel` Args definition (required_unless_present, requires and
// conflicts_with): same message, same smart-usage order, same exit block.
func TestTcpTunnelClapDiagnosticsLikeRust(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		message string
		usage   string
	}{
		{
			name:    "missing environment id",
			args:    nil,
			message: "the following required arguments were not provided:\n  --environment-id <ENVIRONMENT_ID>",
			usage:   "Usage: codex tcp-tunnel --environment-id <ENVIRONMENT_ID>",
		},
		{
			name:    "port requires environment id",
			args:    []string{"--port", "22"},
			message: "the following required arguments were not provided:\n  --environment-id <ENVIRONMENT_ID>",
			usage:   "Usage: codex tcp-tunnel --environment-id <ENVIRONMENT_ID> --port <PORT>",
		},
		{
			name:    "target requires token file",
			args:    []string{"--target", "10.0.0.1:22"},
			message: "the following required arguments were not provided:\n  --token-file <TOKEN_FILE>",
			usage:   "Usage: codex tcp-tunnel --token-file <TOKEN_FILE> --target <TARGET>",
		},
		{
			name:    "token file alone still lacks a route",
			args:    []string{"--token-file", "f"},
			message: "the following required arguments were not provided:\n  --environment-id <ENVIRONMENT_ID>",
			usage:   "Usage: codex tcp-tunnel --token-file <TOKEN_FILE> --environment-id <ENVIRONMENT_ID>",
		},
		{
			name:    "both legacy requirements",
			args:    []string{"--port", "22", "--target", "1.2.3.4:22"},
			message: "the following required arguments were not provided:\n  --environment-id <ENVIRONMENT_ID>\n  --token-file <TOKEN_FILE>",
			usage:   "Usage: codex tcp-tunnel --environment-id <ENVIRONMENT_ID> --token-file <TOKEN_FILE> --port <PORT> --target <TARGET>",
		},
		{
			name:    "environment id conflicts with target",
			args:    []string{"--environment-id", "env", "--target", "1.2.3.4:22", "--token-file", "f"},
			message: "the argument '--environment-id <ENVIRONMENT_ID>' cannot be used with '--target <TARGET>'",
			usage:   "Usage: codex tcp-tunnel --environment-id <ENVIRONMENT_ID> --token-file <TOKEN_FILE>",
		},
		{
			name:    "conflict reports the earlier argument",
			args:    []string{"--target", "1.2.3.4:22", "--token-file", "f", "--environment-id", "env"},
			message: "the argument '--target <TARGET>' cannot be used with '--environment-id <ENVIRONMENT_ID>'",
			usage:   "Usage: codex tcp-tunnel --target <TARGET> --token-file <TOKEN_FILE>",
		},
		{
			name:    "invalid socket address",
			args:    []string{"--target", "bad"},
			message: "invalid value 'bad' for '--target <TARGET>': invalid socket address syntax",
		},
		{
			name:    "unknown argument",
			args:    []string{"--bogus"},
			message: "unexpected argument '--bogus' found",
			usage:   "Usage: codex tcp-tunnel [OPTIONS]",
		},
		{
			name:    "missing value",
			args:    []string{"--proxy-url"},
			message: "a value is required for '--proxy-url <PROXY_URL>' but none was supplied",
		},
		{
			name:    "zero port",
			args:    []string{"--environment-id", "env", "--port", "0"},
			message: "invalid value '0' for '--port <PORT>': number would be zero for non-zero type",
		},
		{
			name:    "duplicate flag",
			args:    []string{"--environment-id", "env", "--port", "22", "--port", "23"},
			message: "the argument '--port <PORT>' cannot be used multiple times",
			usage:   "Usage: codex tcp-tunnel [OPTIONS]",
		},
		{
			name:    "removed listener flag",
			args:    []string{"--auth-token-stdin"},
			message: "unexpected argument '--auth-token-stdin' found",
			usage:   "Usage: codex tcp-tunnel [OPTIONS]",
		},
		{
			name:    "known flag cannot be a value",
			args:    []string{"--port", "--target", "1.2.3.4:22"},
			message: "a value is required for '--port <PORT>' but none was supplied",
		},
		{
			name:    "unknown flag cannot be a value",
			args:    []string{"--environment-id", "-x"},
			message: "unexpected argument '-x' found",
			usage:   "Usage: codex tcp-tunnel [OPTIONS]",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := ParseArgs(testCase.args)
			if err == nil {
				t.Fatalf("ParseArgs(%v) returned no error", testCase.args)
			}
			diagnostic, ok := err.(*Diagnostic)
			if !ok {
				t.Fatalf("ParseArgs(%v) error = %T %v, want *Diagnostic", testCase.args, err, err)
			}
			if diagnostic.Message != testCase.message {
				t.Fatalf("message = %q, want %q", diagnostic.Message, testCase.message)
			}
			if diagnostic.Usage != testCase.usage {
				t.Fatalf("usage = %q, want %q", diagnostic.Usage, testCase.usage)
			}
		})
	}
}

// TestTcpTunnelArgsParseLikeRust covers the accepted routes, including the
// port-with-target combination clap allows because the implied environment id
// conflicts with the supplied target.
func TestTcpTunnelArgsParseLikeRust(t *testing.T) {
	oauth, err := ParseArgs([]string{"--environment-id", "ccarenv_test", "--port", "2222"})
	if err != nil {
		t.Fatalf("ParseArgs(oauth) error = %v", err)
	}
	if oauth.ProxyURL != PublicGateway || oauth.EnvironmentID != "ccarenv_test" || oauth.Port != 2222 {
		t.Fatalf("oauth args = %#v", oauth)
	}
	if oauth.UsesTokenFile() {
		t.Fatalf("oauth args unexpectedly use a token file")
	}

	legacy, err := ParseArgs([]string{"--token-file", "/private/token", "--target", "10.0.0.1:22"})
	if err != nil {
		t.Fatalf("ParseArgs(legacy) error = %v", err)
	}
	if legacy.Target != "10.0.0.1:22" || legacy.TokenFile != "/private/token" || !legacy.UsesTokenFile() {
		t.Fatalf("legacy args = %#v", legacy)
	}

	// clap drops the environment-id requirement of --port when the supplied
	// target conflicts with it, so this parses.
	mixed, err := ParseArgs([]string{"--port", "22", "--target", "1.2.3.4:22", "--token-file", "f"})
	if err != nil {
		t.Fatalf("ParseArgs(mixed) error = %v", err)
	}
	if mixed.Port != 22 || mixed.Target != "1.2.3.4:22" {
		t.Fatalf("mixed args = %#v", mixed)
	}

	attached, err := ParseArgs([]string{"--environment-id=env", "--proxy-url=https://codex-portals.api.openai.com"})
	if err != nil {
		t.Fatalf("ParseArgs(attached) error = %v", err)
	}
	if attached.EnvironmentID != "env" {
		t.Fatalf("attached environment id = %q", attached.EnvironmentID)
	}
}
