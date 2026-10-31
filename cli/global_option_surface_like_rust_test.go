package cli

import "testing"

// TestGlobalOptionSurfaceLikeRust pins the diagnostics clap raises for the
// options the whole CLI shares (`-c/--config`) plus the per-command value
// options that only exist below the root. clap defers a bad value until it has
// finished scanning argv, so an undeclared flag later on still wins, and the
// missing-value wording carries the flag's own placeholder.
func TestGlobalOptionSurfaceLikeRust(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		message string
		usage   string
	}{
		{
			name:    "exec root keeps the static page",
			args:    []string{"exec", "--sandbox", "x", "--bogus"},
			message: "unexpected argument '--bogus' found\n\n  tip: to pass '--bogus' as a value, use '-- --bogus'",
			usage:   "Usage: codex exec [OPTIONS] [PROMPT]\n       codex exec [OPTIONS] <COMMAND> [ARGS]",
		},
		{
			name:    "exec review json",
			args:    []string{"exec", "review", "--json", "--bogus"},
			message: "unexpected argument '--bogus' found\n\n  tip: to pass '--bogus' as a value, use '-- --bogus'",
			usage:   "Usage: codex exec review --json [PROMPT]",
		},
		{
			name:    "exec fork single-value image",
			args:    []string{"exec", "fork", "--image", "x", "--bogus"},
			message: "unexpected argument '--bogus' found\n\n  tip: to pass '--bogus' as a value, use '-- --bogus'",
			usage:   "Usage: codex exec fork --image <FILE> <SESSION_ID> [PROMPT]",
		},
		{
			name:    "sandbox profile",
			args:    []string{"sandbox", "--profile", "x", "--bogus"},
			message: "unexpected argument '--bogus' found\n\n  tip: to pass '--bogus' as a value, use '-- --bogus'",
			usage:   "Usage: codex sandbox --profile <CONFIG_PROFILE> [COMMAND]...",
		},
		{
			name:    "migrate rollouts thread",
			args:    []string{"migrate-rollouts", "--thread", "x", "--bogus"},
			message: "unexpected argument '--bogus' found",
			usage:   "Usage: codex migrate-rollouts --thread <THREAD_ID>",
		},
		{
			name:    "migrate rollouts thread placeholder",
			args:    []string{"migrate-rollouts", "--thread", "--apply"},
			message: "a value is required for '--thread <THREAD_ID>' but none was supplied",
		},
		{
			name:    "migrate rollouts rate placeholder",
			args:    []string{"migrate-rollouts", "--max-mib-per-second", "--apply"},
			message: "a value is required for '--max-mib-per-second <MIB>' but none was supplied",
		},
		{
			name:    "cloud list limit integer",
			args:    []string{"cloud", "list", "--limit", "x"},
			message: "invalid value 'x' for '--limit <N>': limit must be an integer between 1 and 20",
		},
		{
			name:    "cloud exec attempts integer",
			args:    []string{"cloud", "exec", "--attempts", "x", "--env", "e", "q"},
			message: "invalid value 'x' for '--attempts <ATTEMPTS>': attempts must be an integer between 1 and 4",
		},
		{
			name:    "app-server startup client info",
			args:    []string{"app-server", "--startup-client-info", "x", "--bogus"},
			message: "unexpected argument '--bogus' found",
			usage:   "Usage: codex app-server --startup-client-info <STARTUP_CLIENT_INFO>",
		},
		{
			name:    "app-server code mode host is a raw value error",
			args:    []string{"app-server", "--code-mode-host", "x"},
			message: "invalid code-mode host URL: relative URL without a base",
		},
		{
			name:    "exec-server concurrent requests",
			args:    []string{"exec-server", "--concurrent-requests", "x", "--bogus"},
			message: "unexpected argument '--bogus' found",
			usage:   "Usage: codex exec-server --concurrent-requests <COUNT>",
		},
		{
			name:    "exec-server pid namespace placeholder",
			args:    []string{"exec-server", "--linux-sandbox-pid-namespace"},
			message: "a value is required for '--linux-sandbox-pid-namespace <MODE>' but none was supplied",
		},
		{
			name:    "suggested flag joins the smart usage",
			args:    []string{"doctor", "--summary", "--jso"},
			message: "unexpected argument '--jso' found\n\n  tip: a similar argument exists: '--json'",
			usage:   "Usage: codex doctor --summary --json",
		},
		{
			name:    "approval policy similar value",
			args:    []string{"resume", "--ask-for-approval", "on_request"},
			message: "invalid value 'on_request' for '--ask-for-approval <APPROVAL_POLICY>'\n  [possible values: on-request, never]\n\n  tip: a similar value exists: 'on-request'",
		},
		{
			name:    "sandbox mode similar value",
			args:    []string{"--sandbox", "dangr"},
			message: "invalid value 'dangr' for '--sandbox <SANDBOX_MODE>'\n  [possible values: read-only, workspace-write, danger-full-access]\n\n  tip: a similar value exists: 'danger-full-access'",
		},
		{
			name:    "distant sandbox mode has no suggestion",
			args:    []string{"--sandbox", "bogs"},
			message: "invalid value 'bogs' for '--sandbox <SANDBOX_MODE>'\n  [possible values: read-only, workspace-write, danger-full-access]",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			usageErr := requireClapUsageError(t, testCase.args)
			if usageErr.Message != testCase.message {
				t.Fatalf("message = %q, want %q", usageErr.Message, testCase.message)
			}
			if usageErr.Usage != testCase.usage {
				t.Fatalf("usage = %q, want %q", usageErr.Usage, testCase.usage)
			}
		})
	}
}

// TestExecReviewAcceptsGlobalOptionsLikeRust pins that the options clap declares
// global on `codex exec` also parse after `review`, and that `-c` keeps landing
// in the exec override list.
func TestExecReviewAcceptsGlobalOptionsLikeRust(t *testing.T) {
	parsed, err := Parse([]string{"exec", "review", "--json", "-c", "a=b", "--strict-config"})
	if err != nil {
		t.Fatalf("Parse returned %v", err)
	}
	if !parsed.Exec.JSON {
		t.Fatalf("Exec.JSON = false, want true")
	}
	if len(parsed.Exec.ConfigOverrides) != 1 || parsed.Exec.ConfigOverrides[0] != "a=b" {
		t.Fatalf("ConfigOverrides = %v, want [a=b]", parsed.Exec.ConfigOverrides)
	}
	// `codex exec` marks --strict-config global, so it lands on the exec
	// options rather than on the review subcommand's own copy.
	if !parsed.Exec.StrictConfig {
		t.Fatalf("Exec.StrictConfig = false, want true")
	}
}
