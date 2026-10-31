package cli

import "strings"

// This file mirrors the clap argument model `codex app-server` declares: which
// options conflict with each other, which option they require, and how clap
// renders the usage block of the diagnostics those relations raise. The tables
// follow the 0.166.0 binary, including the three argument groups clap prints as
// `<--a|--b|...>` once one of their members was accepted.

// appServerOption is one declared `codex app-server` argument.
type appServerOption struct {
	// name is clap's argument id.
	name string
	// token is how the argument renders inside a usage block.
	token string
	// takesValue reports whether clap consumes a separate value token.
	takesValue bool
	// hidden reports whether clap declares the argument with hide(true), which
	// keeps it out of every usage echo.
	hidden bool
	// group is the clap argument group the option belongs to, if any.
	group string
}

const (
	appServerStartupGroup      = "<--startup-sign-out|--print-startup-config|--startup-client-info <STARTUP_CLIENT_INFO>>"
	appServerWebSocketGroup    = "<--ws-auth <MODE>|--ws-token-file <PATH>|--ws-token-sha256 <HEX>|--ws-shared-secret-file <PATH>|--ws-issuer <ISSUER>|--ws-audience <AUDIENCE>|--ws-max-clock-skew-seconds <SECONDS>>"
	appServerCodeModeHostGroup = "<--code-mode-host <URL>>"
)

// appServerOptions lists the arguments `codex app-server` declares, in
// declaration order.
var appServerOptions = []appServerOption{
	{name: "config", token: "--config <key=value>", takesValue: true},
	{name: "startup-sign-out", token: "--startup-sign-out", group: "startup"},
	{name: "enable", token: "--enable <FEATURE>", takesValue: true},
	{name: "print-startup-config", token: "--print-startup-config", group: "startup"},
	{name: "disable", token: "--disable <FEATURE>", takesValue: true},
	{name: "startup-client-info", token: "--startup-client-info <STARTUP_CLIENT_INFO>", takesValue: true, group: "startup"},
	{name: "cloud-skills-snapshot", token: "--cloud-skills-snapshot <PATH>", takesValue: true},
	{name: "code-mode-host", token: "--code-mode-host <URL>", takesValue: true, group: "code-mode-host"},
	{name: "strict-config", token: "--strict-config"},
	{name: "listen", token: "--listen <URL>", takesValue: true},
	{name: "stdio", token: "--stdio"},
	{name: "analytics-default-enabled", token: "--analytics-default-enabled"},
	{name: "ws-auth", token: "--ws-auth <MODE>", takesValue: true, group: "ws"},
	{name: "ws-token-file", token: "--ws-token-file <PATH>", takesValue: true, group: "ws"},
	{name: "ws-token-sha256", token: "--ws-token-sha256 <HEX>", takesValue: true, group: "ws"},
	{name: "ws-shared-secret-file", token: "--ws-shared-secret-file <PATH>", takesValue: true, group: "ws"},
	{name: "ws-issuer", token: "--ws-issuer <ISSUER>", takesValue: true, group: "ws"},
	{name: "ws-audience", token: "--ws-audience <AUDIENCE>", takesValue: true, group: "ws"},
	{name: "ws-max-clock-skew-seconds", token: "--ws-max-clock-skew-seconds <SECONDS>", takesValue: true, group: "ws"},
	{name: "remote-control", token: "--remote-control", hidden: true},
}

// appServerConflicts mirrors the symmetric conflicts_with_all relations clap
// declares for the startup gate. `--remote-control` stays out of the help page
// but takes part in the conflict check.
var appServerConflicts = map[string][]string{
	"print-startup-config": {"stdio", "listen", "remote-control"},
	"stdio":                {"listen", "print-startup-config"},
	"listen":               {"stdio", "print-startup-config"},
	"remote-control":       {"print-startup-config"},
}

// appServerRequires mirrors clap's requires relations: the startup discovery
// flags only run together with `--print-startup-config`.
var appServerRequires = map[string][]string{
	"startup-sign-out":    {"print-startup-config"},
	"startup-client-info": {"print-startup-config"},
}

// appServerGroupUsage renders the argument group a used option belongs to.
func appServerGroupUsage(group string) string {
	switch group {
	case "startup":
		return appServerStartupGroup
	case "ws":
		return appServerWebSocketGroup
	case "code-mode-host":
		return appServerCodeModeHostGroup
	}
	return ""
}

// appServerOptionByName finds a declared argument by clap id.
func appServerOptionByName(name string) (appServerOption, bool) {
	for _, option := range appServerOptions {
		if option.name == name {
			return option, true
		}
	}
	return appServerOption{}, false
}

// appServerLookupArg resolves one argv token to a declared argument. The
// `--flag=value` spelling is folded onto the flag, and the `-c` alias resolves
// to `--config`.
func appServerLookupArg(arg string) (appServerOption, bool) {
	name := arg
	if trimmed, _, ok := strings.Cut(arg, "="); ok {
		name = trimmed
	}
	if name == "-c" {
		name = "--config"
	}
	if !strings.HasPrefix(name, "--") {
		return appServerOption{}, false
	}
	return appServerOptionByName(strings.TrimPrefix(name, "--"))
}

// appServerOptionHit records one option clap accepted together with where its
// value token sat in argv. The parse-time diagnostics need that position: a
// value option whose value is the token right before the offender is still
// pending in clap's matcher and never reaches a usage block.
type appServerOptionHit struct {
	opt     appServerOption
	flagAt  int
	valueAt int
}

// appServerScan replays the options clap accepts for `codex app-server`,
// stopping in front of the subcommand name, which clap parses with the
// subcommand's own tables.
func appServerScan(args []string) []appServerOptionHit {
	hits := make([]appServerOptionHit, 0, len(args))
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if arg == "--" {
			break
		}
		if !clapLooksLikeOption(arg) {
			break
		}
		opt, known := appServerLookupArg(arg)
		if !known {
			continue
		}
		hit := appServerOptionHit{opt: opt, flagAt: index, valueAt: -1}
		if opt.takesValue && !strings.Contains(arg, "=") && index+1 < len(args) {
			hit.valueAt = index + 1
			index++
		}
		hits = append(hits, hit)
	}
	return hits
}

// appServerSubcommandIndex returns the argv index that names the app-server
// subcommand, or -1 when argv carries none. clap hands the tokens from there on
// to the subcommand's own parser.
func appServerSubcommandIndex(args []string) int {
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if arg == "--" {
			return -1
		}
		if !clapLooksLikeOption(arg) {
			return index
		}
		if known, found := appServerLookupArg(arg); found && known.takesValue && !strings.Contains(arg, "=") {
			index++
		}
	}
	return -1
}

// appServerPresentOptions replays the arguments clap marks present after a
// successful parse, in the order the matcher recorded them.
func appServerPresentOptions(args []string) []appServerOption {
	hits := appServerScan(args)
	present := make([]appServerOption, 0, len(hits))
	seen := make(map[string]bool, len(hits))
	for _, hit := range hits {
		if seen[hit.opt.name] {
			continue
		}
		seen[hit.opt.name] = true
		present = append(present, hit.opt)
	}
	return present
}

// validateAppServerStartupGate replays clap's post-parse conflicts check and
// the requirement check that follows it, so `codex app-server` rejects the same
// argument combinations as Rust and prints clap's diagnostics.
func validateAppServerStartupGate(args []string, opts *AppServerOptions) error {
	if opts == nil {
		return nil
	}
	present := appServerPresentOptions(args)
	if err := appServerConflictError(present); err != nil {
		return err
	}
	return appServerRequirementError(present)
}

// appServerConflictError reports the first present argument clap finds a
// conflict for, listing every conflicting argument it accepted.
func appServerConflictError(present []appServerOption) error {
	for _, option := range present {
		conflicting := appServerConflictingPresent(present, option)
		if len(conflicting) == 0 {
			continue
		}
		return appServerArgumentConflictError(option, conflicting, present)
	}
	return nil
}

// appServerConflictingPresent returns the present arguments the option cannot
// be combined with, in argv order.
func appServerConflictingPresent(present []appServerOption, option appServerOption) []appServerOption {
	declared := appServerConflicts[option.name]
	if len(declared) == 0 {
		return nil
	}
	conflicting := make([]appServerOption, 0, len(declared))
	for _, candidate := range present {
		for _, name := range declared {
			if candidate.name == name {
				conflicting = append(conflicting, candidate)
				break
			}
		}
	}
	return conflicting
}

// appServerArgumentConflictError renders clap's argument-conflict diagnostic.
// The usage block lists the accepted options minus the conflicting ones, with
// the requirements those options bring in front of them.
func appServerArgumentConflictError(option appServerOption, conflicting, present []appServerOption) error {
	message := "the argument '" + option.token + "' cannot be used with '" + conflicting[0].token + "'"
	if len(conflicting) > 1 {
		message = "the argument '" + option.token + "' cannot be used with:"
		for _, conflict := range conflicting {
			message += "\n  " + conflict.token
		}
	}
	excluded := make(map[string]bool, len(conflicting))
	for _, conflict := range conflicting {
		excluded[conflict.name] = true
	}
	kept := make([]appServerOption, 0, len(present))
	used := make([]string, 0, len(present))
	for _, candidate := range present {
		if candidate.hidden || excluded[candidate.name] {
			continue
		}
		kept = append(kept, candidate)
		used = append(used, candidate.token)
	}
	required := make([]string, 0, len(used))
	for _, candidate := range kept {
		for _, name := range appServerRequires[candidate.name] {
			requirement, known := appServerOptionByName(name)
			if !known || excluded[name] || clapUsageTokenListed(used, requirement.token) || clapUsageTokenListed(required, requirement.token) {
				continue
			}
			required = append(required, requirement.token)
		}
	}
	return &UsageError{
		Message:    message,
		Usage:      appServerUsageText(append(required, used...)),
		SmartUsage: true,
	}
}

// appServerRequirementError reports the `requires` targets clap accepted an
// option for but never received. clap skips a requirement whose argument
// conflicts with an accepted one, which is how `--startup-client-info` passes
// next to `--stdio`.
func appServerRequirementError(present []appServerOption) error {
	if len(present) == 0 {
		return nil
	}
	accepted := make(map[string]bool, len(present))
	for _, option := range present {
		accepted[option.name] = true
	}
	var roots []string
	var missing []string
	for _, option := range present {
		for _, name := range appServerRequires[option.name] {
			if accepted[name] || len(appServerConflictingPresent(present, appServerOption{name: name})) > 0 {
				continue
			}
			if !clapUsageTokenListed(roots, name) {
				roots = append(roots, name)
			}
			if !clapUsageTokenListed(missing, name) {
				missing = append(missing, name)
			}
		}
	}
	if len(missing) == 0 {
		return nil
	}
	used := make([]string, 0, len(roots)+len(present)+len(missing))
	for _, name := range roots {
		if requirement, known := appServerOptionByName(name); known {
			used = append(used, requirement.token)
		}
	}
	for _, option := range present {
		if option.hidden {
			continue
		}
		used = append(used, option.token)
	}
	for _, name := range missing {
		if requirement, known := appServerOptionByName(name); known {
			used = append(used, requirement.token)
		}
	}
	tokens := make([]string, 0, len(used))
	seen := make(map[string]bool, len(used))
	for _, token := range used {
		if seen[token] {
			continue
		}
		seen[token] = true
		tokens = append(tokens, token)
	}
	return &UsageError{
		Message:    requiredArgumentsMessage(appServerRequirementTokens(roots)...),
		Usage:      appServerUsageText(tokens),
		SmartUsage: true,
	}
}

// appServerRequirementTokens renders the required argument ids clap reports in
// the diagnostic body.
func appServerRequirementTokens(names []string) []string {
	tokens := make([]string, 0, len(names))
	for _, name := range names {
		if requirement, known := appServerOptionByName(name); known {
			tokens = append(tokens, requirement.token)
		}
	}
	return tokens
}

// appServerUsageText renders a smart usage block for `codex app-server`. clap
// falls back to the static page while nothing was marked present.
func appServerUsageText(tokens []string) string {
	if len(tokens) == 0 {
		return clapCommandUsageText("app-server")
	}
	return "Usage: codex app-server " + strings.Join(tokens, " ")
}

// appServerAttachedValueArg reports a `--flag=value` spelling of an argument
// that takes no value. clap rejects those while parsing, before it resolves the
// option that precedes them.
func appServerAttachedValueArg(arg string) (appServerOption, string, bool) {
	name, value, ok := strings.Cut(arg, "=")
	if !ok || !strings.HasPrefix(name, "--") {
		return appServerOption{}, "", false
	}
	opt, known := appServerOptionByName(strings.TrimPrefix(name, "--"))
	if !known || opt.takesValue {
		return appServerOption{}, "", false
	}
	return opt, value, true
}

// appServerUnexpectedValueError renders clap's rejection of `--flag=value` on a
// flag that takes no value. The usage block echoes the options clap already
// committed plus the offending flag, and folds every accepted argument group
// into its `<--a|--b>` rendering: clap never repeats a group member on its own.
func appServerUnexpectedValueError(args []string, offender int, opt appServerOption, value string) error {
	hits := appServerScan(args[:offender])
	if len(hits) > 0 && hits[len(hits)-1].valueAt == offender-1 {
		// clap still holds that value in its pending slot when it raises the
		// diagnostic, so the option never reaches the matcher.
		hits = hits[:len(hits)-1]
	}
	groups := make([]string, 0, 2)
	members := make(map[string]bool, len(hits))
	seenGroup := make(map[string]bool, 2)
	for _, hit := range hits {
		members[hit.opt.name] = true
		if hit.opt.group == "" || seenGroup[hit.opt.group] {
			continue
		}
		seenGroup[hit.opt.group] = true
		groups = append(groups, appServerGroupUsage(hit.opt.group))
	}
	tokens := make([]string, 0, len(hits)+len(groups)+1)
	seen := make(map[string]bool, len(hits))
	for _, hit := range hits {
		if hit.opt.hidden || members[hit.opt.name] && hit.opt.group != "" || seen[hit.opt.token] {
			continue
		}
		seen[hit.opt.token] = true
		tokens = append(tokens, hit.opt.token)
	}
	tokens = append(tokens, opt.token)
	tokens = append(tokens, groups...)
	return &UsageError{
		Message:    "unexpected value '" + value + "' for '" + opt.token + "' found; no more were expected",
		Usage:      appServerUsageText(tokens),
		SmartUsage: true,
	}
}

// appServerAttachedValueDiagnostic replaces the unknown-argument diagnostic the
// app-server parser raises for `--flag=value` with clap's unexpected-value
// message. clap raises it while parsing, so it only applies when the parser
// failed on that very token.
func appServerAttachedValueDiagnostic(args []string, err error) (error, bool) {
	usageErr, ok := err.(*UsageError)
	if !ok {
		return nil, false
	}
	offender := -1
	var opt appServerOption
	value := ""
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if arg == "--" || !clapLooksLikeOption(arg) {
			break
		}
		if candidate, candidateValue, attached := appServerAttachedValueArg(arg); attached {
			offender, opt, value = index, candidate, candidateValue
			break
		}
		if known, found := appServerLookupArg(arg); found && known.takesValue && !strings.Contains(arg, "=") {
			index++
		}
	}
	if offender < 0 || !strings.HasPrefix(usageErr.Message, "unexpected argument '"+opt.token+"' found") {
		return nil, false
	}
	return appServerUnexpectedValueError(args, offender, opt, value), true
}

// clapUsageTokenListed reports whether the rendered token list already carries
// token.
func clapUsageTokenListed(tokens []string, token string) bool {
	for _, candidate := range tokens {
		if candidate == token {
			return true
		}
	}
	return false
}
