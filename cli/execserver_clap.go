package cli

import "strings"

// execServerArgSpec mirrors one clap argument of the `codex exec-server`
// command tree. Rust's exec-server parser declares the same relations, so the
// Go port replays clap's scope-local validation instead of hand-rolled checks.
type execServerArgSpec struct {
	Token      string
	TakesValue bool
	// Requires lists clap's `requires` relations.
	Requires []string
	// RequiresIfValue lists `requires_if` relations keyed by the explicit
	// value the caller passed.
	RequiresIfValue map[string][]string
	// Conflicts lists the mutual `conflicts_with` relations.
	Conflicts []string
}

var execServerArgSpecs = map[string]execServerArgSpec{
	"strict-config":                  {Token: "--strict-config"},
	"linux-sandbox-pid-namespace":    {Token: "--linux-sandbox-pid-namespace <MODE>", TakesValue: true},
	"proxy-private-ips-via-upstream": {Token: "--proxy-private-ips-via-upstream"},
	"concurrent-requests":            {Token: "--concurrent-requests <COUNT>", TakesValue: true},
	"listen":                         {Token: "--listen <URL>", TakesValue: true, Conflicts: []string{"remote"}},
	"remote":                         {Token: "--remote <URL>", TakesValue: true, Requires: []string{"environment-id"}},
	"remote-transport": {
		Token:           "--remote-transport <REMOTE_TRANSPORT>",
		TakesValue:      true,
		Requires:        []string{"remote"},
		RequiresIfValue: map[string][]string{"direct": {"aws-sigv4"}},
	},
	"environment-id":            {Token: "--environment-id <ID>", TakesValue: true},
	"name":                      {Token: "--name <NAME>", TakesValue: true},
	"use-agent-identity-auth":   {Token: "--use-agent-identity-auth", Requires: []string{"remote"}, Conflicts: []string{"aws-sigv4"}},
	"executor-credential-stdin": {Token: "--executor-credential-stdin", Requires: []string{"remote"}, Conflicts: []string{"use-agent-identity-auth", "aws-sigv4"}},
	"aws-sigv4":                 {Token: "--aws-sigv4", Requires: []string{"remote"}},
	"aws-profile":               {Token: "--aws-profile <PROFILE>", TakesValue: true, Requires: []string{"aws-sigv4"}},
	"aws-region":                {Token: "--aws-region <REGION>", TakesValue: true, Requires: []string{"aws-sigv4"}},
	"aws-service":               {Token: "--aws-service <SERVICE>", TakesValue: true, Requires: []string{"aws-sigv4"}},
	"exit-on-stdin-close":       {Token: "--exit-on-stdin-close", Requires: []string{"remote"}},
	"connect":                   {Token: "--connect <URL>", TakesValue: true},
}

// execServerForwardBase is the argument order clap renders for the forward
// subcommand's required group.
var execServerForwardBase = []string{"remote", "environment-id", "connect"}

const execServerRootDuplicateUsage = "Usage: codex exec-server [OPTIONS] [COMMAND]"

const execServerForwardDuplicateUsage = "Usage: codex exec-server forward [OPTIONS] --remote <URL> --environment-id <ID> --connect <URL>"

// execServerHit records one argument occurrence together with the command
// scope that matched it. clap validates `requires` per subcommand scope, so an
// option given before `forward` never satisfies that subcommand's relations.
type execServerHit struct {
	Name    string
	Value   string
	Forward bool
}

// execServerPresenceScan replays argv and records the exec-server arguments it
// carries. Callers only run it after a successful parse, so unknown flags and
// positional values cannot appear.
func execServerPresenceScan(args []string) []execServerHit {
	var hits []execServerHit
	forward := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			break
		}
		if arg == "forward" {
			forward = true
			continue
		}
		name, value, hasValue := execServerFlagName(arg)
		switch {
		case name == "":
			continue
		case name == "config":
			if !hasValue && i+1 < len(args) {
				i++
			}
			continue
		}
		spec, ok := execServerArgSpecs[name]
		if !ok {
			continue
		}
		if spec.TakesValue && !hasValue && i+1 < len(args) {
			value = args[i+1]
			i++
		}
		hits = append(hits, execServerHit{Name: name, Value: value, Forward: forward})
	}
	return hits
}

func execServerFlagName(arg string) (string, string, bool) {
	if strings.HasPrefix(arg, "-c") && !strings.HasPrefix(arg, "--") {
		value := strings.TrimPrefix(arg, "-c")
		if strings.HasPrefix(value, "=") {
			return "config", strings.TrimPrefix(value, "="), true
		}
		return "config", value, value != ""
	}
	if !strings.HasPrefix(arg, "--") {
		return "", "", false
	}
	body := strings.TrimPrefix(arg, "--")
	if index := strings.Index(body, "="); index >= 0 {
		return body[:index], body[index+1:], true
	}
	return body, "", false
}

// validateExecServerClapScopes replays clap's duplicate, conflict and
// requirement checks over both the root command and the `forward` subcommand.
// clap validates the deepest matched subcommand first, so forward errors win
// over root-scope ones.
func validateExecServerClapScopes(args []string, opts *ExecServerOptions) error {
	if opts == nil {
		return nil
	}
	hits := execServerPresenceScan(args)
	if err := execServerDuplicateError(hits); err != nil {
		return err
	}
	if opts.ForwardBound {
		forwardHits := execServerScopeHits(hits, true)
		if err := execServerConflictError(forwardHits, true); err != nil {
			return err
		}
		if err := execServerRequiredError(forwardHits, true); err != nil {
			return err
		}
	}
	rootHits := execServerScopeHits(hits, false)
	if !containsExecServerHit(hits, "exit-on-stdin-close") && opts.ExitOnStdinClose {
		// clap applies the CODEX_EXEC_SERVER_EXIT_ON_STDIN_CLOSE environment
		// value to the root matcher, where `requires_if("true", ...)` still
		// pulls in the remote registration arguments.
		rootHits = append(rootHits, execServerHit{Name: "exit-on-stdin-close", Value: "true"})
	}
	if err := execServerConflictError(rootHits, false); err != nil {
		return err
	}
	return execServerRequiredError(rootHits, false)
}

func execServerScopeHits(hits []execServerHit, forward bool) []execServerHit {
	scoped := make([]execServerHit, 0, len(hits))
	for _, hit := range hits {
		if hit.Forward == forward {
			scoped = append(scoped, hit)
		}
	}
	return scoped
}

func containsExecServerHit(hits []execServerHit, name string) bool {
	for _, hit := range hits {
		if hit.Name == name {
			return true
		}
	}
	return false
}

func execServerToken(name string) string {
	return execServerArgSpecs[name].Token
}

// execServerDuplicateError reports a value option clap refuses to repeat. The
// check is scope-local: `--remote` may appear once at the root command and once
// under `forward`.
func execServerDuplicateError(hits []execServerHit) error {
	seen := make(map[string]bool, len(hits))
	for _, hit := range hits {
		spec := execServerArgSpecs[hit.Name]
		if !spec.TakesValue {
			continue
		}
		key := hit.Name
		if hit.Forward {
			key = "forward/" + key
		}
		if seen[key] {
			usage := execServerRootDuplicateUsage
			if hit.Forward {
				usage = execServerForwardDuplicateUsage
			}
			return &UsageError{
				Message: "the argument '" + spec.Token + "' cannot be used multiple times",
				Usage:   usage,
			}
		}
		seen[key] = true
	}
	return nil
}

// execServerConflictError renders clap's argument-conflict diagnostic for one
// scope. The first argument in argv order that conflicts becomes the subject.
func execServerConflictError(hits []execServerHit, forward bool) error {
	for _, hit := range hits {
		conflicting := execServerConflictingHits(hits, hit.Name)
		if len(conflicting) == 0 {
			continue
		}
		message := "the argument '" + execServerToken(hit.Name) + "' cannot be used with '" + execServerToken(conflicting[0].Name) + "'"
		if len(conflicting) > 1 {
			message = "the argument '" + execServerToken(hit.Name) + "' cannot be used with:"
			for _, conflict := range conflicting {
				message += "\n  " + execServerToken(conflict.Name)
			}
		}
		excluded := make(map[string]bool, len(conflicting))
		for _, conflict := range conflicting {
			excluded[conflict.Name] = true
		}
		kept := make([]execServerHit, 0, len(hits))
		for _, candidate := range hits {
			if excluded[candidate.Name] {
				continue
			}
			kept = append(kept, candidate)
		}
		_, usage := execServerRequirements(kept, forward)
		return &UsageError{
			Message: message,
			Usage:   usage,
		}
	}
	return nil
}

// execServerConflictingHits returns the present arguments that cannot be
// combined with name, in argv order, de-duplicated by name.
func execServerConflictingHits(hits []execServerHit, name string) []execServerHit {
	declared := make(map[string]bool)
	for _, conflict := range execServerArgSpecs[name].Conflicts {
		declared[conflict] = true
	}
	for other, spec := range execServerArgSpecs {
		for _, conflict := range spec.Conflicts {
			if conflict == name {
				declared[other] = true
			}
		}
	}
	conflicting := make([]execServerHit, 0, len(hits))
	seen := make(map[string]bool)
	for _, hit := range hits {
		if hit.Name == name || !declared[hit.Name] || seen[hit.Name] {
			continue
		}
		seen[hit.Name] = true
		conflicting = append(conflicting, hit)
	}
	return conflicting
}

// execServerRequiredError replays clap's requirement check for one scope.
func execServerRequiredError(hits []execServerHit, forward bool) error {
	missing, usage := execServerRequirements(hits, forward)
	if len(missing) == 0 {
		return nil
	}
	return &UsageError{
		Message: requiredArgumentsMessage(missing...),
		Usage:   usage,
	}
}

func execServerRequirements(hits []execServerHit, forward bool) ([]string, string) {
	present := make(map[string]bool, len(hits))
	for _, hit := range hits {
		present[hit.Name] = true
	}
	command := "Usage: codex exec-server"
	if forward {
		command += " forward"
	}
	if forward {
		required := append([]string{}, execServerForwardBase...)
		for _, hit := range hits {
			if hit.Name == "aws-profile" || hit.Name == "aws-region" || hit.Name == "aws-service" {
				required = append(required, "aws-sigv4")
			}
			if hit.Name == "remote-transport" && strings.EqualFold(strings.TrimSpace(hit.Value), "direct") {
				required = append(required, "aws-sigv4")
			}
		}
		required = execServerUniqueNames(required)
		tokens := execServerForwardUsageTokens(required, hits)
		return execServerMissingTokens(required, present), command + " " + strings.Join(tokens, " ")
	}
	chain := execServerRequirementClosure(hits, present)
	emitted := make(map[string]bool, len(chain)+len(hits))
	var tokens []string
	var missing []string
	for _, name := range chain {
		if emitted[name] {
			continue
		}
		emitted[name] = true
		if !present[name] {
			missing = append(missing, execServerToken(name))
		}
		tokens = append(tokens, execServerToken(name))
	}
	for _, hit := range hits {
		if emitted[hit.Name] {
			continue
		}
		emitted[hit.Name] = true
		tokens = append(tokens, execServerToken(hit.Name))
	}
	return missing, command + " " + strings.Join(tokens, " ")
}

// execServerRequirementClosure collects the requirement chains clap renders in
// front of the arguments argv carried. clap only unrolls the transitive
// requirement chain of an argument whose immediate requirement is missing.
func execServerRequirementClosure(hits []execServerHit, present map[string]bool) []string {
	var closure []string
	for _, hit := range hits {
		chain := execServerUnrollRequirements(hit.Name, hit.Value)
		satisfied := true
		for _, requirement := range chain {
			if !present[requirement] {
				satisfied = false
				break
			}
		}
		if satisfied {
			continue
		}
		for index := len(chain) - 1; index >= 0; index-- {
			if !execServerContainsName(closure, chain[index]) {
				closure = append(closure, chain[index])
			}
		}
	}
	// clap renders the SigV4 requirement group of the AWS profile family with
	// --remote ahead of --environment-id.
	if execServerAWSOptionPresent(hits) && !present["remote"] {
		closure = execServerMoveFirst(closure, "remote")
	}
	return closure
}

func execServerDirectRequirements(name, value string) []string {
	spec, ok := execServerArgSpecs[name]
	if !ok {
		return nil
	}
	requirements := make([]string, 0, len(spec.RequiresIfValue)+len(spec.Requires))
	requirements = append(requirements, spec.RequiresIfValue[value]...)
	requirements = append(requirements, spec.Requires...)
	return execServerUniqueNames(requirements)
}

func execServerUnrollRequirements(name, value string) []string {
	var out []string
	for _, requirement := range execServerDirectRequirements(name, value) {
		if !execServerContainsName(out, requirement) {
			out = append(out, requirement)
		}
		for _, nested := range execServerUnrollRequirements(requirement, "") {
			if !execServerContainsName(out, nested) {
				out = append(out, nested)
			}
		}
	}
	return out
}

func execServerForwardUsageTokens(required []string, hits []execServerHit) []string {
	tokens := make([]string, 0, len(required)+len(hits))
	emitted := make(map[string]bool, len(required)+len(hits))
	for _, name := range required {
		if emitted[name] {
			continue
		}
		emitted[name] = true
		tokens = append(tokens, execServerToken(name))
	}
	for _, hit := range hits {
		if emitted[hit.Name] {
			continue
		}
		emitted[hit.Name] = true
		tokens = append(tokens, execServerToken(hit.Name))
	}
	return tokens
}

func execServerMissingTokens(required []string, present map[string]bool) []string {
	missing := make([]string, 0, len(required))
	for _, name := range required {
		if present[name] {
			continue
		}
		missing = append(missing, execServerToken(name))
	}
	return missing
}

func execServerAWSOptionPresent(hits []execServerHit) bool {
	for _, hit := range hits {
		switch hit.Name {
		case "aws-profile", "aws-region", "aws-service":
			return true
		}
	}
	return false
}

func execServerMoveFirst(names []string, first string) []string {
	reordered := make([]string, 0, len(names))
	reordered = append(reordered, first)
	for _, name := range names {
		if name == first {
			continue
		}
		reordered = append(reordered, name)
	}
	return reordered
}

func execServerUniqueNames(names []string) []string {
	unique := make([]string, 0, len(names))
	for _, name := range names {
		if execServerContainsName(unique, name) {
			continue
		}
		unique = append(unique, name)
	}
	return unique
}

func execServerContainsName(names []string, name string) bool {
	for _, candidate := range names {
		if candidate == name {
			return true
		}
	}
	return false
}
