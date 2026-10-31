package cli

import "strings"

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
