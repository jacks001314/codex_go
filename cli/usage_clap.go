package cli

import (
	"fmt"
	"strings"
)

const (
	rootUsageText   = "Usage: codex [OPTIONS] [PROMPT]\n       codex [OPTIONS] <COMMAND> [ARGS]"
	doctorUsageText = "Usage: codex doctor [OPTIONS]"
)

// clapValueOption describes how clap renders a flag in its "a value is
// required for ..." diagnostic: the long spelling it echoes after a short
// alias, the value placeholder, and any possible values.
type clapValueOption struct {
	long        string
	placeholder string
	possible    []string
}

// valueOptionSpecs maps every spelling of the root, shared and doctor value
// options to clap's rendering (taken from `codex --help`).
var valueOptionSpecs = map[string]clapValueOption{
	"-c":                      {"--config", "<key=value>", nil},
	"--config":                {"--config", "<key=value>", nil},
	"--enable":                {"--enable", "<FEATURE>", nil},
	"--disable":               {"--disable", "<FEATURE>", nil},
	"--remote":                {"--remote", "<ADDR>", nil},
	"--remote-auth-token-env": {"--remote-auth-token-env", "<ENV_VAR>", nil},
	"-i":                      {"--image", "<FILE>...", nil},
	"--image":                 {"--image", "<FILE>...", nil},
	"-m":                      {"--model", "<MODEL>", nil},
	"--model":                 {"--model", "<MODEL>", nil},
	"--local-provider":        {"--local-provider", "<OSS_PROVIDER>", nil},
	"-p":                      {"--profile", "<CONFIG_PROFILE_V2>", nil},
	"--profile":               {"--profile", "<CONFIG_PROFILE_V2>", nil},
	"-s":                      {"--sandbox", "<SANDBOX_MODE>", sandboxModeValues},
	"--sandbox":               {"--sandbox", "<SANDBOX_MODE>", sandboxModeValues},
	"-C":                      {"--cd", "<DIR>", nil},
	"--cd":                    {"--cd", "<DIR>", nil},
	"--add-dir":               {"--add-dir", "<DIR>", nil},
	"-a":                      {"--ask-for-approval", "<APPROVAL_POLICY>", approvalPolicyValues},
	"--ask-for-approval":      {"--ask-for-approval", "<APPROVAL_POLICY>", approvalPolicyValues},
	"--probe-filesystem-path": {"--probe-filesystem-path", "<PROBE_FILESYSTEM_PATH>", nil},
}

var approvalPolicyValues = []string{"on-request", "never"}

// valueRequiredError mirrors clap's missing-value diagnostic for a known flag
// and falls back to the historical Go wording for anything unmapped.
func valueRequiredError(flag string) error {
	spec, ok := valueOptionSpecs[flag]
	if !ok {
		return fmt.Errorf("%s requires a value", flag)
	}
	message := "a value is required for '" + spec.long + " " + spec.placeholder + "' but none was supplied"
	if len(spec.possible) > 0 {
		message += "\n  [possible values: " + strings.Join(spec.possible, ", ") + "]"
	}
	return &UsageError{Message: message}
}

// flagNameOnly is clap's rendering of an unexpected flag: the `=value` part of a
// long option is dropped.
func flagNameOnly(arg string) string {
	if !strings.HasPrefix(arg, "--") {
		return arg
	}
	if index := strings.IndexByte(arg, '='); index >= 0 {
		return arg[:index]
	}
	return arg
}

// subcommandOnlyLongFlags are root-unknown long flags whose owning subcommand
// clap names in its suggestion.
var subcommandOnlyLongFlags = map[string]string{
	"--json":                "exec",
	"--skip-git-repo-check": "exec",
	"--output-schema":       "exec",
	"--output-last-message": "exec",
}

// unexpectedRootArgumentError mirrors clap's root diagnostic, including the
// `'exec --json' exists` tip for flags that only a later subcommand accepts.
func unexpectedRootArgumentError(arg string, remaining []string) error {
	name := flagNameOnly(arg)
	tip := "  tip: to pass '" + name + "' as a value, use '-- " + name + "'"
	if owner, ok := subcommandOnlyLongFlags[name]; ok {
		for _, candidate := range remaining {
			if candidate == owner {
				tip = "  tip: '" + owner + " " + name + "' exists"
				break
			}
		}
	}
	return &UsageError{
		Message: "unexpected argument '" + name + "' found\n\n" + tip,
		Usage:   rootUsageText,
	}
}

// unexpectedArgumentWithoutTip is clap's diagnostic for commands that declare no
// positional arguments, where the `--` workaround tip would be meaningless.
func unexpectedArgumentWithoutTip(arg, usage string) error {
	return &UsageError{Message: "unexpected argument '" + flagNameOnly(arg) + "' found", Usage: usage}
}

// UsageError mirrors a clap argument diagnostic. Rust's CLI prints the message,
// an optional usage block and the trailing hint, then exits with status 2.
type UsageError struct {
	// Message is clap's diagnostic body without the leading "error: ",
	// including indented detail lines such as a suggestion tip.
	Message string
	// Usage is the "Usage: ..." block; it is empty for diagnostics clap
	// prints without one (invalid and missing values).
	Usage string
}

func (e *UsageError) Error() string { return e.Message }

// sandboxModeValues are the `--sandbox` values Rust's SandboxModeCliArg accepts
// (codex-rs/utils/cli/src/sandbox_mode_cli_arg.rs).
var sandboxModeValues = []string{"read-only", "workspace-write", "danger-full-access"}

// colorValues are the `--color` values Rust's ColorCliArg accepts.
var colorValues = []string{"always", "never", "auto"}

const (
	execUsageText       = "Usage: codex exec [OPTIONS] [PROMPT]\n       codex exec [OPTIONS] <COMMAND> [ARGS]"
	execResumeUsageText = "Usage: codex exec resume [OPTIONS] [SESSION_ID] [PROMPT]"
	execForkUsageText   = "Usage: codex exec fork [OPTIONS] <SESSION_ID> [PROMPT]"
	execReviewUsageText = "Usage: codex exec review [OPTIONS] [PROMPT]"
)

// execResumeUsageTextFor renders clap's resume usage line. clap prints the
// session selector flag instead of [OPTIONS] once the caller supplied it,
// because `--last`/`--all` occupy the same required slot as SESSION_ID.
func execResumeUsageTextFor(last, all bool) string {
	switch {
	case last:
		return "Usage: codex exec resume --last [SESSION_ID] [PROMPT]"
	case all:
		return "Usage: codex exec resume --all [SESSION_ID] [PROMPT]"
	default:
		return execResumeUsageText
	}
}

// execResumeFlagUsageText renders clap's resume usage for an unexpected flag:
// supplied positionals are shown with angle brackets, the `[OPTIONS]`
// placeholder disappears, and --last/--all are echoed.
func execResumeFlagUsageText(last, all bool, supplied int) string {
	if !last && !all && supplied == 0 {
		return execResumeUsageText
	}
	var builder strings.Builder
	builder.WriteString("Usage: codex exec resume ")
	switch {
	case last:
		builder.WriteString("--last ")
	case all:
		builder.WriteString("--all ")
	}
	switch supplied {
	case 0:
		builder.WriteString("[SESSION_ID] [PROMPT]")
	case 1:
		builder.WriteString("<SESSION_ID> [PROMPT]")
	default:
		builder.WriteString("<SESSION_ID> <PROMPT>")
	}
	return builder.String()
}

func unexpectedArgumentError(arg, usage string) error {
	return &UsageError{
		Message: "unexpected argument '" + arg + "' found\n\n  tip: to pass '" + arg + "' as a value, use '-- " + arg + "'",
		Usage:   usage,
	}
}

// unexpectedPositionalError is clap's diagnostic when a second positional hits
// an already-satisfied slot: unlike an unknown flag there is no `--` tip.
func unexpectedPositionalError(arg, usage string) error {
	return &UsageError{Message: "unexpected argument '" + arg + "' found", Usage: usage}
}

func unrecognizedSubcommandError(name, usage string) error {
	return &UsageError{Message: "unrecognized subcommand '" + name + "'", Usage: usage}
}

func invalidValueError(value, flag string, possible []string) error {
	return &UsageError{
		Message: "invalid value '" + value + "' for '" + flag + "'\n  [possible values: " + strings.Join(possible, ", ") + "]" + similarValueTip(value, possible),
	}
}

func missingValueError(flag string, possible []string) error {
	return &UsageError{
		Message: "a value is required for '" + flag + "' but none was supplied\n  [possible values: " + strings.Join(possible, ", ") + "]",
	}
}

// similarValueTip mirrors clap's "a similar value exists" hint for values that
// are a prefix of (or extend) a possible value.
func similarValueTip(value string, possible []string) string {
	best := ""
	for _, candidate := range possible {
		if candidate == value {
			return ""
		}
		if !strings.HasPrefix(candidate, value) && !strings.HasPrefix(value, candidate) {
			continue
		}
		if best == "" || len(candidate) < len(best) {
			best = candidate
		}
	}
	if best == "" {
		return ""
	}
	return "\n\n  tip: a similar value exists: '" + best + "'"
}

// validateSandboxMode applies clap's value parser for `--sandbox`.
func validateSandboxMode(value string) error {
	if value == "" {
		return missingValueError("--sandbox <SANDBOX_MODE>", sandboxModeValues)
	}
	for _, allowed := range sandboxModeValues {
		if value == allowed {
			return nil
		}
	}
	return invalidValueError(value, "--sandbox <SANDBOX_MODE>", sandboxModeValues)
}
