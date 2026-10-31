package cli

import (
	"fmt"
	"sort"
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
	"--attempt":                     "<N>",
	"--attempts":                    "<ATTEMPTS>",
	"--base":                        "<BRANCH>",
	"--bearer-token-env-var":        "<ENV_VAR>",
	"--branch":                      "<BRANCH>",
	"--cloud-skills-snapshot":       "<PATH>",
	"--code-mode-host":              "<URL>",
	"--commit":                      "<SHA>",
	"--concurrent-requests":         "<COUNT>",
	"--cursor":                      "<CURSOR>",
	"--download-url":                "<DOWNLOAD_URL_OVERRIDE>",
	"--dump-dir":                    "<DIR>",
	"--env":                         "<ENV_ID>",
	"--environment-id":              "<ID>",
	"--github-oidc-request-url":     "<URL>",
	"--identity-provider-id":        "<ID>",
	"--limit":                       "<N>",
	"--linux-sandbox-pid-namespace": "<MODE>",
	"--listen":                      "<URL>",
	"--oauth-client-registration":   "<AUTO|CIMD|DCR>",
	"--marketplace":                 "<MARKETPLACE>",
	"--max-mib-per-second":          "<MIB>",
	"--message":                     "<TEXT>",
	"--name":                        "<NAME>",
	"--oauth-client-id":             "<CLIENT_ID>",
	"--oauth-client-secret":         "<CLIENT_SECRET>",
	"--oauth-resource":              "<RESOURCE>",
	"--oidc-audience":               "<AUDIENCE>",
	"--out":                         "<DIR>",
	"--output":                      "<FILE>",
	"--port":                        "<PORT>",
	"--prettier":                    "<PRETTIER_BIN>",
	"--ref":                         "<REF>",
	"--remote-transport":            "<REMOTE_TRANSPORT>",
	"--restore-release":             "<RESTORE_RELEASE>",
	"--rules":                       "<PATH>",
	"--sandbox-state-json":          "<JSON>",
	"--scopes":                      "<SCOPE,SCOPE>",
	"--server-info":                 "<FILE>",
	"--service-account-id":          "<ID>",
	"--sock":                        "<SOCKET_PATH>",
	"--sparse":                      "<PATH>",
	"--staging-dir":                 "<STAGING_DIR>",
	"--startup-client-info":         "<STARTUP_CLIENT_INFO>",
	"--thread":                      "<THREAD>",
	"--title":                       "<TITLE>",
	"--upstream-url":                "<UPSTREAM_URL>",
	"--url":                         "<URL>",
	"--user":                        "<USER>",
	"--codex-home":                  "<DIR>",
	"--ws-audience":                 "<AUDIENCE>",
	"--ws-issuer":                   "<ISSUER>",
	"--ws-max-clock-skew-seconds":   "<SECONDS>",
	"--ws-auth":                     "<MODE>",
	"--ws-shared-secret-file":       "<PATH>",
	"--ws-token-file":               "<PATH>",
	"--ws-token-sha256":             "<HEX>",
	"--connect":                     "<URL>",
	"--aws-profile":                 "<PROFILE>",
	"--aws-region":                  "<REGION>",
	"--aws-service":                 "<SERVICE>",
	"--proxy-url":                   "<PROXY_URL>",
	"--proxy-origins-file":          "<PROXY_ORIGINS_FILE>",
	"--target":                      "<TARGET>",
	"--thread-source":               "<SOURCE>",
	"--cyber-access-program":        "<PROGRAM>",
	"--output-schema":               "<FILE>",
	"--output-last-message":         "<FILE>",
	"--api-key":                     "[<API_KEY>]",
	"--color":                       "<COLOR>",
}

// clapProfileValueError mirrors clap's ConfigProfile value parser: the name is
// validated while argv is matched, and an empty name reports the same wording.
func clapProfileValueError(flag, placeholder, value string) error {
	return &UsageError{Message: "invalid value '" + value + "' for '" + flag + " " + placeholder + "': invalid --profile value `" + value + "`; pass a plain name such as `work`"}
}

// subcommandValuePossibleValues lists the possible values a subcommand flag
// advertises in its missing-value diagnostic; clap repeats them whenever the
// argument is a possible-values parser.
var subcommandValuePossibleValues = map[string][]string{
	"--ws-auth":              {"capability-token", "signed-bearer-token"},
	"--remote-transport":     {"noise", "direct"},
	"--cyber-access-program": {"standard", "daybreak_blue", "daybreak_red"},
}

// valueRequiredError mirrors clap's missing-value diagnostic for a known flag
// and falls back to the historical Go wording for anything unmapped.
func valueRequiredError(flag string) error {
	spec, ok := valueOptionSpecs[flag]
	if !ok {
		if placeholder, known := subcommandValuePlaceholders[flag]; known {
			message := "a value is required for '" + flag + " " + placeholder + "' but none was supplied"
			if possible := subcommandValuePossibleValues[flag]; len(possible) > 0 {
				message += "\n  [possible values: " + strings.Join(possible, ", ") + "]"
			}
			return &UsageError{Message: message}
		}
		return fmt.Errorf("%s requires a value", flag)
	}
	message := "a value is required for '" + spec.long + " " + spec.placeholder + "' but none was supplied"
	if len(spec.possible) > 0 {
		message += "\n  [possible values: " + strings.Join(spec.possible, ", ") + "]"
	}
	return &UsageError{Message: message}
}

// pendingValueRequiredError is clap's "a value is required" diagnostic together
// with the token the parser refused to bind. When that token turns out to be an
// undeclared flag, clap reports the unknown argument instead.
func pendingValueRequiredError(flag string, args []string, i int) error {
	err := valueRequiredError(flag)
	usageErr, ok := err.(*UsageError)
	if !ok {
		return err
	}
	if i+1 < len(args) {
		usageErr.PendingFlag = args[i]
		usageErr.PendingNext = args[i+1]
	}
	return usageErr
}

// missingValueNamedError is clap's "a value is required" diagnostic for a flag
// whose placeholder depends on the enclosing subcommand.
func missingValueNamedError(flag, placeholder string, possible []string) error {
	message := "a value is required for '" + flag + " " + placeholder + "' but none was supplied"
	if len(possible) > 0 {
		message += "\n  [possible values: " + strings.Join(possible, ", ") + "]"
	}
	return &UsageError{Message: message}
}

// pendingValueRequiredPlaceholderError is pendingValueRequiredError for a flag
// whose clap placeholder depends on the enclosing subcommand.
func pendingValueRequiredPlaceholderError(flag, placeholder string, args []string, i int) error {
	err := &UsageError{Message: "a value is required for '" + flag + " " + placeholder + "' but none was supplied"}
	if i+1 < len(args) {
		err.PendingFlag = args[i]
		err.PendingNext = args[i+1]
	}
	return err
}

// clapConfigOverrideArg matches clap's global `-c/--config` spelling at args[i]
// and returns the override value plus the index of the token that carried it.
// clap declares the argument global, so every subcommand accepts it.
func clapConfigOverrideArg(args []string, i int) (string, int, bool, error) {
	arg := args[i]
	switch {
	case arg == "-c" || arg == "--config":
		value, next, err := requireValue(args, i, arg)
		if err != nil {
			return "", i, true, err
		}
		return value, next, true, nil
	case strings.HasPrefix(arg, "--config="):
		return strings.TrimPrefix(arg, "--config="), i, true, nil
	case strings.HasPrefix(arg, "-c") && arg != "-C":
		return shortAttachedValue(arg, "-c"), i, true, nil
	}
	return "", i, false, nil
}

// shortAttachedValue returns a short option's attached value. clap drops a
// single "=" between a short flag and its value, so `-c=key=value` parses as
// `key=value` and `-c=` as an empty value.
func shortAttachedValue(arg, flag string) string {
	return strings.TrimPrefix(strings.TrimPrefix(arg, flag), "=")
}

// flagNameOnly is clap's rendering of an unexpected flag: the `=value` part of a
// long option is dropped.
func flagNameOnly(arg string) string {
	if !strings.HasPrefix(arg, "-") {
		return arg
	}
	if strings.HasPrefix(arg, "--") {
		if index := strings.IndexByte(arg, '='); index >= 0 {
			return arg[:index]
		}
		return arg
	}
	// clap names an unknown short flag by its first character only, so `-x1`
	// reports `-x`.
	if len(arg) > 2 {
		return arg[:2]
	}
	return arg
}

// clapLooksLikeShortFlag reports whether the token is a short-flag token, which
// clap diagnoses against the command's static usage page.
func clapLooksLikeShortFlag(arg string) bool {
	return len(arg) > 1 && arg[0] == '-' && arg[1] != '-'
}

// clapRepeatedOptionError replays clap's parse-time occurrence check: a flag
// declared with ArgAction::SetTrue fails on its second occurrence while clap
// parses, so that diagnostic beats a later undeclared flag but loses to an
// earlier one. Value options are only checked by the post-parse validator and
// go through clapRepeatedValueOptionError instead.
func clapRepeatedOptionError(command string, argv []string, stopAtPositional bool) error {
	seen := make(map[string]bool, len(argv))
	for i := 0; i < len(argv); i++ {
		arg := argv[i]
		if arg == "--" {
			break
		}
		if !clapLooksLikeOption(arg) {
			if stopAtPositional {
				// The root parser only owns the tokens before its prompt or the
				// subcommand name; everything after belongs to the child command.
				return nil
			}
			continue
		}
		if clapIsHelpOrVersionToken(arg) {
			// clap prints the help/version page as soon as it reaches the flag.
			return nil
		}
		if !clapCommandDeclaresOption(command, arg) {
			// The command parser reports this token with its own diagnostic;
			// every occurrence check clap runs after it is unreachable.
			return nil
		}
		token, consumesValue := clapUsageOptionToken(command, arg)
		if consumesValue {
			if i+1 >= len(argv) || clapLooksLikeOption(argv[i+1]) {
				// Missing or hyphen-prefixed values are parse-time errors that
				// beat the post-parse occurrence check.
				return nil
			}
			i++
			continue
		}
		if clapRepeatableUsageToken(command, token) {
			continue
		}
		if seen[token] {
			return clapRepeatedOptionUsageError(command, token)
		}
		seen[token] = true
	}
	return nil
}

// clapRepeatedValueOptionError replays clap's post-parse occurrence check for
// single-value options. The caller runs it after the command parser finished
// (or failed a validation check), so a parse-time diagnostic such as an invalid
// value still wins, exactly as in clap.
func clapRepeatedValueOptionError(command string, argv []string) error {
	seen := make(map[string]bool, len(argv))
	for i := 0; i < len(argv); i++ {
		arg := argv[i]
		if arg == "--" {
			break
		}
		if !clapLooksLikeOption(arg) {
			continue
		}
		if clapIsHelpOrVersionToken(arg) || !clapCommandDeclaresOption(command, arg) {
			return nil
		}
		token, consumesValue := clapUsageOptionToken(command, arg)
		if !consumesValue {
			continue
		}
		if i+1 >= len(argv) || clapLooksLikeOption(argv[i+1]) {
			return nil
		}
		i++
		if clapRepeatableUsageToken(command, token) {
			continue
		}
		if seen[token] {
			return clapRepeatedOptionUsageError(command, token)
		}
		seen[token] = true
	}
	return nil
}

func clapRepeatedOptionUsageError(command, token string) error {
	return &UsageError{
		Message: "the argument '" + token + "' cannot be used multiple times",
		Usage:   clapCommandUsageText(command),
	}
}

// clapRepeatableUsageToken reports the rendered tokens clap declares with a
// repeatable action: the shared overrides, the variadic value options and the
// per-command lists Apache-style appends (`--env`, `--scopes`, `--sparse`).
func clapRepeatableUsageToken(command, token string) bool {
	for _, prefix := range []string{"--config ", "--enable ", "--disable ", "--add-dir ", "--image "} {
		if strings.HasPrefix(token, prefix) {
			return true
		}
	}
	return clapRepeatableCommandTokens[command][token]
}

// clapRepeatableCommandTokens lists the repeatable options whose arity depends
// on the command that declares them.
var clapRepeatableCommandTokens = map[string]map[string]bool{
	"mcp add":                {"--env <KEY=VALUE>": true},
	"mcp login":              {"--scopes <SCOPE,SCOPE>": true},
	"plugin marketplace add": {"--sparse <PATH>": true},
}

// clapIsHelpOrVersionToken reports the flags clap resolves into a help or
// version page instead of a parsed argument.
func clapIsHelpOrVersionToken(arg string) bool {
	switch arg {
	case "--help", "-h", "--version", "-V":
		return true
	}
	return false
}

// clapCommandDeclaresOption reports whether the command declares the option
// spelling, long or short.
func clapCommandDeclaresOption(command, arg string) bool {
	if clapLooksLikeShortFlag(arg) {
		return clapCommandDeclaresShortFlag(command, arg)
	}
	return clapCommandDeclaresLongFlag(command, arg)
}

// clapCommandDeclaresLongFlag reports whether the command declares the long flag
// spelled in arg (inline `=value` is ignored, hidden aliases count).
func clapCommandDeclaresLongFlag(command, arg string) bool {
	if !strings.HasPrefix(arg, "--") {
		return false
	}
	name := strings.TrimPrefix(flagNameOnly(arg), "--")
	flags, ok := clapCommandLongFlags[command]
	if !ok {
		return false
	}
	for _, declared := range flags {
		if declared == name {
			return true
		}
		if primary, ok := clapLongFlagAliases[name]; ok && primary == declared {
			return true
		}
		if primary, ok := clapLongFlagAliases[declared]; ok && primary == name {
			return true
		}
	}
	return false
}

// clapCommandDeclaresShortFlag reports whether the command declares the short
// flag spelled in arg.
func clapCommandDeclaresShortFlag(command, arg string) bool {
	name := clapShortFlagName(arg)
	flags, ok := clapCommandShortFlags[command]
	if !ok {
		flags = clapDefaultShortFlags
	}
	for _, declared := range flags {
		if declared == name {
			return true
		}
	}
	return false
}

// clapShortFlagName is the spelling clap prints for a short-flag token.
func clapShortFlagName(arg string) string {
	return flagNameOnly(arg)
}

// clapUnexpectedShortFlagError renders clap's diagnostic for an unknown short
// flag: the static usage page (clap resolves short flags before it records the
// accepted options) and the `--` workaround tip when a positional exists.
func clapUnexpectedShortFlagError(command, arg string) error {
	name := clapShortFlagName(arg)
	message := "unexpected argument '" + name + "' found"
	if clapCommandHasPositionals(command) {
		message += "\n\n  tip: to pass '" + name + "' as a value, use '-- " + name + "'"
	}
	return &UsageError{Message: message, Usage: clapCommandUsageText(command)}
}

// unexpectedRootArgumentError mirrors clap's root diagnostic, including the
// did-you-mean tip and the `'exec --json' exists` tip for flags that only a
// later subcommand accepts.
func unexpectedRootArgumentError(arg string, remaining []string) error {
	name := flagNameOnly(arg)
	if suggestion, ok := clapSuggestLongFlag("", name); ok {
		return &UsageError{
			Message: "unexpected argument '" + name + "' found\n\n  tip: a similar argument exists: '--" + suggestion.matched + "'",
			Usage:   rootUsageText,
		}
	}
	tip := "  tip: to pass '" + name + "' as a value, use '-- " + name + "'"
	if owner, flag, ok := clapSubcommandLongFlagSuggestion(name, remaining); ok {
		tip = "  tip: '" + owner + " --" + flag + "' exists"
	}
	return &UsageError{
		Message: "unexpected argument '" + name + "' found\n\n" + tip,
		Usage:   rootUsageText,
	}
}

// clapSubcommandLongFlagSuggestion mirrors clap's second did-you-mean pass: when
// the root command has no similar flag, clap scores the unknown spelling against
// the long flags of every top-level subcommand and names the one whose name
// appears earliest in the remaining arguments.
func clapSubcommandLongFlagSuggestion(flag string, remaining []string) (string, string, bool) {
	for _, token := range remaining {
		if _, ok := clapCommandLongFlags[token]; !ok || token == "" {
			continue
		}
		if suggestion, ok := clapSuggestDeclaredLongFlag(token, flag); ok {
			return token, suggestion.matched, true
		}
	}
	return "", "", false
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
	"plugin add":                        {"Usage: codex plugin add [OPTIONS] <PLUGIN[@MARKETPLACE]>", true},
	"plugin list":                       {"Usage: codex plugin list [OPTIONS]", false},
	"plugin marketplace add":            {"Usage: codex plugin marketplace add [OPTIONS] <SOURCE>", true},
	"plugin remove":                     {"Usage: codex plugin remove [OPTIONS] <PLUGIN[@MARKETPLACE]>", true},
	"plugin marketplace list":           {"Usage: codex plugin marketplace list [OPTIONS]", false},
	"plugin marketplace upgrade":        {"Usage: codex plugin marketplace upgrade [OPTIONS] [MARKETPLACE_NAME]", true},
	"plugin marketplace remove":         {"Usage: codex plugin marketplace remove [OPTIONS] <MARKETPLACE_NAME>", true},
	"sandbox":                           {"Usage: codex sandbox [OPTIONS] [COMMAND]... [COMMAND]", true},
	"sandbox uninstall":                 {"Usage: codex sandbox uninstall [OPTIONS]", false},
	"cloud exec":                        {"Usage: codex cloud exec [OPTIONS] --env <ENV_ID> [QUERY]", true},
	"cloud status":                      {"Usage: codex cloud status [OPTIONS] <TASK_ID>", true},
	"cloud list":                        {"Usage: codex cloud list [OPTIONS]", false},
	"responses-api-proxy":               {"Usage: codex responses-api-proxy [OPTIONS]", false},
	"exec-server":                       {"Usage: codex exec-server [OPTIONS] [COMMAND]", false},
	"exec-server forward":               {"Usage: codex exec-server forward [OPTIONS] --remote <URL> --environment-id <ID> --connect <URL>", false},
	"app-server":                        {"Usage: codex app-server [OPTIONS] [COMMAND]", false},
	"app-server proxy":                  {"Usage: codex app-server proxy [OPTIONS]", false},
	"app-server daemon pid-update-loop": {"Usage: codex app-server daemon pid-update-loop [OPTIONS]", false},
	"app-server daemon bootstrap":       {"Usage: codex app-server daemon bootstrap [OPTIONS]", false},
	"app-server daemon update":          {"Usage: codex app-server daemon update [OPTIONS]", false},
	"app-server daemon start":           {"Usage: codex app-server daemon start [OPTIONS]", false},
	"app-server daemon stop":            {"Usage: codex app-server daemon stop [OPTIONS]", false},
	"app-server daemon restart":         {"Usage: codex app-server daemon restart [OPTIONS]", false},
	"app-server daemon version":         {"Usage: codex app-server daemon version [OPTIONS]", false},
	"app-server daemon enable-remote-control":  {"Usage: codex app-server daemon enable-remote-control [OPTIONS]", false},
	"app-server daemon disable-remote-control": {"Usage: codex app-server daemon disable-remote-control [OPTIONS]", false},
	"debug app-server send-message-v2":         {"Usage: codex debug app-server send-message-v2 [OPTIONS] <USER_MESSAGE>", true},
	"app-server generate-json-schema":          {"Usage: codex app-server generate-json-schema [OPTIONS] --out <DIR>", false},
	"app-server generate-internal-json-schema": {
		"Usage: codex app-server generate-internal-json-schema [OPTIONS] --out <DIR>", false,
	},
	"app-server generate-ts": {"Usage: codex app-server generate-ts [OPTIONS] --out <DIR>", false},
	"queue":                  {"Usage: codex queue [OPTIONS] --thread <THREAD> --message <TEXT>", false},
	"completion":             {"Usage: codex completion [OPTIONS] [SHELL]", true},
	"agents":                 {"Usage: codex agents [OPTIONS]", false},
	"app":                    {"Usage: codex app [OPTIONS] [PATH]", true},
	"update":                 {"Usage: codex update [OPTIONS]", false},
	"migrate-nfs-db":         {"Usage: codex migrate-nfs-db [OPTIONS]", false},
	"migrate-rollouts":       {"Usage: codex migrate-rollouts [OPTIONS]", false},
	"apply":                  {"Usage: codex apply [OPTIONS] <TASK_ID>", true},
	"remote-control":         {"Usage: codex remote-control [OPTIONS] [COMMAND]", false},
	"remote-control start":   {"Usage: codex remote-control start [OPTIONS]", false},
	"remote-control stop":    {"Usage: codex remote-control stop [OPTIONS]", false},
	"remote-control pair":    {"Usage: codex remote-control pair [OPTIONS]", false},
	"delete":                 {"Usage: codex delete [OPTIONS] <SESSION>", true},
	"resume":                 {"Usage: codex resume [OPTIONS] [SESSION_ID] [PROMPT]", true},
	"fork":                   {"Usage: codex fork [OPTIONS] [SESSION_ID] [PROMPT]", true},
	"archive":                {"Usage: codex archive [OPTIONS] <SESSION>", true},
	"unarchive":              {"Usage: codex unarchive [OPTIONS] <SESSION>", true},
	"debug models":           {"Usage: codex debug models [OPTIONS]", false},
	"debug prompt-input":     {"Usage: codex debug prompt-input [OPTIONS] [PROMPT]", true},
	"debug trace-reduce":     {"Usage: codex debug trace-reduce [OPTIONS] <TRACE_BUNDLE>", true},
	"debug clear-memories":   {"Usage: codex debug clear-memories [OPTIONS]", false},
	"features list":          {"Usage: codex features list [OPTIONS]", false},
	"features enable":        {"Usage: codex features enable [OPTIONS] <FEATURE>", true},
	"features disable":       {"Usage: codex features disable [OPTIONS] <FEATURE>", true},
	"features":               {"Usage: codex features [OPTIONS] <COMMAND>", false},
	"exec fork":              {"Usage: codex exec fork [OPTIONS] <SESSION_ID> [PROMPT]", true},
	"login status":           {"Usage: codex login status [OPTIONS]", false},
	"mcp":                    {"Usage: codex mcp [OPTIONS] <COMMAND>", false},
	"plugin":                 {"Usage: codex plugin [OPTIONS] <COMMAND>", false},
	"plugin marketplace":     {"Usage: codex plugin marketplace [OPTIONS] <COMMAND>", false},
	"debug":                  {"Usage: codex debug [OPTIONS] <COMMAND>", false},
	"debug app-server":       {"Usage: codex debug app-server [OPTIONS] <COMMAND>", false},
	"execpolicy":             {"Usage: codex execpolicy [OPTIONS] <COMMAND>", false},
	"doctor":                 {"Usage: codex doctor [OPTIONS]", false},
	"app-server daemon":      {"Usage: codex app-server daemon [OPTIONS] <COMMAND>", false},
	"cloud":                  {"Usage: codex cloud [OPTIONS] [COMMAND]", false},
	"cloud apply":            {"Usage: codex cloud apply [OPTIONS] <TASK_ID>", true},
	"cloud diff":             {"Usage: codex cloud diff [OPTIONS] <TASK_ID>", true},
}

// unexpectedSubcommandArgumentError mirrors clap for a subcommand: the `--`
// workaround tip only appears when the command declares positional arguments.
func unexpectedSubcommandArgumentError(command, arg string) error {
	spec, ok := subcommandUsageTexts[command]
	if !ok {
		return fmt.Errorf("unknown %s option %s", command, arg)
	}
	if clapLooksLikeShortFlag(arg) {
		return clapUnexpectedShortFlagError(command, arg)
	}
	name := flagNameOnly(arg)
	message := "unexpected argument '" + name + "' found"
	suggestion, hasSuggestion := clapLongFlagSuggestionFor(command, arg)
	if hasSuggestion {
		message += "\n\n  tip: a similar argument exists: '--" + suggestion.matched + "'"
	} else if spec.hasPositional {
		message += "\n\n  tip: to pass '" + name + "' as a value, use '-- " + name + "'"
	}
	if hasSuggestion && clapTrailingPositionalCommands[command] {
		// clap still prints the `--` workaround for commands whose positional
		// captures trailing values (`-- <COMMAND>...`) and stacks both tips.
		message += "\n  tip: to pass '" + name + "' as a value, use '-- " + name + "'"
	}
	usageErr := &UsageError{Message: message, Usage: spec.usage, SmartUsage: true}
	if hasSuggestion {
		usageErr.Suggested = clapLongFlagUsageToken(command, suggestion.primary)
	}
	return usageErr
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
	// Suggested is the usage token of the flag clap suggests for an unknown
	// argument. clap marks the suggestion present, so the usage block echoes it
	// next to the options the parser already consumed.
	Suggested string
	// Raw marks diagnostics raised through clap::Error::raw, which print the
	// message alone: no usage block and no trailing `--help` hint.
	Raw bool
	// PendingFlag and PendingNext describe "a value is required for ..." raised
	// because the following token looked like an option. clap resolves that
	// token first, so an undeclared flag wins over the pending value; the
	// subcommand decorator re-checks it against the leaf command's tables.
	PendingFlag string
	PendingNext string
}

func (e *UsageError) Error() string { return e.Message }

// sandboxModeValues are the `--sandbox` values Rust's SandboxModeCliArg accepts
// (codex-rs/utils/cli/src/sandbox_mode_cli_arg.rs).
var sandboxModeValues = []string{"read-only", "workspace-write", "danger-full-access"}

// colorValues are the `--color` values Rust's ColorCliArg accepts.
var colorValues = []string{"always", "never", "auto"}

// cyberAccessProgramValues are the `--cyber-access-program` values Rust's
// CyberAccessProgramCliArg accepts.
var cyberAccessProgramValues = []string{"standard", "daybreak_blue", "daybreak_red"}

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
	bestScore := 0.0
	for _, candidate := range possible {
		if candidate == value {
			return ""
		}
		// clap's did_you_mean keeps candidates whose Jaro-Winkler confidence
		// clears 0.8 and prints the highest scoring one.
		score := jaroWinklerSimilarity(value, candidate)
		if score <= 0.8 || (best != "" && score <= bestScore) {
			continue
		}
		best, bestScore = candidate, score
	}
	if best == "" {
		return ""
	}
	return "\n\n  tip: a similar value exists: '" + best + "'"
}

// jaroWinklerSimilarity is strsim::jaro_winkler: Jaro with the common-prefix
// bonus clap applies when it looks for a likely misspelling.
func jaroWinklerSimilarity(a, b string) float64 {
	jaro := jaroSimilarity(a, b)
	prefix := 0
	arunes := []rune(a)
	brunes := []rune(b)
	for prefix < len(arunes) && prefix < len(brunes) && prefix < 4 && arunes[prefix] == brunes[prefix] {
		prefix++
	}
	return jaro + 0.1*float64(prefix)*(1-jaro)
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
func decorateSubcommandClapUsage(command Command, err error, args []string) error {
	usageErr, ok := err.(*UsageError)
	if !ok {
		return err
	}
	if usageErr.PendingNext != "" {
		if replacement, ok := clapPendingValueError(string(command), args, usageErr.PendingFlag, usageErr.PendingNext); ok {
			return replacement
		}
	}
	if usageErr.Usage == "" || !usageErr.SmartUsage {
		return err
	}
	stopAt := ""
	switch {
	case strings.HasPrefix(usageErr.Message, "unexpected argument '"):
		stopAt, _, _ = strings.Cut(strings.TrimPrefix(usageErr.Message, "unexpected argument '"), "'")
		if !clapLooksLikeOption(stopAt) && string(command) != "login" {
			// clap matches the stray positional with an empty "used" set, so the
			// diagnostic keeps the plain [OPTIONS] block. `login` renders its
			// required group member instead and is handled below.
			return err
		}
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
	tokens := clapUsageOptionTokens(string(command), args, stopAt)
	if clapUsageTokensRepeat(clapEffectiveCommand(string(command), args), tokens) {
		// clap cannot echo a single-value argument the caller repeated, so the
		// diagnostic keeps the plain usage block instead of the smart one.
		return err
	}
	used := clapDedupeUsageTokens(tokens)
	if usageErr.Suggested != "" {
		used = append(used, usageErr.Suggested)
	}
	leaf := clapEffectiveCommand(string(command), args)
	if leaf == "agents" {
		// Rust leaves --no-daemon out of the smart usage: the overview pulls it
		// from the shared root parser, so clap never marks it used here.
		used = clapRemoveUsageToken(used, "--no-daemon")
	}
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
	usageErr.Usage = usageWithUsedOptions(usageErr.Usage, used, leaf)
	return err
}

// clapRemoveUsageToken drops every occurrence of token from the echoed usage
// list, keeping the remaining order.
func clapRemoveUsageToken(tokens []string, token string) []string {
	out := tokens[:0]
	for _, candidate := range tokens {
		if candidate != token {
			out = append(out, candidate)
		}
	}
	return out
}

// clapPendingValueError re-reads a pending-value diagnostic the way clap does:
// it resolves the token that follows the value-less option first, so `-c -x1`
// reports `unexpected argument '-x' found` and `-c --bogus` reports the long
// flag, while a declared flag (`-c --json`) keeps the missing-value message.
func clapPendingValueError(command string, args []string, pendingFlag, next string) (error, bool) {
	at := -1
	for i := 0; i+1 < len(args); i++ {
		if args[i] == pendingFlag && args[i+1] == next {
			at = i
			break
		}
	}
	if at < 0 {
		return nil, false
	}
	leaf, prefix := clapLeafCommandFor(command, args)
	if leaf == "" && command != "" {
		return nil, false
	}
	if prefix > at {
		return nil, false
	}
	if clapLooksLikeShortFlag(next) {
		if clapCommandDeclaresShortFlag(leaf, next) {
			return nil, false
		}
		return clapUnexpectedShortFlagError(leaf, next), true
	}
	if !strings.HasPrefix(next, "--") {
		return nil, false
	}
	if clapCommandDeclaresLongFlag(leaf, next) {
		return nil, false
	}
	return clapUnexpectedLongFlagError(leaf, next, args[prefix:at]), true
}

// decorateRootPendingValueError applies the same lookahead to the root command,
// which does not go through the subcommand decorator.
func decorateRootPendingValueError(err error, args []string) error {
	usageErr, ok := err.(*UsageError)
	if !ok || usageErr.PendingNext == "" {
		return err
	}
	if replacement, ok := clapPendingValueError("", args, usageErr.PendingFlag, usageErr.PendingNext); ok {
		return replacement
	}
	return err
}

// clapEffectiveCommand resolves the command path the flag tables are keyed by
// from the argv tokens that name the nested subcommand, so `app-server daemon
// update -y` resolves past the bare `app-server` entry. It keeps the given
// command when no nested path matches.
func clapEffectiveCommand(command string, args []string) string {
	if leaf, _ := clapLeafCommandFor(command, args); leaf != "" {
		return leaf
	}
	return command
}

// clapLeafCommandFor resolves the command path the flag tables are keyed by and
// the number of argv tokens that name it. The longest matching key wins, so
// `plugin marketplace add` resolves past the bare `plugin` entry.
func clapLeafCommandFor(command string, args []string) (string, int) {
	best, bestLen := command, 0
	for key := range clapCommandLongFlags {
		if command == "" {
			if key == "" {
				continue
			}
			continue
		}
		if !strings.HasPrefix(key, command+" ") {
			continue
		}
		tokens := strings.Split(key, " ")
		named := len(tokens) - 1
		if named > len(args) || named <= bestLen {
			continue
		}
		matches := true
		for i := 0; i < named; i++ {
			if args[i] != tokens[i+1] {
				matches = false
				break
			}
		}
		if matches {
			best, bestLen = key, named
		}
	}
	if _, ok := clapCommandLongFlags[best]; !ok {
		return "", 0
	}
	return best, bestLen
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

// reviewUsageConflicts mirrors the conflicts_with_all table Rust declares on
// ReviewArgs (exec/src/cli.rs): the scope flags and PROMPT exclude each other.
var reviewUsageConflicts = map[string][]string{
	"--uncommitted":   {"--base <BRANCH>", "--commit <SHA>", "[PROMPT]"},
	"--base <BRANCH>": {"--uncommitted", "--commit <SHA>", "[PROMPT]"},
	"--commit <SHA>":  {"--uncommitted", "--base <BRANCH>", "[PROMPT]"},
	"[PROMPT]":        {"--uncommitted", "--base <BRANCH>", "--commit <SHA>"},
}

// reviewUsageKeys lists the review-scope arguments in clap's declaration order
// together with the usage token each one renders as.
var reviewUsageKeys = []struct {
	key   string
	token string
}{
	{"uncommitted", "--uncommitted"},
	{"base", "--base <BRANCH>"},
	{"commit", "--commit <SHA>"},
	{"prompt", "[PROMPT]"},
}

// reviewClapValidation mirrors clap's post-parse validation for `codex review`
// and `codex exec review`: single-use arguments reject a second occurrence,
// the scope flags conflict with each other and with PROMPT, and --title
// requires --commit unless a conflicting argument was supplied.
func reviewClapValidation(command string, args []string, positions map[string]int) error {
	used := clapUsageOptionTokens(command, args, "")
	counts := make(map[string]int, len(used))
	for _, token := range used {
		if reviewUsageTokenRepeatable(token) {
			continue
		}
		counts[token]++
		if counts[token] == 2 {
			return &UsageError{
				Message: "the argument '" + token + "' cannot be used multiple times",
				Usage:   clapCommandUsageText(command),
			}
		}
	}

	present := make([]struct {
		token string
		index int
	}, 0, len(reviewUsageKeys))
	for _, spec := range reviewUsageKeys {
		if index, ok := positions[spec.key]; ok {
			present = append(present, struct {
				token string
				index int
			}{spec.token, index})
		}
	}
	sort.Slice(present, func(i, j int) bool { return present[i].index < present[j].index })

	_, titlePresent := positions["title"]
	for _, candidate := range present {
		conflicting := make([]string, 0, 3)
		excluded := make(map[string]bool, 3)
		for _, other := range present {
			if other.token == candidate.token {
				continue
			}
			if !clapReviewConflicts(candidate.token, other.token) {
				continue
			}
			conflicting = append(conflicting, other.token)
			excluded[other.token] = true
		}
		if len(conflicting) == 0 {
			continue
		}
		message := "the argument '" + candidate.token + "' cannot be used with '" + conflicting[0] + "'"
		if len(conflicting) > 1 {
			message = "the argument '" + candidate.token + "' cannot be used with:\n  " + strings.Join(conflicting, "\n  ")
		}
		return &UsageError{Message: message, Usage: reviewClapUsage(command, used, excluded, titlePresent)}
	}

	if titlePresent && !clapReviewRequirementBlocked(positions) {
		return &UsageError{
			Message: "the following required arguments were not provided:\n  --commit <SHA>",
			Usage:   reviewClapUsage(command, used, nil, true),
		}
	}
	return nil
}

func clapReviewConflicts(token, other string) bool {
	for _, conflict := range reviewUsageConflicts[token] {
		if conflict == other {
			return true
		}
	}
	return false
}

// clapReviewRequirementBlocked reports whether --commit conflicts with an
// argument the user supplied. clap stops enforcing a requirement when the
// required argument cannot be present.
func clapReviewRequirementBlocked(positions map[string]int) bool {
	for _, key := range []string{"uncommitted", "base", "commit", "prompt"} {
		if _, ok := positions[key]; ok {
			return true
		}
	}
	return false
}

// reviewClapUsage renders clap's smart usage for a review diagnostic: derived
// requirements first, then the arguments argv supplied in order, then PROMPT.
func reviewClapUsage(command string, used []string, excluded map[string]bool, requireCommit bool) string {
	fields := []string{"Usage: codex"}
	if command == "exec review" {
		fields = append(fields, "exec")
	}
	fields = append(fields, "review")
	seen := make(map[string]bool, len(used)+1)
	kept := make([]string, 0, len(used))
	for _, token := range used {
		if seen[token] {
			continue
		}
		seen[token] = true
		if excluded[token] {
			continue
		}
		kept = append(kept, token)
	}
	if requireCommit && !seen["--commit <SHA>"] {
		fields = append(fields, "--commit <SHA>")
	}
	fields = append(fields, kept...)
	fields = append(fields, "[PROMPT]")
	return strings.Join(fields, " ")
}

// reviewUsageTokenRepeatable reports the review arguments clap lets a caller
// repeat; every other option rejects a second occurrence.
func reviewUsageTokenRepeatable(token string) bool {
	switch token {
	case "--config <key=value>", "--enable <FEATURE>", "--disable <FEATURE>":
		return true
	}
	return false
}

// clapUsageOptionUsed reports whether consumed already carries token.
func clapUsageOptionUsed(command string, consumed []string, token string) bool {
	for _, used := range clapUsageOptionTokens(command, consumed, "") {
		if used == token {
			return true
		}
	}
	return false
}

// clapUsageTokensRepeat reports whether argv repeated one of the review
// arguments clap only accepts once, in which case the diagnostic cannot echo
// the options into its usage block.
func clapUsageTokensRepeat(command string, used []string) bool {
	seen := make(map[string]bool, len(used))
	for _, token := range used {
		if clapRepeatableUsageToken(command, token) {
			continue
		}
		if seen[token] {
			return true
		}
		seen[token] = true
	}
	return false
}

// clapDedupeUsageTokens keeps the first occurrence of every rendered option:
// clap echoes each argument id once in its usage block, even for the actions it
// accepts more than once.
func clapDedupeUsageTokens(tokens []string) []string {
	seen := make(map[string]bool, len(tokens))
	kept := tokens[:0]
	for _, token := range tokens {
		if seen[token] {
			continue
		}
		seen[token] = true
		kept = append(kept, token)
	}
	return kept
}

// clapUsageOptionTokens renders the options clap accepted, stopping in front of
// the token that produced the diagnostic. Option values are folded into the
// flag token so `--enable x` renders as `--enable <FEATURE>`.
func clapUsageOptionTokens(command string, args []string, stopAt string) []string {
	command = clapEffectiveCommand(command, args)
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
		token, consumedValue := clapUsageOptionToken(command, arg)
		used = append(used, token)
		if consumedValue {
			i++
		}
	}
	return used
}

// clapUsageOptionToken renders one option the way clap's usage block does and
// reports whether the following argv token was its value. Long spellings go
// through clapLongFlagUsageToken so the per-command placeholder overrides apply
// to both spellings: `sandbox -p x` and `sandbox --profile x` both echo
// `--profile <CONFIG_PROFILE>`.
func clapUsageOptionToken(command, arg string) (string, bool) {
	if strings.Contains(arg, " ") {
		// Already rendered by clapLongFlagUsageToken: placeholders may carry an
		// "=" (`--config <key=value>`), which is not an inline value here.
		return arg, false
	}
	name, hasInlineValue := arg, false
	if trimmed, _, ok := strings.Cut(arg, "="); ok {
		name, hasInlineValue = trimmed, true
	}
	if strings.HasPrefix(name, "--") {
		token := clapLongFlagUsageToken(command, strings.TrimPrefix(name, "--"))
		// Only a flag whose rendering carries a placeholder eats the next
		// token; a boolean flag leaves the following option alone.
		return token, !hasInlineValue && strings.Contains(token, " ")
	}
	if spec, known := clapCommandShortFlagLongs[command][name]; known {
		if spec.placeholder == "" {
			return spec.long, false
		}
		return clapLongFlagUsageToken(command, strings.TrimPrefix(spec.long, "--")), !hasInlineValue
	}
	if spec, known := valueOptionSpecs[name]; known {
		return clapLongFlagUsageToken(command, strings.TrimPrefix(spec.long, "--")), !hasInlineValue
	}
	if placeholder, known := subcommandValuePlaceholders[name]; known {
		return name + " " + placeholder, !hasInlineValue
	}
	return name, false
}

// reorderUsageOptionRun rebuilds the option run a usage block already lists so
// the options keep clap's argv order: `exec resume --enable f --last --bogus`
// prints `--enable <FEATURE> --last` even though the subcommand parser only saw
// the slice left after clap's global --enable was collected. It only fires when
// every option in the run is one of the used tokens, so a block that lists
// required options argv never repeated stays untouched.
func reorderUsageOptionRun(usage string, used, missing []string) (string, bool) {
	fields := strings.Fields(usage)
	start := -1
	for i, field := range fields {
		if strings.HasPrefix(field, "-") && field != "--" {
			start = i
			break
		}
	}
	if start < 0 {
		return usage, false
	}
	usedSet := make(map[string]bool, len(used))
	for _, token := range used {
		usedSet[token] = true
	}
	runTokens := make([]string, 0, 4)
	end := start
	for end < len(fields) {
		field := fields[end]
		if !strings.HasPrefix(field, "-") || field == "--" {
			break
		}
		token := field
		end++
		if end < len(fields) && strings.HasPrefix(fields[end], "<") {
			token += " " + fields[end]
			end++
		}
		if !usedSet[token] {
			return usage, false
		}
		runTokens = append(runTokens, token)
	}
	if len(runTokens) == 0 {
		return usage, false
	}
	keep := make(map[string]bool, len(runTokens)+len(missing))
	for _, token := range runTokens {
		keep[token] = true
	}
	for _, token := range missing {
		keep[token] = true
	}
	rebuilt := make([]string, 0, len(used))
	seen := make(map[string]bool, len(used))
	for _, token := range used {
		if !keep[token] || seen[token] {
			continue
		}
		seen[token] = true
		rebuilt = append(rebuilt, token)
	}
	for _, token := range runTokens {
		if !seen[token] {
			return usage, false
		}
	}
	out := make([]string, 0, len(fields))
	out = append(out, fields[:start]...)
	out = append(out, rebuilt...)
	out = append(out, fields[end:]...)
	return strings.Join(out, " "), true
}

// usageWithUsedOptions inserts clap's echoed options into a usage block: the
// [OPTIONS] placeholder is replaced when present, otherwise the options go in
// front of the first positional (or at the end for option-only commands).
func usageWithUsedOptions(usage string, used []string, command string) string {
	// clap renders the optional subcommand placeholder only in the static page:
	// its smart usage lists the used arguments and the required ones.
	if fields := strings.Fields(usage); len(fields) > 0 {
		kept := fields[:0]
		for _, field := range fields {
			if field == "[COMMAND]" {
				continue
			}
			kept = append(kept, field)
		}
		usage = strings.Join(kept, " ")
	}
	missing := make([]string, 0, len(used))
	for _, token := range used {
		if !strings.Contains(usage, token) {
			missing = append(missing, token)
		}
	}
	if reordered, ok := reorderUsageOptionRun(usage, used, missing); ok {
		usage = reordered
	}
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
		if len(used) == 0 {
			return usage
		}
		// clap prints [OPTIONS] only while nothing is marked present: a used
		// option the block already lists (a required one) still drops it.
		fields := strings.Fields(usage)
		kept := fields[:0]
		for _, field := range fields {
			if field == "[OPTIONS]" {
				continue
			}
			kept = append(kept, field)
		}
		return strings.Join(kept, " ")
	}
	joined := strings.Join(echoed, " ")
	if strings.Contains(usage, "[OPTIONS]") && !clapOptionsPrecedesRequired(usage) {
		return strings.Replace(usage, "[OPTIONS]", joined, 1)
	}
	if strings.Contains(usage, "[OPTIONS]") {
		// clap writes the requirements first and the used options after them,
		// so a static page whose `[OPTIONS]` sits in front of the required
		// options drops it and appends the echo behind them.
		usage = strings.Replace(usage, "[OPTIONS] ", "", 1)
	}
	fields := strings.Fields(usage)
	for idx, field := range fields {
		if !strings.HasPrefix(field, "<") && !strings.HasPrefix(field, "[") {
			continue
		}
		// A placeholder that renders an option's value (`--thread <THREAD>`) is
		// part of the option, not a positional, so keep looking.
		if idx > 0 && strings.HasPrefix(fields[idx-1], "-") && fields[idx-1] != "--" {
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

// clapOptionsPrecedesRequired reports whether a usage block lists `[OPTIONS]` in
// front of required options, which clap's smart usage renders in the opposite
// order (requirements first, then the used options).
func clapOptionsPrecedesRequired(usage string) bool {
	fields := strings.Fields(usage)
	for i, field := range fields {
		if field != "[OPTIONS]" {
			continue
		}
		return i+1 < len(fields) && strings.HasPrefix(fields[i+1], "-")
	}
	return false
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

// clapCommandPositionalSlots lists the positional slots clap binds for the
// session subcommands (`codex resume`, `codex fork`, `codex archive`,
// `codex unarchive`, `codex delete`) and the exec family, in declaration order:
// clap renders bound positionals with angle brackets in every smart usage block.
var clapCommandPositionalSlots = map[string][]sessionPositionalSlot{
	"resume":      {{name: "SESSION_ID"}, {name: "PROMPT"}},
	"fork":        {{name: "SESSION_ID"}, {name: "PROMPT"}},
	"archive":     {{name: "SESSION", required: true}},
	"unarchive":   {{name: "SESSION", required: true}},
	"delete":      {{name: "SESSION", required: true}},
	"review":      {{name: "PROMPT"}},
	"exec resume": {{name: "SESSION_ID"}, {name: "PROMPT"}},
	"exec fork":   {{name: "SESSION_ID", required: true}, {name: "PROMPT"}},
	"exec review": {{name: "PROMPT"}},
	// Group subcommands with a single positional: clap still renders the slot
	// in smart usage once the value is bound.
	"features enable":                  {{name: "FEATURE", required: true}},
	"features disable":                 {{name: "FEATURE", required: true}},
	"plugin add":                       {{name: "PLUGIN[@MARKETPLACE]", required: true}},
	"plugin remove":                    {{name: "PLUGIN[@MARKETPLACE]", required: true}},
	"debug app-server send-message-v2": {{name: "USER_MESSAGE", required: true}},
}

// clapCommandLongFlags lists the long options each command declares, in
// declaration order, taken from `codex <command> --help`. Aliases sit next to
// the argument they alias and are resolved through clapLongFlagAliases.
var clapCommandLongFlags = map[string][]string{
	"": {
		"config", "enable", "disable", "remote", "remote-auth-token-env", "strict-config",
		"image", "model", "oss", "local-provider", "profile", "sandbox",
		"approve-for-me", "not-so-yolo", "dangerously-bypass-approvals-and-sandbox", "yolo",
		"dangerously-bypass-hook-trust", "cd", "worktree", "add-dir", "ask-for-approval",
		"search", "no-alt-screen", "no-daemon", "help", "version",
	},
	"exec": {
		"config", "enable", "disable", "strict-config", "image", "model", "oss",
		"local-provider", "profile", "sandbox", "approve-for-me", "not-so-yolo",
		"dangerously-bypass-approvals-and-sandbox", "yolo", "dangerously-bypass-hook-trust",
		"cd", "worktree", "add-dir", "thread-source", "cyber-access-program",
		"skip-git-repo-check", "ephemeral", "ignore-user-config", "ignore-rules",
		"output-schema", "color", "json", "experimental-json", "output-last-message",
		"help", "version",
	},
	"exec resume": {
		"config", "last", "all", "enable", "disable", "image", "strict-config", "model",
		"dangerously-bypass-approvals-and-sandbox", "yolo", "dangerously-bypass-hook-trust",
		"worktree", "thread-source", "cyber-access-program", "skip-git-repo-check",
		"ephemeral", "ignore-user-config", "ignore-rules", "output-schema", "json",
		"experimental-json", "output-last-message", "help",
	},
	"exec fork": {
		"config", "image", "enable", "disable", "strict-config", "model",
		"dangerously-bypass-approvals-and-sandbox", "yolo", "dangerously-bypass-hook-trust",
		"worktree", "thread-source", "cyber-access-program", "skip-git-repo-check",
		"ephemeral", "ignore-user-config", "ignore-rules", "output-schema", "json",
		"experimental-json", "output-last-message", "help",
	},
	"exec review": {
		"config", "uncommitted", "base", "enable", "commit", "disable", "strict-config",
		"title", "model", "dangerously-bypass-approvals-and-sandbox", "yolo",
		"dangerously-bypass-hook-trust", "worktree", "thread-source",
		"cyber-access-program", "skip-git-repo-check", "ephemeral", "ignore-user-config",
		"ignore-rules", "output-schema", "json", "experimental-json",
		"output-last-message", "help",
	},
	"resume": {
		"config", "last", "all", "enable", "disable", "include-non-interactive", "remote",
		"remote-auth-token-env", "strict-config", "image", "model", "oss",
		"local-provider", "profile", "sandbox", "approve-for-me", "not-so-yolo",
		"dangerously-bypass-approvals-and-sandbox", "yolo", "dangerously-bypass-hook-trust",
		"cd", "worktree", "add-dir", "ask-for-approval", "search", "no-alt-screen",
		"no-daemon", "help", "version",
	},
	"fork": {
		"config", "last", "all", "enable", "disable", "remote", "remote-auth-token-env",
		"strict-config", "image", "model", "oss", "local-provider", "profile", "sandbox",
		"approve-for-me", "not-so-yolo", "dangerously-bypass-approvals-and-sandbox", "yolo",
		"dangerously-bypass-hook-trust", "cd", "worktree", "add-dir", "ask-for-approval",
		"search", "no-alt-screen", "no-daemon", "help", "version",
	},
	"archive":   clapSessionMutationLongFlags,
	"unarchive": clapSessionMutationLongFlags,
	"delete":    append(append([]string{}, clapSessionMutationLongFlags...), "force"),
	"review":    {"strict-config", "uncommitted", "base", "commit", "title", "help"},
	"agents": {
		"config", "enable", "remote", "disable", "remote-auth-token-env", "cd",
		"no-alt-screen", "no-daemon", "help",
	},
	"app": {
		"config", "download-url", "enable", "disable", "help",
	},
	"app-server": {
		"config", "startup-sign-out", "enable", "print-startup-config", "disable",
		"startup-client-info", "cloud-skills-snapshot", "code-mode-host", "strict-config",
		"listen", "stdio", "analytics-default-enabled", "ws-auth", "ws-token-file",
		"ws-token-sha256", "ws-shared-secret-file", "ws-issuer", "ws-audience",
		"ws-max-clock-skew-seconds", "help",
	},
	"app-server daemon": {
		"config", "enable", "disable", "help",
	},
	"app-server generate-json-schema": {
		"config", "out", "enable", "experimental", "disable", "help",
	},
	"app-server generate-ts": {
		"config", "out", "enable", "prettier", "disable", "experimental", "help",
	},
	"app-server daemon bootstrap": {
		"config", "remote-control", "enable", "disable", "help",
	},
	"app-server daemon disable-remote-control": {
		"config", "enable", "disable", "help",
	},
	"app-server daemon enable-remote-control": {
		"config", "enable", "disable", "help",
	},
	"app-server daemon restart": {
		"config", "enable", "disable", "help",
	},
	"app-server daemon start": {
		"config", "enable", "disable", "help",
	},
	"app-server daemon stop": {
		"config", "enable", "disable", "help",
	},
	"app-server daemon update": {
		"config", "from-cli", "enable", "yes", "disable", "help",
	},
	"app-server daemon version": {
		"config", "enable", "disable", "help",
	},
	"app-server proxy": {
		"config", "sock", "enable", "disable", "help",
	},
	"apply": {
		"config", "enable", "disable", "help",
	},
	"cloud": {
		"config", "enable", "disable", "help", "version",
	},
	"cloud apply": {
		"attempt", "config", "enable", "disable", "help",
	},
	"cloud diff": {
		"attempt", "config", "enable", "disable", "help",
	},
	"cloud exec": {
		"config", "env", "attempts", "enable", "branch", "disable", "help",
	},
	"cloud list": {
		"config", "env", "enable", "limit", "cursor", "disable", "json", "help",
	},
	"cloud status": {
		"config", "enable", "disable", "help",
	},
	"completion": {
		"config", "enable", "disable", "help",
	},
	"debug": {
		"config", "enable", "disable", "help",
	},
	"debug app-server send-message-v2": {
		"config", "enable", "disable", "help",
	},
	"debug app-server": {
		"config", "enable", "disable", "help",
	},
	"debug models": {
		"bundled", "config", "enable", "disable", "help",
	},
	"debug prompt-input": {
		"config", "image", "enable", "disable", "help",
	},
	"doctor": {
		"config", "enable", "json", "disable", "network-only", "summary", "all", "no-color",
		"ascii", "help",
	},
	"exec-server": {
		"config", "enable", "strict-config", "disable", "linux-sandbox-pid-namespace",
		"proxy-private-ips-via-upstream", "concurrent-requests", "listen", "ws-auth",
		"ws-token-file", "ws-token-sha256", "ws-shared-secret-file", "ws-issuer",
		"ws-audience", "ws-max-clock-skew-seconds", "remote", "remote-transport",
		"environment-id", "name", "use-agent-identity-auth", "executor-credential-stdin",
		"aws-sigv4", "aws-profile", "aws-region", "aws-service", "exit-on-stdin-close",
		"help",
	},
	"exec-server forward": {
		"config", "connect", "enable", "strict-config", "disable",
		"linux-sandbox-pid-namespace", "proxy-private-ips-via-upstream", "remote",
		"remote-transport", "environment-id", "name", "use-agent-identity-auth",
		"executor-credential-stdin", "aws-sigv4", "aws-profile", "aws-region", "aws-service",
		"exit-on-stdin-close", "help",
	},
	"features": {
		"config", "enable", "disable", "help",
	},
	"features disable": {
		"config", "enable", "disable", "help",
	},
	"features enable": {
		"config", "enable", "disable", "help",
	},
	"features list": {
		"config", "enable", "disable", "help",
	},
	"login": {
		"config", "with-api-key", "api-key", "enable", "with-access-token", "disable",
		"device-auth", "help",
	},
	"login status": {
		"config", "enable", "disable", "help",
	},
	"logout": {
		"config", "enable", "disable", "help",
	},
	"mcp": {
		"config", "enable", "disable", "help",
	},
	"mcp add": {
		"config", "env", "enable", "url", "bearer-token-env-var", "disable",
		"oauth-client-id", "oauth-client-secret", "oauth-client-registration",
		"oauth-resource", "help",
	},
	"mcp get": {
		"config", "json", "enable", "disable", "help",
	},
	"mcp list": {
		"config", "json", "enable", "disable", "help",
	},
	"mcp login": {
		"config", "no-browser", "enable", "scopes", "disable", "oauth-client-registration",
		"help",
	},
	"mcp logout": {
		"config", "enable", "disable", "help",
	},
	"mcp remove": {
		"config", "enable", "disable", "help",
	},
	"migrate-nfs-db": {
		"config", "staging-dir", "enable", "disable", "help",
	},
	"migrate-rollouts": {
		"apply", "config", "enable", "thread", "disable", "max-mib-per-second", "json",
		"verbose", "help",
	},
	"plugin": {
		"config", "enable", "disable", "help",
	},
	"plugin add": {
		"config", "marketplace", "enable", "json", "disable", "help",
	},
	"plugin list": {
		"config", "marketplace", "enable", "json", "available", "disable", "help",
	},
	"plugin marketplace add": {
		"config", "ref", "enable", "sparse", "disable", "json", "help",
	},
	"plugin marketplace list": {
		"config", "json", "enable", "disable", "help",
	},
	"plugin marketplace remove": {
		"config", "json", "enable", "disable", "help",
	},
	"plugin marketplace upgrade": {
		"config", "json", "enable", "disable", "help",
	},
	"plugin marketplace": {
		"config", "enable", "disable", "help",
	},
	"plugin remove": {
		"config", "marketplace", "enable", "json", "disable", "help",
	},
	"queue": {
		"thread", "enable", "message", "disable", "resume", "remote",
		"remote-auth-token-env", "profile", "strict-config", "config", "help", "cd",
	},
	"remote-control": {
		"config", "json", "enable", "no-daemon", "disable", "help",
	},
	"remote-control pair": {
		"config", "json", "enable", "disable", "help",
	},
	"remote-control start": {
		"config", "json", "enable", "disable", "help",
	},
	"remote-control stop": {
		"config", "json", "enable", "disable", "help",
	},
	"sandbox": {
		"config", "sandbox-state-json", "enable", "sandbox-state-readable-root", "disable",
		"sandbox-state-disable-network", "permission-profile", "profile", "cd",
		"include-managed-config", "help",
	},
	"sandbox uninstall": {
		"config", "enable", "disable", "help",
	},
	"update": {
		"config", "enable", "disable", "help",
	},
}

// clapSessionMutationLongFlags are the shared options `codex archive`,
// `unarchive` and `delete` flatten; `delete` adds --force.
var clapSessionMutationLongFlags = []string{
	"remote", "enable", "remote-auth-token-env", "disable", "image", "model", "oss",
	"local-provider", "profile", "sandbox", "approve-for-me", "not-so-yolo",
	"dangerously-bypass-approvals-and-sandbox", "yolo", "dangerously-bypass-hook-trust",
	"cd", "worktree", "add-dir", "strict-config", "config", "help",
}

// clapLongFlagAliases maps an alias clap declares to the long name it echoes in
// a smart usage block; the tip itself keeps the alias spelling.
var clapLongFlagAliases = map[string]string{
	"experimental-json": "json",
	"not-so-yolo":       "approve-for-me",
	"yolo":              "dangerously-bypass-approvals-and-sandbox",
}

// clapLongFlagPlaceholderOverrides holds the placeholders that depend on the
// command: `--image` is num_args = 1.. at the root and on the session commands
// but takes a single value inside `codex exec resume`/`fork`.
var clapLongFlagPlaceholderOverrides = map[string]map[string]string{
	"exec resume": {"image": "<FILE>"},
	"exec fork":   {"image": "<FILE>"},
	"mcp add":     {"env": "<KEY=VALUE>"},
	"sandbox": {
		"profile":                     "<CONFIG_PROFILE>",
		"permission-profile":          "<NAME>",
		"sandbox-state-readable-root": "<SANDBOX_STATE_READABLE_ROOT>",
	},
	"app-server": {
		"ws-auth": "<MODE>",
	},
	"exec-server": {
		"remote":                      "<URL>",
		"ws-auth":                     "<MODE>",
		"aws-profile":                 "<PROFILE>",
		"aws-region":                  "<REGION>",
		"aws-service":                 "<SERVICE>",
		"concurrent-requests":         "<COUNT>",
		"linux-sandbox-pid-namespace": "<MODE>",
	},
	"exec-server forward": {
		"remote":                      "<URL>",
		"ws-auth":                     "<MODE>",
		"aws-profile":                 "<PROFILE>",
		"aws-region":                  "<REGION>",
		"aws-service":                 "<SERVICE>",
		"concurrent-requests":         "<COUNT>",
		"linux-sandbox-pid-namespace": "<MODE>",
	},
	"migrate-rollouts": {"thread": "<THREAD_ID>"},
}

// clapCommandShortFlags lists the short aliases each command declares,
// taken from `codex <command> --help`. Commands that only carry the global
// -c/--config and -h/--help are omitted; clapCommandDeclaresShortFlag falls
// back to clapDefaultShortFlags for them.
var clapCommandShortFlags = map[string][]string{
	"":                                {"-c", "-i", "-m", "-p", "-s", "-C", "-a", "-h", "-V"},
	"agents":                          {"-c", "-C", "-h"},
	"app-server daemon update":        {"-c", "-y", "-h"},
	"app-server generate-json-schema": {"-c", "-o", "-h"},
	"app-server generate-ts":          {"-c", "-o", "-p", "-h"},
	"archive":                         {"-i", "-m", "-p", "-s", "-C", "-c", "-h"},
	"cloud":                           {"-c", "-h", "-V"},
	"debug prompt-input":              {"-c", "-i", "-h"},
	"delete":                          {"-i", "-m", "-p", "-s", "-C", "-c", "-h"},
	"exec":                            {"-c", "-i", "-m", "-p", "-s", "-C", "-o", "-h", "-V"},
	"exec fork":                       {"-c", "-i", "-m", "-o", "-h"},
	"exec resume":                     {"-c", "-i", "-m", "-o", "-h"},
	"exec review":                     {"-c", "-m", "-o", "-h"},
	"fork":                            {"-c", "-i", "-m", "-p", "-s", "-C", "-a", "-h", "-V"},
	"plugin add":                      {"-c", "-m", "-h"},
	"plugin list":                     {"-c", "-m", "-h"},
	"plugin remove":                   {"-c", "-m", "-h"},
	"queue":                           {"-p", "-c", "-h", "-C"},
	"resume":                          {"-c", "-i", "-m", "-p", "-s", "-C", "-a", "-h", "-V"},
	"sandbox":                         {"-c", "-P", "-p", "-C", "-h"},
	"unarchive":                       {"-i", "-m", "-p", "-s", "-C", "-c", "-h"},
}

// clapDefaultShortFlags are the aliases every leaf inherits from the global
// --config and --help arguments.
var clapDefaultShortFlags = []string{"-c", "-h"}

// clapCommandShortFlagLongs maps the short aliases whose long spelling differs
// from the shared table, taken from `codex <command> --help`. clap echoes the
// long spelling in its smart usage block, so `plugin add -m x` renders
// `--marketplace <MARKETPLACE>` while `exec -m x` keeps `--model <MODEL>`; an
// empty placeholder marks a boolean alias such as `-y/--yes`.
var clapCommandShortFlagLongs = map[string]map[string]clapValueOption{
	"app-server daemon update": {
		"-y": {"--yes", "", nil},
	},
	"app-server generate-json-schema": {
		"-o": {"--out", "<DIR>", nil},
	},
	"app-server generate-ts": {
		"-o": {"--out", "<DIR>", nil},
		"-p": {"--prettier", "<PRETTIER_BIN>", nil},
	},
	"exec": {
		"-o": {"--output-last-message", "<FILE>", nil},
	},
	"exec fork": {
		"-o": {"--output-last-message", "<FILE>", nil},
	},
	"exec resume": {
		"-o": {"--output-last-message", "<FILE>", nil},
	},
	"exec review": {
		"-o": {"--output-last-message", "<FILE>", nil},
	},
	"plugin add": {
		"-m": {"--marketplace", "<MARKETPLACE>", nil},
	},
	"plugin list": {
		"-m": {"--marketplace", "<MARKETPLACE>", nil},
	},
	"plugin remove": {
		"-m": {"--marketplace", "<MARKETPLACE>", nil},
	},
	"sandbox": {
		"-P": {"--permission-profile", "<NAME>", nil},
		"-p": {"--profile", "<CONFIG_PROFILE>", nil},
	},
}

// clapLongFlagSuggestion is one did-you-mean hit: matched is the spelling clap
// names in the tip, primary the long name it echoes in the usage block.
type clapLongFlagSuggestion struct {
	matched string
	primary string
}

// clapSuggestLongFlag mirrors clap's did_you_mean over a command's long flags:
// strsim's Jaro score above 0.7, highest wins, and a tie keeps the candidate
// declared last because clap pops the tail of its ascending list.
func clapSuggestLongFlag(command, flag string) (clapLongFlagSuggestion, bool) {
	return clapSuggestLongFlagScored(command, flag, false)
}

// clapSuggestDeclaredLongFlag is clapSuggestLongFlag without the self-reference
// guard: clap's second did-you-mean pass walks the top-level subcommands and
// happily names a flag the subcommand declares verbatim, as in
// `codex --skip-git-repo-check exec`.
func clapSuggestDeclaredLongFlag(command, flag string) (clapLongFlagSuggestion, bool) {
	return clapSuggestLongFlagScored(command, flag, true)
}

func clapSuggestLongFlagScored(command, flag string, allowExact bool) (clapLongFlagSuggestion, bool) {
	name := strings.TrimPrefix(flag, "--")
	best := ""
	bestScore := 0.0
	for _, candidate := range clapCommandLongFlags[command] {
		// A spelling clap declares never reaches the did-you-mean pass, so an
		// identical candidate means the parser rejected a flag the table still
		// lists: suggesting it back is never what clap prints.
		if !allowExact && candidate == name {
			continue
		}
		score := jaroSimilarity(name, candidate)
		if score <= 0.7 || (best != "" && score < bestScore) {
			continue
		}
		best, bestScore = candidate, score
	}
	if best == "" {
		return clapLongFlagSuggestion{}, false
	}
	primary := best
	if alias, ok := clapLongFlagAliases[best]; ok {
		primary = alias
	}
	return clapLongFlagSuggestion{matched: best, primary: primary}, true
}

// clapTrailingPositionalCommands declare a positional that captures the
// remaining values (`-- <COMMAND>...`), so clap keeps the `--` workaround tip
// even when it suggests a similar flag.
var clapTrailingPositionalCommands = map[string]bool{
	"mcp add": true,
	"sandbox": true,
}

// clapLongFlagSuggestionFor returns clap's did-you-mean hit for an unknown long
// flag, or false when the command has no table or the token is not a long flag.
func clapLongFlagSuggestionFor(command, arg string) (clapLongFlagSuggestion, bool) {
	if !strings.HasPrefix(arg, "--") {
		return clapLongFlagSuggestion{}, false
	}
	if _, ok := clapCommandLongFlags[command]; !ok {
		return clapLongFlagSuggestion{}, false
	}
	return clapSuggestLongFlag(command, arg)
}

// clapLongFlagUsageToken renders one long flag the way clap's usage block does.
func clapLongFlagUsageToken(command, name string) string {
	primary := name
	if alias, ok := clapLongFlagAliases[name]; ok {
		primary = alias
	}
	placeholder := clapLongFlagPlaceholder(command, primary)
	if placeholder == "" {
		return "--" + primary
	}
	return "--" + primary + " " + placeholder
}

func clapLongFlagPlaceholder(command, name string) string {
	if overrides, ok := clapLongFlagPlaceholderOverrides[command]; ok {
		if placeholder, ok := overrides[name]; ok {
			return placeholder
		}
	}
	if spec, ok := valueOptionSpecs["--"+name]; ok {
		return spec.placeholder
	}
	return subcommandValuePlaceholders["--"+name]
}

// clapUnexpectedLongFlagError renders clap's unknown-long-flag diagnostic: the
// `--` workaround tip, or the did-you-mean tip plus the suggested flag folded
// into the smart usage block when clap scores one above its cutoff.
func clapUnexpectedLongFlagError(command, arg string, consumed []string) error {
	if clapLooksLikeShortFlag(arg) {
		return clapUnexpectedShortFlagError(command, arg)
	}
	name := flagNameOnly(arg)
	message := "unexpected argument '" + name + "' found"
	trailingTip := "\n\n  tip: to pass '" + name + "' as a value, use '-- " + name + "'"
	suggestion, ok := clapSuggestLongFlag(command, name)
	if !ok {
		if !clapCommandHasPositionals(command) {
			trailingTip = ""
		}
		return &UsageError{
			Message:    message + trailingTip,
			Usage:      clapCommandUsageAfter(command, consumed),
			SmartUsage: clapUsageIsSmart(command),
		}
	}
	if clapTrailingPositionalCommands[command] {
		// clap skips the `--` workaround once it suggests a flag, unless the
		// command captures trailing values; both tips stack on separate lines.
		message += strings.TrimPrefix(trailingTip, "\n\n")
	}
	suggestedToken := clapLongFlagUsageToken(command, suggestion.primary)
	echoed := append([]string{}, consumed...)
	if !clapUsageOptionUsed(command, echoed, suggestedToken) {
		// A tip that points at an option argv already carries must not add a
		// second copy: clap echoes each consumed option once.
		echoed = append(echoed, suggestedToken)
	}
	return &UsageError{
		Message:    message + "\n\n  tip: a similar argument exists: '--" + suggestion.matched + "'",
		Usage:      clapCommandUsageAfter(command, echoed),
		SmartUsage: clapUsageIsSmart(command),
		Suggested:  suggestedToken,
	}
}

// clapUsageIsSmart reports whether the command's unknown-argument diagnostic
// renders clap's smart usage block. The root command and `codex exec` keep the
// static page, so decorators must not echo options into them.
func clapUsageIsSmart(command string) bool {
	return command != "" && command != "exec"
}

// clapCommandUsageAfter returns the usage block for an unknown-argument
// diagnostic: the static page for the root command and `codex exec` (clap does
// not echo arguments there), the smart usage everywhere else.
func clapCommandUsageAfter(command string, consumed []string) string {
	switch command {
	case "", "exec":
		return clapCommandUsageText(command)
	}
	if clapReviewUsageRepeats(command, consumed) {
		// clap cannot echo a single-value argument the caller repeated, so it
		// falls back to the static usage page.
		return clapCommandUsageText(command)
	}
	return clapSmartUsage(command, consumed)
}

// clapReviewUsageRepeats reports whether argv repeated an argument clap only
// accepts once for this command.
func clapReviewUsageRepeats(command string, consumed []string) bool {
	return clapUsageTokensRepeat(command, clapUsageOptionTokens(command, consumed, ""))
}

// clapCommandHasPositionals reports whether clap offers the `--` workaround tip
// on a command: it needs a positional argument to pass the value to.
func clapCommandHasPositionals(command string) bool {
	switch command {
	case "", "exec":
		// Both declare a single PROMPT positional.
		return true
	}
	if _, ok := clapCommandPositionalSlots[command]; ok {
		return true
	}
	return subcommandUsageTexts[command].hasPositional
}

func clapCommandUsageText(command string) string {
	switch command {
	case "":
		return rootUsageText
	case "exec":
		return execUsageText
	case "exec resume":
		return execResumeUsageText
	case "exec fork":
		return execForkUsageText
	case "exec review":
		return execReviewUsageText
	}
	return subcommandUsageTexts[command].usage
}

// sessionUnexpectedArgumentError mirrors clap's diagnostic for an unknown token
// on a session subcommand: options clap already consumed are echoed in clap's
// smart usage block, and the positional slots reflect what the parser bound.
func sessionUnexpectedArgumentError(command string, consumed []string, arg string) error {
	if clapLooksLikeShortFlag(arg) {
		return clapUnexpectedShortFlagError(command, arg)
	}
	name := flagNameOnly(arg)
	if suggestion, ok := clapSuggestLongFlag(command, name); ok {
		// clap scores the unknown flag against the declared ones and folds the
		// suggested argument into the smart usage block as well.
		return &UsageError{
			Message: "unexpected argument '" + name + "' found\n\n  tip: a similar argument exists: '--" + suggestion.matched + "'",
			Usage: sessionClapUsage(command, append(append([]string{}, consumed...),
				clapLongFlagUsageToken(command, suggestion.primary))),
			SmartUsage: true,
		}
	}
	return &UsageError{
		Message:    "unexpected argument '" + name + "' found\n\n  tip: to pass '" + name + "' as a value, use '-- " + name + "'",
		Usage:      sessionClapUsage(command, consumed),
		SmartUsage: true,
	}
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
	return clapSmartUsage(command, consumed)
}

// clapSmartUsage renders clap's smart usage block. Commands with a positional
// slot table use the slot renderer; the rest drop [OPTIONS] and echo the options
// clap marked present in front of their remaining block.
func clapSmartUsage(command string, consumed []string) string {
	if _, ok := clapCommandPositionalSlots[command]; ok {
		return clapSlotSmartUsage(command, consumed)
	}
	return clapStringSmartUsage(command, consumed)
}

// clapStringSmartUsage covers the commands without a positional slot table:
// clap only prints [OPTIONS] while nothing was marked present
// (clap_builder/src/output/usage.rs), so the echoed options replace it.
func clapStringSmartUsage(command string, consumed []string) string {
	base := clapCommandUsageText(command)
	if base == "" || clapRequiredKeepsOptions[command] {
		return base
	}
	used, _ := clapUsageState(command, consumed)
	if command == "agents" {
		// Rust leaves --no-daemon out of the smart usage: the overview pulls it
		// from the shared root parser, so clap never marks it used here.
		used = clapRemoveUsageToken(used, "--no-daemon")
	}
	if len(used) == 0 {
		return base
	}
	body := strings.TrimPrefix(base, "Usage: codex "+command+" ")
	body = strings.Replace(body, "[OPTIONS]", "", 1)
	bodyFields := strings.Fields(body)
	// The optional subcommand placeholder only belongs to the static page.
	kept := bodyFields[:0]
	for _, field := range bodyFields {
		if field != "[COMMAND]" {
			kept = append(kept, field)
		}
	}
	bodyFields = kept
	// clap writes the required options first, then the options it consumed
	// (Options::write_args unrolls requirements before the `used` ids) and the
	// positionals last.
	lead, trailing := clapSplitRequiredOptions(bodyFields)
	echo := make([]string, 0, len(used))
	for _, token := range used {
		if strings.Contains(body, token) {
			continue
		}
		echo = append(echo, token)
	}
	fields := make([]string, 0, len(lead)+len(echo)+len(trailing))
	fields = append(fields, lead...)
	fields = append(fields, echo...)
	fields = append(fields, trailing...)
	return strings.Join(append([]string{"Usage: codex " + command}, fields...), " ")
}

// clapSplitRequiredOptions splits a usage body into the leading option block
// (each option with its placeholder) and the trailing positionals.
func clapSplitRequiredOptions(fields []string) (lead, trailing []string) {
	split := 0
	for split < len(fields) {
		field := fields[split]
		if !strings.HasPrefix(field, "-") {
			break
		}
		split++
		if split < len(fields) && strings.HasPrefix(fields[split], "<") {
			split++
		}
	}
	return fields[:split], fields[split:]
}

// clapSlotSmartUsage renders clap's smart usage block: the options clap already
// accepted in argv order, then the positional slots, with every bound slot (and
// every required one) in angle brackets. The static page is returned while
// nothing was consumed.
func clapSlotSmartUsage(command string, consumed []string) string {
	base := clapCommandUsageText(command)
	slots, hasSlots := clapCommandPositionalSlots[command]
	if !hasSlots {
		return base
	}
	used, bound := clapUsageState(command, consumed)
	if len(used) == 0 && bound == 0 {
		return base
	}
	fields := make([]string, 0, len(used)+len(slots)+2)
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

// clapUsageState replays the tokens clap already accepted and returns the
// option tokens clap echoes in its smart usage block plus the number of bound
// positionals. The root and session commands declare `--image` as num_args =
// 1.., so every following non-flag token is one of its values, not a positional;
// `codex exec resume`/`fork` take a single value instead.
func clapUsageState(command string, consumed []string) ([]string, int) {
	command = clapEffectiveCommand(command, consumed)
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
		token, consumesValue := clapUsageOptionToken(command, arg)
		if clapImageIsSingleValue(command) {
			token = strings.Replace(token, "--image <FILE>...", "--image <FILE>", 1)
		}
		if !seen[token] {
			seen[token] = true
			used = append(used, token)
		}
		if isSessionVariadicImageFlag(arg) && !clapImageIsSingleValue(command) {
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

// clapImageIsSingleValue reports whether the command declares `--image` with a
// single value; `codex exec resume` and `codex exec fork` do.
func clapImageIsSingleValue(command string) bool {
	return command == "exec resume" || command == "exec fork"
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
