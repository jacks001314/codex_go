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

// subcommandValuePlaceholders maps the value flags of subcommands to the
// <PLACEHOLDER> clap prints when the value is missing, taken from each
// subcommand's `--help` page. Flags whose placeholder depends on the command are
// absent here and handled at their call site.
var subcommandValuePlaceholders = map[string]string{
	"--attempt":                   "<N>",
	"--attempts":                  "<ATTEMPTS>",
	"--base":                      "<BRANCH>",
	"--bearer-token-env-var":      "<ENV_VAR>",
	"--branch":                    "<BRANCH>",
	"--cloud-skills-snapshot":     "<PATH>",
	"--code-mode-host":            "<URL>",
	"--commit":                    "<SHA>",
	"--concurrent-requests":       "<COUNT>",
	"--cursor":                    "<CURSOR>",
	"--download-url":              "<DOWNLOAD_URL_OVERRIDE>",
	"--dump-dir":                  "<DIR>",
	"--env":                       "<ENV_ID>",
	"--environment-id":            "<ID>",
	"--github-oidc-request-url":   "<URL>",
	"--identity-provider-id":      "<ID>",
	"--limit":                     "<N>",
	"--listen":                    "<URL>",
	"--marketplace":               "<MARKETPLACE>",
	"--max-mib-per-second":        "<MIB>",
	"--message":                   "<TEXT>",
	"--name":                      "<NAME>",
	"--oauth-client-id":           "<CLIENT_ID>",
	"--oauth-client-secret":       "<CLIENT_SECRET>",
	"--oauth-resource":            "<RESOURCE>",
	"--oidc-audience":             "<AUDIENCE>",
	"--out":                       "<DIR>",
	"--output":                    "<FILE>",
	"--port":                      "<PORT>",
	"--prettier":                  "<PRETTIER_BIN>",
	"--ref":                       "<REF>",
	"--remote-transport":          "<REMOTE_TRANSPORT>",
	"--restore-release":           "<RESTORE_RELEASE>",
	"--rules":                     "<PATH>",
	"--sandbox-state-json":        "<JSON>",
	"--scopes":                    "<SCOPE,SCOPE>",
	"--server-info":               "<FILE>",
	"--service-account-id":        "<ID>",
	"--sock":                      "<SOCKET_PATH>",
	"--sparse":                    "<PATH>",
	"--staging-dir":               "<STAGING_DIR>",
	"--startup-client-info":       "<STARTUP_CLIENT_INFO>",
	"--thread":                    "<THREAD>",
	"--title":                     "<TITLE>",
	"--upstream-url":              "<UPSTREAM_URL>",
	"--url":                       "<URL>",
	"--user":                      "<USER>",
	"--codex-home":                "<DIR>",
	"--ws-audience":               "<AUDIENCE>",
	"--ws-issuer":                 "<ISSUER>",
	"--ws-max-clock-skew-seconds": "<SECONDS>",
	"--ws-shared-secret-file":     "<PATH>",
	"--ws-token-file":             "<PATH>",
	"--ws-token-sha256":           "<HEX>",
	"--proxy-url":                 "<PROXY_URL>",
	"--proxy-origins-file":        "<PROXY_ORIGINS_FILE>",
	"--target":                    "<TARGET>",
}

// valueRequiredError mirrors clap's missing-value diagnostic for a known flag
// and falls back to the historical Go wording for anything unmapped.
func valueRequiredError(flag string) error {
	spec, ok := valueOptionSpecs[flag]
	if !ok {
		if placeholder, known := subcommandValuePlaceholders[flag]; known {
			return &UsageError{Message: "a value is required for '" + flag + " " + placeholder + "' but none was supplied"}
		}
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

// subcommandUsageText describes a subcommand's clap usage block and whether it
// declares positional arguments (clap only offers the `--` workaround tip when
// the command can take positional values).
type subcommandUsageText struct {
	usage         string
	hasPositional bool
}

var subcommandUsageTexts = map[string]subcommandUsageText{
	"login":                             {"Usage: codex login [OPTIONS] [COMMAND]", false},
	"review":                            {"Usage: codex review [OPTIONS] [PROMPT]", true},
	"logout":                            {"Usage: codex logout [OPTIONS]", false},
	"mcp list":                          {"Usage: codex mcp list [OPTIONS]", false},
	"mcp get":                           {"Usage: codex mcp get [OPTIONS] <NAME>", true},
	"mcp add":                           {"Usage: codex mcp add [OPTIONS] <NAME> (--url <URL> | -- <COMMAND>...)", true},
	"mcp login":                         {"Usage: codex mcp login [OPTIONS] <NAME>", true},
	"mcp logout":                        {"Usage: codex mcp logout [OPTIONS] <NAME>", true},
	"mcp remove":                        {"Usage: codex mcp remove [OPTIONS] <NAME>", true},
	"plugin list":                       {"Usage: codex plugin list [OPTIONS]", false},
	"plugin marketplace add":            {"Usage: codex plugin marketplace add [OPTIONS] <SOURCE>", true},
	"plugin marketplace list":           {"Usage: codex plugin marketplace list [OPTIONS]", false},
	"plugin marketplace upgrade":        {"Usage: codex plugin marketplace upgrade [OPTIONS] [MARKETPLACE_NAME]", true},
	"plugin marketplace remove":         {"Usage: codex plugin marketplace remove [OPTIONS] <MARKETPLACE_NAME>", true},
	"sandbox":                           {"Usage: codex sandbox [OPTIONS] [COMMAND]... [COMMAND]", true},
	"cloud exec":                        {"Usage: codex cloud exec [OPTIONS] --env <ENV_ID> [QUERY]", true},
	"cloud status":                      {"Usage: codex cloud status [OPTIONS] <TASK_ID>", true},
	"cloud list":                        {"Usage: codex cloud list [OPTIONS]", false},
	"responses-api-proxy":               {"Usage: codex responses-api-proxy [OPTIONS]", false},
	"exec-server":                       {"Usage: codex exec-server [OPTIONS] [COMMAND]", false},
	"app-server":                        {"Usage: codex app-server [OPTIONS] [COMMAND]", false},
	"app-server proxy":                  {"Usage: codex app-server proxy [OPTIONS]", false},
	"app-server daemon pid-update-loop": {"Usage: codex app-server daemon pid-update-loop [OPTIONS]", false},
	"app-server daemon bootstrap":       {"Usage: codex app-server daemon bootstrap [OPTIONS]", false},
	"app-server daemon update":          {"Usage: codex app-server daemon update [OPTIONS]", false},
	"queue":                             {"Usage: codex queue [OPTIONS] --thread <THREAD> --message <TEXT>", false},
	"agents":                            {"Usage: codex agents [OPTIONS]", false},
	"app":                               {"Usage: codex app [OPTIONS] [PATH]", true},
	"update":                            {"Usage: codex update [OPTIONS]", false},
	"migrate-nfs-db":                    {"Usage: codex migrate-nfs-db [OPTIONS]", false},
	"migrate-rollouts":                  {"Usage: codex migrate-rollouts [OPTIONS]", false},
	"apply":                             {"Usage: codex apply [OPTIONS] <TASK_ID>", true},
	"remote-control":                    {"Usage: codex remote-control [OPTIONS] [COMMAND]", false},
	"delete":                            {"Usage: codex delete [OPTIONS] <SESSION>", true},
	"resume":                            {"Usage: codex resume [OPTIONS] [SESSION_ID] [PROMPT]", true},
	"fork":                              {"Usage: codex fork [OPTIONS] [SESSION_ID] [PROMPT]", true},
	"archive":                           {"Usage: codex archive [OPTIONS] <SESSION>", true},
	"unarchive":                         {"Usage: codex unarchive [OPTIONS] <SESSION>", true},
	"debug models":                      {"Usage: codex debug models [OPTIONS]", false},
	"debug prompt-input":                {"Usage: codex debug prompt-input [OPTIONS] [PROMPT]", true},
	"debug trace-reduce":                {"Usage: codex debug trace-reduce [OPTIONS] <TRACE_BUNDLE>", true},
	"debug clear-memories":              {"Usage: codex debug clear-memories [OPTIONS]", false},
	"features list":                     {"Usage: codex features list [OPTIONS]", false},
	"features enable":                   {"Usage: codex features enable [OPTIONS] <FEATURE>", true},
	"features disable":                  {"Usage: codex features disable [OPTIONS] <FEATURE>", true},
	"features":                          {"Usage: codex features [OPTIONS] <COMMAND>", false},
	"exec fork":                         {"Usage: codex exec fork [OPTIONS] <SESSION_ID> [PROMPT]", true},
	"mcp":                               {"Usage: codex mcp [OPTIONS] <COMMAND>", false},
	"plugin":                            {"Usage: codex plugin [OPTIONS] <COMMAND>", false},
	"plugin marketplace":                {"Usage: codex plugin marketplace [OPTIONS] <COMMAND>", false},
	"debug":                             {"Usage: codex debug [OPTIONS] <COMMAND>", false},
	"debug app-server":                  {"Usage: codex debug app-server [OPTIONS] <COMMAND>", false},
	"execpolicy":                        {"Usage: codex execpolicy [OPTIONS] <COMMAND>", false},
	"app-server daemon":                 {"Usage: codex app-server daemon [OPTIONS] <COMMAND>", false},
	"cloud":                             {"Usage: codex cloud [OPTIONS] [COMMAND]", false},
	"cloud apply":                       {"Usage: codex cloud apply [OPTIONS] <TASK_ID>", true},
	"cloud diff":                        {"Usage: codex cloud diff [OPTIONS] <TASK_ID>", true},
}

// unexpectedSubcommandArgumentError mirrors clap for a subcommand: the `--`
// workaround tip only appears when the command declares positional arguments.
func unexpectedSubcommandArgumentError(command, arg string) error {
	spec, ok := subcommandUsageTexts[command]
	if !ok {
		return fmt.Errorf("unknown %s option %s", command, arg)
	}
	name := flagNameOnly(arg)
	message := "unexpected argument '" + name + "' found"
	if spec.hasPositional {
		message += "\n\n  tip: to pass '" + name + "' as a value, use '-- " + name + "'"
	}
	return &UsageError{Message: message, Usage: spec.usage, SmartUsage: true}
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
	// SmartUsage marks usage blocks built from the table snapshots, where clap
	// echoes the options it already consumed. The exec family renders its own
	// usage lines and leaves this false.
	SmartUsage bool
	// Raw marks diagnostics raised through clap::Error::raw, which print the
	// message alone: no usage block and no trailing `--help` hint.
	Raw bool
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

	execResumeHelpValueUsageText = "Usage: codex exec resume --help [SESSION_ID] [PROMPT]"
	execForkHelpValueUsageText   = "Usage: codex exec fork --help <SESSION_ID> [PROMPT]"
	execReviewHelpValueUsageText = "Usage: codex exec review --help [PROMPT]"
)

// execHelpValueError renders clap's `--help=<value>` diagnostic for the exec
// family, whose parsers carry their own usage blocks.
func execHelpValueError(arg, usage string) error {
	return &UsageError{
		Message: "unexpected value '" + strings.TrimPrefix(arg, "--help=") + "' for '--help' found; no more were expected",
		Usage:   usage,
	}
}

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

// mcpOAuthClientRegistrationValues are the values Rust's
// McpOAuthClientRegistrationArg accepts; `--help` renders them as
// <AUTO|CIMD|DCR> while clap's diagnostics list them lowercase.
var mcpOAuthClientRegistrationValues = []string{"auto", "cimd", "dcr"}

const mcpOAuthClientRegistrationFlag = "--oauth-client-registration <AUTO|CIMD|DCR>"

// validateMCPOAuthClientRegistration applies clap's value parser for
// `--oauth-client-registration`, which rejects an empty value and anything
// outside the enum.
func validateMCPOAuthClientRegistration(value string) error {
	if value == "" {
		return missingValueError(mcpOAuthClientRegistrationFlag, mcpOAuthClientRegistrationValues)
	}
	for _, allowed := range mcpOAuthClientRegistrationValues {
		if value == allowed {
			return nil
		}
	}
	return invalidValueError(value, mcpOAuthClientRegistrationFlag, mcpOAuthClientRegistrationValues)
}

// clapRequiredKeepsOptions lists the commands whose missing-argument usage
// keeps [OPTIONS]: clap cannot drop the optional block when the remaining
// requirement comes from an argument group.
var clapRequiredKeepsOptions = map[string]bool{"mcp add": true}

// requiredArgumentsMessage renders the body of clap's "required arguments were
// not provided" diagnostic, one indented line per missing argument.
func requiredArgumentsMessage(missing ...string) string {
	message := "the following required arguments were not provided:"
	for _, name := range missing {
		message += "\n  " + name
	}
	return message
}

// decorateSubcommandClapUsage reproduces clap's smart usage for diagnostics a
// subcommand parser raised: once clap has consumed an option it drops the
// [OPTIONS] placeholder from the usage block and echoes the options it saw, in
// the order they appeared. Errors clap raises before it parsed any option (an
// unknown token first in argv) keep the plain table usage.
func decorateSubcommandClapUsage(err error, args []string) error {
	usageErr, ok := err.(*UsageError)
	if !ok || usageErr.Usage == "" || !usageErr.SmartUsage {
		return err
	}
	stopAt := ""
	switch {
	case strings.HasPrefix(usageErr.Message, "unexpected argument '"):
		stopAt, _, _ = strings.Cut(strings.TrimPrefix(usageErr.Message, "unexpected argument '"), "'")
	case strings.HasPrefix(usageErr.Message, "the following required arguments were not provided:"):
		// Every option in argv was accepted, so echo all of them.
	default:
		return err
	}
	// clap cannot drop the optional block when the remaining requirement is an
	// argument group (`mcp add` requires either --url or a COMMAND).
	if usageErr.Usage == subcommandUsageTexts["mcp add"].usage {
		return err
	}
	used := clapUsageOptionTokens(args, stopAt)
	if strings.HasPrefix(usageErr.Usage, "Usage: codex queue ") {
		if strings.HasPrefix(usageErr.Message, "unexpected argument '") && !clapLooksLikeOption(stopAt) {
			// clap keeps the plain table block for a stray positional.
			return err
		}
		used = queueVisibleUsageOptions(used)
		// clap rebuilds the usage line once it consumed an option, so the
		// [OPTIONS] placeholder disappears even when the consumed options are
		// the required ones the block already lists.
		if len(used) > 0 {
			usageErr.Usage = strings.Replace(usageErr.Usage, "[OPTIONS] ", "", 1)
		}
	}
	if len(used) == 0 {
		return err
	}
	usageErr.Usage = usageWithUsedOptions(usageErr.Usage, used)
	return err
}

// queueVisibleUsageOptions keeps the options clap echoes for `codex queue`.
// QueueCommand hides every inherited option except the ones
// queue_cmd::restricted_options allows, and clap never echoes hidden options.
func queueVisibleUsageOptions(used []string) []string {
	kept := make([]string, 0, len(used))
	for _, token := range used {
		name, _, _ := strings.Cut(token, " ")
		if queueEchoableUsageOptions[name] {
			kept = append(kept, token)
		}
	}
	return kept
}

// queueEchoableUsageOptions is the complement of queue_cmd::restricted_options,
// keyed by flag (the value placeholder is part of the echoed token, not the key).
var queueEchoableUsageOptions = map[string]bool{
	"--thread":                true,
	"--message":               true,
	"--remote":                true,
	"--remote-auth-token-env": true,
	"--config":                true,
	"--enable":                true,
	"--disable":               true,
	"--profile":               true,
	"--strict-config":         true,
	"--cd":                    true,
	"--resume":                true,
}

// clapUsageOptionTokens renders the options clap accepted, stopping in front of
// the token that produced the diagnostic. Option values are folded into the
// flag token so `--enable x` renders as `--enable <FEATURE>`.
func clapUsageOptionTokens(args []string, stopAt string) []string {
	used := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			break
		}
		if !clapLooksLikeOption(arg) {
			continue
		}
		if stopAt != "" && flagNameOnly(arg) == stopAt {
			break
		}
		token, consumedValue := clapUsageOptionToken(arg)
		used = append(used, token)
		if consumedValue {
			i++
		}
	}
	return used
}

// clapUsageOptionToken renders one option the way clap's usage block does and
// reports whether the following argv token was its value.
func clapUsageOptionToken(arg string) (string, bool) {
	name, hasInlineValue := arg, false
	if trimmed, _, ok := strings.Cut(arg, "="); ok {
		name, hasInlineValue = trimmed, true
	}
	if spec, known := valueOptionSpecs[name]; known {
		return spec.long + " " + spec.placeholder, !hasInlineValue
	}
	if placeholder, known := subcommandValuePlaceholders[name]; known {
		return name + " " + placeholder, !hasInlineValue
	}
	return name, false
}

// usageWithUsedOptions inserts clap's echoed options into a usage block: the
// [OPTIONS] placeholder is replaced when present, otherwise the options go in
// front of the first positional (or at the end for option-only commands).
func usageWithUsedOptions(usage string, used []string) string {
	echoed := make([]string, 0, len(used))
	for _, token := range used {
		// clap only prints options the static usage does not already list;
		// required options are part of the block either way.
		if strings.Contains(usage, token) {
			continue
		}
		echoed = append(echoed, token)
	}
	if len(echoed) == 0 {
		return usage
	}
	joined := strings.Join(echoed, " ")
	if strings.Contains(usage, "[OPTIONS]") {
		return strings.Replace(usage, "[OPTIONS]", joined, 1)
	}
	fields := strings.Fields(usage)
	for idx, field := range fields {
		if !strings.HasPrefix(field, "<") && !strings.HasPrefix(field, "[") {
			continue
		}
		// A placeholder that renders an option's value (`--thread <THREAD>`) is
		// part of the option, not a positional, so keep looking.
		if idx > 0 && clapOptionPlaceholder(fields[idx-1]) == field {
			continue
		}
		expanded := make([]string, 0, len(fields)+1)
		expanded = append(expanded, fields[:idx]...)
		expanded = append(expanded, joined)
		expanded = append(expanded, fields[idx:]...)
		return strings.Join(expanded, " ")
	}
	return usage + " " + joined
}

// clapOptionPlaceholder returns the value placeholder clap renders for an
// option, or "" when the option takes no value.
func clapOptionPlaceholder(option string) string {
	if spec, known := valueOptionSpecs[option]; known {
		return spec.placeholder
	}
	return subcommandValuePlaceholders[option]
}

// sessionPositionalSlot is one positional slot of a session subcommand.
type sessionPositionalSlot struct {
	name     string
	required bool
}

// sessionPositionalSlots lists the positional slots clap binds for the session
// subcommands (`codex resume`, `codex fork`, `codex archive`, `codex unarchive`
// and `codex delete`), in declaration order.
var sessionPositionalSlots = map[string][]sessionPositionalSlot{
	"resume":    {{name: "SESSION_ID"}, {name: "PROMPT"}},
	"fork":      {{name: "SESSION_ID"}, {name: "PROMPT"}},
	"archive":   {{name: "SESSION", required: true}},
	"unarchive": {{name: "SESSION", required: true}},
	"delete":    {{name: "SESSION", required: true}},
}

// sessionUnexpectedArgumentError mirrors clap's diagnostic for an unknown token
// on a session subcommand: options clap already consumed are echoed in clap's
// smart usage block, and the positional slots reflect what the parser bound.
func sessionUnexpectedArgumentError(command string, consumed []string, arg string) error {
	name := flagNameOnly(arg)
	if similar, ok := sessionSimilarArgumentTips[name]; ok {
		// clap scores the unknown flag against the declared ones and folds the
		// suggested argument into the smart usage block as well.
		return &UsageError{
			Message: "unexpected argument '" + name + "' found\n\n  tip: a similar argument exists: '" + similar + "'",
			Usage:   sessionClapUsage(command, append(append([]string{}, consumed...), similar)),
		}
	}
	return &UsageError{
		Message: "unexpected argument '" + name + "' found\n\n  tip: to pass '" + name + "' as a value, use '-- " + name + "'",
		Usage:   sessionClapUsage(command, consumed),
	}
}

// sessionSimilarArgumentTips mirrors clap's "a similar argument exists" tip for
// removed spawn-history flags whose spelling is closest to `--last`.
var sessionSimilarArgumentTips = map[string]string{
	"--last-n":       "--last",
	"--last-turn-id": "--last",
}

// sessionLastPromptConflictError renders clap's conflict diagnostic for
// `--last` combined with an explicit SESSION_ID and PROMPT: Rust marks the TUI
// prompt argument as conflicts_with = "last" for `codex resume`/`codex fork`.
func sessionLastPromptConflictError(command string) error {
	return &UsageError{
		Message: "the argument '--last' cannot be used with '[PROMPT]'",
		Usage:   "Usage: codex " + command + " --last <SESSION_ID> [PROMPT]",
	}
}

// sessionClapUsage builds clap's smart usage block for a session subcommand
// diagnostic: consumed options are echoed in argv order and every positional
// slot shows whether the parser already bound it. clap prints the plain table
// block while nothing has been consumed yet.
func sessionClapUsage(command string, consumed []string) string {
	spec, ok := subcommandUsageTexts[command]
	if !ok {
		return ""
	}
	slots, hasSlots := sessionPositionalSlots[command]
	if !hasSlots {
		return spec.usage
	}
	used, bound := clapSessionUsageState(consumed)
	if len(used) == 0 && bound == 0 {
		return spec.usage
	}
	fields := make([]string, 0, len(used)+len(slots)+1)
	fields = append(fields, "Usage: codex "+command)
	fields = append(fields, used...)
	for idx, slot := range slots {
		if slot.required || idx < bound {
			fields = append(fields, "<"+slot.name+">")
			continue
		}
		fields = append(fields, "["+slot.name+"]")
	}
	return strings.Join(fields, " ")
}

// clapSessionUsageState replays the tokens clap already accepted and returns the
// option tokens clap echoes in its smart usage block plus the number of bound
// positionals. The session subcommands declare `--image` as num_args = 1.., so
// every following non-flag token is one of its values, not a positional.
func clapSessionUsageState(consumed []string) ([]string, int) {
	used := make([]string, 0, len(consumed))
	seen := make(map[string]bool, len(consumed))
	bound := 0
	for i := 0; i < len(consumed); i++ {
		arg := consumed[i]
		if arg == "--" {
			bound += len(consumed) - i - 1
			break
		}
		if !clapLooksLikeOption(arg) {
			bound++
			continue
		}
		token, consumesValue := clapUsageOptionToken(arg)
		if !seen[token] {
			seen[token] = true
			used = append(used, token)
		}
		if isSessionVariadicImageFlag(arg) {
			for i+1 < len(consumed) && consumed[i+1] != "--" && !clapLooksLikeOption(consumed[i+1]) {
				i++
			}
			continue
		}
		if !consumesValue {
			continue
		}
		i++
	}
	return used, bound
}

// isSessionVariadicImageFlag reports whether arg is the `--image` spelling of a
// session subcommand, which Rust declares with num_args = 1..
func isSessionVariadicImageFlag(arg string) bool {
	return arg == "--image" || arg == "-i" || strings.HasPrefix(arg, "--image=")
}

// missingRequiredArgumentsError mirrors clap's diagnostic for absent required
// arguments. The usage block is clap's smart usage: supplied flags are echoed by
// the caller and [OPTIONS] disappears unless the requirement is an argument
// group.
func missingRequiredArgumentsError(command string, missing ...string) error {
	spec, ok := subcommandUsageTexts[command]
	if !ok {
		return fmt.Errorf("%s requires %s", command, strings.Join(missing, ", "))
	}
	usage := spec.usage
	if !clapRequiredKeepsOptions[command] {
		usage = strings.Replace(usage, "[OPTIONS] ", "", 1)
	}
	return &UsageError{Message: requiredArgumentsMessage(missing...), Usage: usage, SmartUsage: true}
}

// clapErrorText renders a complete clap error the way clap's Display does: the
// message, an optional usage block and the trailing help hint. The result ends
// with a single newline, which matters for callers that wrap it again.
func clapErrorText(message, usage string) string {
	if usage == "" {
		return "error: " + message + "\n\nFor more information, try '--help'.\n"
	}
	return "error: " + message + "\n\n" + usage + "\n\nFor more information, try '--help'.\n"
}

// subcommandCandidates lists the names clap offers as did-you-mean candidates
// for each group command; clap scores every subcommand, hidden ones included.
var subcommandCandidates = map[string][]string{
	"login":              {"status", "help"},
	"mcp":                {"list", "get", "add", "remove", "login", "logout", "help"},
	"features":           {"list", "enable", "disable", "help"},
	"plugin":             {"add", "list", "marketplace", "remove", "help"},
	"plugin marketplace": {"add", "list", "upgrade", "remove", "help"},
	"debug":              {"models", "app-server", "prompt-input", "trace-reduce", "clear-memories", "help"},
	"debug app-server":   {"send-message-v2", "help"},
	"execpolicy":         {"check", "help"},
	"app-server":         {"daemon", "proxy", "generate-ts", "generate-json-schema", "generate-internal-json-schema", "help"},
	"app-server daemon":  {"bootstrap", "start", "restart", "update", "enable-remote-control", "disable-remote-control", "stop", "version", "pid-update-loop", "help"},
	"cloud":              {"exec", "status", "list", "apply", "diff", "help"},
	"remote-control":     {"start", "stop", "pair", "help"},
}

// unrecognizedSubcommandForGroup mirrors clap's diagnostic for an unknown token
// under a group command: an unknown flag is an unexpected argument, while a bare
// word gets clap's did-you-mean treatment.
func unrecognizedSubcommandForGroup(group, arg string) error {
	spec, ok := subcommandUsageTexts[group]
	if !ok {
		return fmt.Errorf("unknown %s subcommand %s", group, arg)
	}
	if strings.HasPrefix(arg, "-") {
		return unexpectedSubcommandArgumentError(group, arg)
	}
	return unrecognizedSubcommandWithSuggestion(arg, spec.usage, subcommandCandidates[group])
}

// unrecognizedSubcommandWithSuggestion mirrors clap's unrecognized-subcommand
// diagnostic, including its did-you-mean tip when a candidate scores above
// clap's similarity cutoff.
func unrecognizedSubcommandWithSuggestion(name, usage string, candidates []string) error {
	message := unrecognizedSubcommandMessage(name, candidates)
	return &UsageError{Message: message, Usage: usage}
}

// unrecognizedSubcommandMessage renders the diagnostic body without the usage
// block.
func unrecognizedSubcommandMessage(name string, candidates []string) string {
	message := "unrecognized subcommand '" + name + "'"
	suggestions := suggestedSubcommands(name, candidates)
	switch len(suggestions) {
	case 0:
		return message
	case 1:
		return message + "\n\n  tip: a similar subcommand exists: '" + suggestions[0] + "'"
	default:
		quoted := make([]string, len(suggestions))
		for i, suggestion := range suggestions {
			quoted[i] = "'" + suggestion + "'"
		}
		return message + "\n\n  tip: some similar subcommands exist: " + strings.Join(quoted, ", ")
	}
}

// suggestedSubcommands returns the candidates clap keeps in the order clap
// prints them: ascending Jaro similarity, least similar first.
func suggestedSubcommands(name string, candidates []string) []string {
	type scored struct {
		score float64
		name  string
	}
	var ranked []scored
	for _, candidate := range candidates {
		score := jaroSimilarity(name, candidate)
		if score <= 0.7 {
			continue
		}
		position := 0
		for position < len(ranked) && ranked[position].score <= score {
			position++
		}
		ranked = append(ranked, scored{})
		copy(ranked[position+1:], ranked[position:])
		ranked[position] = scored{score: score, name: candidate}
	}
	suggestions := make([]string, len(ranked))
	for i, candidate := range ranked {
		suggestions[i] = candidate.name
	}
	return suggestions
}

// jaroSimilarity implements strsim-rs's Jaro metric, which clap uses for its
// suggestions (clap_builder/src/parser/features/suggestions.rs). The float
// arithmetic is kept in strsim's order so that scores landing exactly on clap's
// 0.7 cutoff fall on the same side.
func jaroSimilarity(a, b string) float64 {
	arunes := []rune(a)
	brunes := []rune(b)
	if len(arunes) == 0 || len(brunes) == 0 {
		return 0
	}
	if a == b {
		return 1
	}
	window := len(arunes)
	if len(brunes) > window {
		window = len(brunes)
	}
	window = window/2 - 1
	if window < 0 {
		window = 0
	}
	bMatched := make([]bool, len(brunes))
	matchedA := make([]rune, 0, len(arunes))
	for i, char := range arunes {
		start := i - window
		if start < 0 {
			start = 0
		}
		end := i + window + 1
		if end > len(brunes) {
			end = len(brunes)
		}
		for j := start; j < end; j++ {
			if bMatched[j] || brunes[j] != char {
				continue
			}
			bMatched[j] = true
			matchedA = append(matchedA, char)
			break
		}
	}
	matches := len(matchedA)
	if matches == 0 {
		return 0
	}
	matchedB := make([]rune, 0, matches)
	for i, matched := range bMatched {
		if matched {
			matchedB = append(matchedB, brunes[i])
		}
	}
	mismatches := 0
	for i, char := range matchedA {
		if char != matchedB[i] {
			mismatches++
		}
	}
	transpositions := float64(mismatches) / 2
	score := float64(matches)
	return (score/float64(len(arunes)) + score/float64(len(brunes)) + (score-transpositions)/score) / 3
}

// sandboxSetupMissingValueError mirrors clap's missing-value diagnostic for the
// nested `setup` parser.
func sandboxSetupMissingValueError(flag string) error {
	return sandboxSetupError("a value is required for '"+flag+"' but none was supplied", "")
}

// sandboxSetupUsageText is the usage line clap renders for the nested parser
// Rust builds for `codex sandbox setup`, whose program name is just "setup".
const sandboxSetupUsageText = "Usage: setup [OPTIONS] <--user <USER>|--current-user>"

// sandboxSetupDiagnostic carries a clap error produced by the nested `setup`
// parser. Rust converts it into an anyhow error, so the CLI prints it with the
// `Error: ` prefix and exits 1 instead of clap's usual "error: " and status 2.
type sandboxSetupDiagnostic struct{ text string }

func (e *sandboxSetupDiagnostic) Error() string { return e.text }

// sandboxSetupError wraps a rendered clap message in the anyhow-style error Rust
// ends up printing for `codex sandbox setup`.
func sandboxSetupError(message, usage string) error {
	return &sandboxSetupDiagnostic{text: clapErrorText(message, usage)}
}

// sandboxSetupUsageParts renders clap's smart usage for the nested setup
// parser. codexHomeFirst mirrors the diagnostics where clap lists the missing
// or required --codex-home before the options it already consumed; the consumed
// options themselves keep their argv order.
func sandboxSetupUsageParts(sandbox *SandboxOptions, requireCodexHome bool) string {
	consumedCodexHome := false
	if sandbox != nil {
		for _, option := range sandbox.setupEchoOptions {
			if option == "--codex-home <DIR>" {
				consumedCodexHome = true
			}
		}
	}
	parts := []string{"Usage: setup"}
	// A required --codex-home that was never parsed is listed first; an already
	// consumed one keeps its argv position.
	if requireCodexHome && !consumedCodexHome {
		parts = append(parts, "--codex-home <DIR>")
	}
	if sandbox != nil {
		parts = append(parts, sandbox.setupEchoOptions...)
	}
	parts = append(parts, "<--user <USER>|--current-user>")
	return strings.Join(parts, " ")
}

// sandboxSetupUnexpectedUsage renders clap's smart usage for an unexpected
// argument: supplied options replace the [OPTIONS] placeholder.
func sandboxSetupUnexpectedUsage(sandbox *SandboxOptions) string {
	if sandbox == nil || !sandbox.setupAnyOption {
		return sandboxSetupUsageText
	}
	return sandboxSetupUsageParts(sandbox, false)
}

// sandboxSetupArgumentError mirrors clap's unexpected-argument diagnostic for
// `codex sandbox setup`.
func sandboxSetupArgumentError(sandbox *SandboxOptions, arg string) error {
	message := "unexpected argument '" + flagNameOnly(arg) + "' found"
	return sandboxSetupError(message, sandboxSetupUnexpectedUsage(sandbox))
}

// sandboxSetupMissingUserError mirrors clap's diagnostic when neither --user nor
// --current-user was supplied.
func sandboxSetupMissingUserError(sandbox *SandboxOptions) error {
	return sandboxSetupError(requiredArgumentsMessage("<--user <USER>|--current-user>"), sandboxSetupUsageParts(sandbox, false))
}

// sandboxSetupMissingCodexHomeError mirrors clap's diagnostic for --user without
// the --codex-home it requires. clap lists the missing requirement first and the
// supplied optional flag after it.
func sandboxSetupMissingCodexHomeError(sandbox *SandboxOptions) error {
	return sandboxSetupError(requiredArgumentsMessage("--codex-home <DIR>"), sandboxSetupUsageParts(sandbox, true))
}

// sandboxSetupConflictError mirrors clap's conflict diagnostic for --user and
// --current-user: the argument accepted first is named first, and the usage
// block keeps the --codex-home dependency of --user plus the flags already
// supplied.
func sandboxSetupConflictError(first, second string, sandbox *SandboxOptions) error {
	// A conflict that names --user keeps the --codex-home it requires in front
	// of the consumed options, matching clap's usage builder.
	return sandboxSetupError("the argument '"+first+"' cannot be used with '"+second+"'", sandboxSetupUsageParts(sandbox, first == "--user <USER>"))
}
