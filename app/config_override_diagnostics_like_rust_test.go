package app

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

// TestConfigOverrideDiagnosticsMatchRust pins the per-command `-c`/`--config`
// diagnostics measured against the Rust 0.166 binary: the message text, the
// context each command adds, and whether the text skips the runtime `Error: `
// head because the command renders its own context.
func TestConfigOverrideDiagnosticsMatchRust(t *testing.T) {
	const missingValue = "Invalid override (missing '='): "
	const execContext = "Error parsing -c overrides: "
	cases := []struct {
		name string
		args []string
		want string
		// preRendered marks diagnostics Rust writes without the runtime
		// `Error: ` head because the command renders its own context.
		preRendered bool
	}{
		{name: "exec", args: []string{"exec", "--config="}, want: execContext + missingValue, preRendered: true},
		{name: "exec resume", args: []string{"exec", "resume", "--config="}, want: execContext + missingValue, preRendered: true},
		{name: "exec review", args: []string{"exec", "review", "--config="}, want: execContext + missingValue, preRendered: true},
		{name: "review", args: []string{"review", "--config="}, want: execContext + missingValue, preRendered: true},
		{name: "login", args: []string{"login", "--config="}, want: execContext + missingValue, preRendered: true},
		{name: "login status", args: []string{"login", "status", "--config="}, want: execContext + missingValue, preRendered: true},
		{name: "logout", args: []string{"logout", "--config="}, want: execContext + missingValue, preRendered: true},
		{name: "app-server", args: []string{"app-server", "--config="}, want: "error parsing -c overrides: " + missingValue},
		{name: "app-server daemon restart", args: []string{"app-server", "daemon", "restart", "--config="}, want: missingValue},
		{name: "app-server daemon update", args: []string{"app-server", "daemon", "update", "--config="}, want: missingValue},
		{name: "features list", args: []string{"features", "list", "--config="}, want: missingValue},
		{name: "features list inline value", args: []string{"features", "list", "--config=x"}, want: "Invalid override (missing '='): x"},
		{name: "plugin list", args: []string{"plugin", "list", "--config="}, want: missingValue},
		{name: "plugin marketplace list", args: []string{"plugin", "marketplace", "list", "--config="}, want: missingValue},
		{name: "plugin marketplace upgrade", args: []string{"plugin", "marketplace", "upgrade", "--config="}, want: missingValue},
		{name: "debug models", args: []string{"debug", "models", "--config="}, want: missingValue},
		{name: "debug prompt-input", args: []string{"debug", "prompt-input", "--config="}, want: missingValue},
		{name: "migrate-rollouts", args: []string{"migrate-rollouts", "--config="}, want: missingValue},
		{name: "sandbox", args: []string{"sandbox", "--config="}, want: missingValue},
		{name: "sandbox with command", args: []string{"sandbox", "--config=", "--", "echo"}, want: missingValue},
		{name: "empty key", args: []string{"features", "list", "-c", "=x"}, want: "Empty key in override: =x"},
		{name: "empty key keeps raw value", args: []string{"exec", "-c", "  =x"}, want: execContext + "Empty key in override:   =x", preRendered: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearAuthEnvApp(t)
			t.Setenv("CODEX_HOME", t.TempDir())
			var stdout, stderr bytes.Buffer
			err := Run(context.Background(), tc.args, strings.NewReader(""), &stdout, &stderr)
			if err == nil {
				t.Fatalf("Run(%v) = nil error, want diagnostic", tc.args)
			}
			if got := err.Error(); got != tc.want {
				t.Fatalf("Run(%v) error = %q, want %q", tc.args, got, tc.want)
			}
			var exitErr *ExitError
			preRendered := errors.As(err, &exitErr) && exitErr.Message != ""
			if preRendered != tc.preRendered {
				t.Fatalf("Run(%v) pre-rendered = %t, want %t", tc.args, preRendered, tc.preRendered)
			}
		})
	}
}

// TestCloudSignInHintPrecedesOverrideDiagnostics pins Rust's cloud ordering:
// the credential check runs before `-c`/`--config` is parsed, so a signed-out
// host reports the sign-in hint instead of an override error.
func TestCloudSignInHintPrecedesOverrideDiagnostics(t *testing.T) {
	clearAuthEnvApp(t)
	t.Setenv("CODEX_HOME", t.TempDir())

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"cloud", "--config="}, strings.NewReader(""), &stdout, &stderr)
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 1 || !exitErr.Silent {
		t.Fatalf("cloud error = %+v, want silent exit 1", err)
	}
	if got := strings.TrimSpace(stderr.String()); got != cloudSignInRequiredMessage {
		t.Fatalf("cloud stderr = %q, want %q", got, cloudSignInRequiredMessage)
	}
}
