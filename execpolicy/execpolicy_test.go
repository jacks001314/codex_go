package execpolicy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckMatchesPrefixRule(t *testing.T) {
	path := writePolicy(t, `
prefix_rule(
    pattern = ["git", "push"],
    decision = "forbidden",
)
`)
	output, err := Check(&CheckOptions{Rules: []string{path}, Command: []string{"git", "push", "origin"}})
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if output.Decision == nil || *output.Decision != DecisionForbidden {
		t.Fatalf("Decision = %#v", output.Decision)
	}
	if len(output.MatchedRules) != 1 || strings.Join(output.MatchedRules[0].PrefixRuleMatch.MatchedPrefix, " ") != "git push" {
		t.Fatalf("MatchedRules = %#v", output.MatchedRules)
	}
}

func TestCheckParsesPolicyCallsWithWhitespaceBeforeParen(t *testing.T) {
	path := writePolicy(t, `
prefix_rule (
    pattern = ["git", "status"],
    decision = "prompt",
)
`)
	output, err := Check(&CheckOptions{Rules: []string{path}, Command: []string{"git", "status"}})
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if output.Decision == nil || *output.Decision != DecisionPrompt {
		t.Fatalf("Decision = %#v, want prompt", output.Decision)
	}
}

func TestCheckParsesPositionalPrefixRuleArguments(t *testing.T) {
	path := writePolicy(t, `
prefix_rule(["git", ["push", "pull"]], "forbidden", [["git", "push"]], [["git", "status"]], "protected remote")
`)
	output, err := Check(&CheckOptions{Rules: []string{path}, Command: []string{"git", "pull", "origin"}})
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if output.Decision == nil || *output.Decision != DecisionForbidden {
		t.Fatalf("Decision = %#v, want forbidden", output.Decision)
	}
	rendered, err := Render(output, false)
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if !strings.Contains(rendered, `"justification":"protected remote"`) {
		t.Fatalf("rendered = %s", rendered)
	}
}

func TestCheckParsesEscapedStarlarkStringLiterals(t *testing.T) {
	path := writePolicy(t, `
prefix_rule(
    pattern = ["git", "say \"hi\"", "path\\name"],
    decision = "prompt",
    justification = "quote: \"ok\" and slash: \\",
)
`)
	output, err := Check(&CheckOptions{Rules: []string{path}, Command: []string{"git", `say "hi"`, `path\name`}})
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if output.Decision == nil || *output.Decision != DecisionPrompt {
		t.Fatalf("Decision = %#v, want prompt", output.Decision)
	}
	if len(output.MatchedRules) != 1 {
		t.Fatalf("MatchedRules = %#v, want one match", output.MatchedRules)
	}
	if got := strings.Join(output.MatchedRules[0].PrefixRuleMatch.MatchedPrefix, "\x00"); got != "git\x00say \"hi\"\x00path\\name" {
		t.Fatalf("MatchedPrefix = %#v", output.MatchedRules[0].PrefixRuleMatch.MatchedPrefix)
	}
	if got, want := output.MatchedRules[0].PrefixRuleMatch.Justification, `quote: "ok" and slash: \`; got != want {
		t.Fatalf("Justification = %q, want %q", got, want)
	}
}

func TestCheckRejectsInvalidStringEscape(t *testing.T) {
	path := writePolicy(t, `
prefix_rule(
    pattern = ["git\q"],
)
`)
	_, err := Check(&CheckOptions{Rules: []string{path}, Command: []string{"git"}})
	if err == nil || !strings.Contains(err.Error(), "pattern element has invalid string literal") {
		t.Fatalf("Check() error = %v, want invalid string literal", err)
	}
}

func TestCheckParsesSimpleStarlarkConstants(t *testing.T) {
	path := writePolicy(t, `
PROGRAM = "git"
STATUS = "status" # comments after assignments are ignored like Starlark
PATTERN = [PROGRAM, STATUS]
MATCHES = [[PROGRAM, STATUS]]
JUSTIFICATION = "from constants"
TAIL_ALIASES = ["commit", "merge"]

prefix_rule(
    pattern = PATTERN,
    decision = "prompt",
    match = MATCHES,
    justification = JUSTIFICATION,
)
prefix_rule(
    pattern = [PROGRAM, TAIL_ALIASES],
    decision = "forbidden",
)
`)
	status, err := Check(&CheckOptions{Rules: []string{path}, Command: []string{"git", "status"}})
	if err != nil {
		t.Fatalf("Check(status) error = %v", err)
	}
	if status.Decision == nil || *status.Decision != DecisionPrompt {
		t.Fatalf("status Decision = %#v, want prompt", status.Decision)
	}
	if got := status.MatchedRules[0].PrefixRuleMatch.Justification; got != "from constants" {
		t.Fatalf("status Justification = %q", got)
	}

	merge, err := Check(&CheckOptions{Rules: []string{path}, Command: []string{"git", "merge", "main"}})
	if err != nil {
		t.Fatalf("Check(merge) error = %v", err)
	}
	if merge.Decision == nil || *merge.Decision != DecisionForbidden {
		t.Fatalf("merge Decision = %#v, want forbidden", merge.Decision)
	}
	if got := strings.Join(merge.MatchedRules[0].PrefixRuleMatch.MatchedPrefix, " "); got != "git merge" {
		t.Fatalf("merge MatchedPrefix = %q", got)
	}
}

func TestCheckConstantsFollowStarlarkStatementOrder(t *testing.T) {
	path := writePolicy(t, `
prefix_rule(pattern = PATTERN)
PATTERN = ["git"]
`)
	_, err := Check(&CheckOptions{Rules: []string{path}, Command: []string{"git"}})
	if err == nil || !strings.Contains(err.Error(), "expected list, got PATTERN") {
		t.Fatalf("Check() error = %v, want unresolved forward constant error", err)
	}
}

func TestCheckParsesSimpleStarlarkConcatenation(t *testing.T) {
	path := writePolicy(t, `
PROGRAM = "g" + "it"
BASE = [PROGRAM]
STATUS = "sta" + "tus"
PATTERN = BASE + [STATUS]
MATCHES = [BASE + [STATUS]]
DECISION = "pro" + "mpt"
WHY = "joined " + "strings"

prefix_rule(
    pattern = PATTERN,
    decision = DECISION,
    match = MATCHES,
    justification = WHY,
)
`)
	output, err := Check(&CheckOptions{Rules: []string{path}, Command: []string{"git", "status"}})
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if output.Decision == nil || *output.Decision != DecisionPrompt {
		t.Fatalf("Decision = %#v, want prompt", output.Decision)
	}
	if got := strings.Join(output.MatchedRules[0].PrefixRuleMatch.MatchedPrefix, " "); got != "git status" {
		t.Fatalf("MatchedPrefix = %q", got)
	}
	if got := output.MatchedRules[0].PrefixRuleMatch.Justification; got != "joined strings" {
		t.Fatalf("Justification = %q", got)
	}
}

func TestCheckParsesSimpleStarlarkFStrings(t *testing.T) {
	path := writePolicy(t, `
PROGRAM = "g" + "it"
ACTION = "sta" + "tus"
WHY = f"{{{PROGRAM}}} {ACTION}"

prefix_rule(
    pattern = [f"{PROGRAM}", f"{ACTION}"],
    decision = "prompt",
    justification = WHY,
)
`)
	output, err := Check(&CheckOptions{Rules: []string{path}, Command: []string{"git", "status"}})
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if output.Decision == nil || *output.Decision != DecisionPrompt {
		t.Fatalf("Decision = %#v, want prompt", output.Decision)
	}
	if got := strings.Join(output.MatchedRules[0].PrefixRuleMatch.MatchedPrefix, " "); got != "git status" {
		t.Fatalf("MatchedPrefix = %q", got)
	}
	if got := output.MatchedRules[0].PrefixRuleMatch.Justification; got != "{git} status" {
		t.Fatalf("Justification = %q", got)
	}
}

func TestCheckRejectsInvalidStarlarkFString(t *testing.T) {
	path := writePolicy(t, `
PROGRAM = "git"
prefix_rule(pattern = [f"{PROGRAM"])
`)
	_, err := Check(&CheckOptions{Rules: []string{path}, Command: []string{"git"}})
	if err == nil || !strings.Contains(err.Error(), "invalid f-string expression") {
		t.Fatalf("Check() error = %v, want invalid f-string expression", err)
	}
}

func TestCheckRejectsInvalidPositionalArgumentOrder(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "duplicate",
			body: `prefix_rule(["git"], pattern = ["git"])`,
			want: "multiple values for argument pattern",
		},
		{
			name: "positional after named",
			body: `prefix_rule(pattern = ["git"], "prompt")`,
			want: "positional argument follows named argument",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writePolicy(t, tt.body)
			_, err := Check(&CheckOptions{Rules: []string{path}, Command: []string{"git"}})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Check() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestCheckRejectsUnknownPolicyFunction(t *testing.T) {
	path := writePolicy(t, `
prefix_rules(pattern = ["git"], decision = "allow")
`)
	_, err := Check(&CheckOptions{Rules: []string{path}, Command: []string{"git"}})
	if err == nil || !strings.Contains(err.Error(), "unknown execpolicy function prefix_rules") {
		t.Fatalf("Check() error = %v, want unknown policy function", err)
	}
}

func TestCheckRejectsUnexpectedRuleArgument(t *testing.T) {
	path := writePolicy(t, `
prefix_rule(
    pattern = ["git"],
    decision = "allow",
    examples = [["git"]],
)
`)
	_, err := Check(&CheckOptions{Rules: []string{path}, Command: []string{"git"}})
	if err == nil || !strings.Contains(err.Error(), "prefix_rule got unexpected argument examples") {
		t.Fatalf("Check() error = %v, want unexpected argument", err)
	}
}

func TestCheckIncludesJustification(t *testing.T) {
	path := writePolicy(t, `
prefix_rule(
    pattern = ["git", "push"],
    decision = "forbidden",
    justification = "pushing is blocked in this repo",
)
`)
	output, err := Check(&CheckOptions{Rules: []string{path}, Command: []string{"git", "push"}})
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	rendered, err := Render(output, false)
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if !strings.Contains(rendered, `"justification":"pushing is blocked in this repo"`) {
		t.Fatalf("rendered = %s", rendered)
	}
}

func TestCheckRejectsEmptyJustification(t *testing.T) {
	path := writePolicy(t, `
prefix_rule(
    pattern = ["git"],
    decision = "prompt",
    justification = "   ",
)
`)
	_, err := Check(&CheckOptions{Rules: []string{path}, Command: []string{"git"}})
	if err == nil || !strings.Contains(err.Error(), "justification cannot be empty") {
		t.Fatalf("Check() error = %v, want empty justification error", err)
	}
}

func TestCheckRejectsEmptyPattern(t *testing.T) {
	path := writePolicy(t, `
prefix_rule(
    pattern = [],
)
`)
	_, err := Check(&CheckOptions{Rules: []string{path}, Command: []string{"git"}})
	if err == nil || !strings.Contains(err.Error(), "pattern cannot be empty") {
		t.Fatalf("Check() error = %v, want empty pattern error", err)
	}
}

func TestCheckRejectsEmptyPatternAlternatives(t *testing.T) {
	path := writePolicy(t, `
prefix_rule(
    pattern = [["git"], []],
)
`)
	_, err := Check(&CheckOptions{Rules: []string{path}, Command: []string{"git"}})
	if err == nil || !strings.Contains(err.Error(), "pattern alternatives cannot be empty") {
		t.Fatalf("Check() error = %v, want empty alternatives error", err)
	}
}

func TestCheckRejectsNonStringPolicyValues(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "pattern token",
			body: `prefix_rule(pattern = [git])`,
			want: "pattern element must be a string literal",
		},
		{
			name: "decision",
			body: `prefix_rule(pattern = ["git"], decision = allow)`,
			want: "decision must be a string literal",
		},
		{
			name: "host path",
			body: `host_executable(name = "git", paths = [git])`,
			want: "list item must be a string literal",
		},
		{
			name: "example",
			body: `prefix_rule(pattern = ["git"], match = [git])`,
			want: "example must be a string literal",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writePolicy(t, tt.body)
			_, err := Check(&CheckOptions{Rules: []string{path}, Command: []string{"git"}})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Check() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestCheckValidatesMatchAndNotMatchExamples(t *testing.T) {
	path := writePolicy(t, `
prefix_rule(
    pattern = ["git", "status"],
    match = [["git", "status"], "git status"],
    not_match = [["git", "commit"], "git --config color.status=always status"],
)
`)
	if _, err := Check(&CheckOptions{Rules: []string{path}, Command: []string{"git", "status"}}); err != nil {
		t.Fatalf("Check() error = %v", err)
	}
}

func TestCheckRejectsUnmatchedMatchExample(t *testing.T) {
	path := writePolicy(t, `
prefix_rule(
    pattern = ["git", "status"],
    match = [["git", "commit"]],
)
`)
	_, err := Check(&CheckOptions{Rules: []string{path}, Command: []string{"git", "status"}})
	if err == nil || !strings.Contains(err.Error(), "example did not match") {
		t.Fatalf("Check() error = %v, want unmatched example error", err)
	}
}

func TestCheckRejectsMatchingNotMatchExample(t *testing.T) {
	path := writePolicy(t, `
prefix_rule(
    pattern = ["git"],
    not_match = ["git status"],
)
`)
	_, err := Check(&CheckOptions{Rules: []string{path}, Command: []string{"git", "status"}})
	if err == nil || !strings.Contains(err.Error(), "example matched when it should not") {
		t.Fatalf("Check() error = %v, want not_match example error", err)
	}
}

func TestCheckParsesNetworkRules(t *testing.T) {
	path := writePolicy(t, `
network_rule("Example.COM:443", "https_connect", "deny", "covered by proxy")
prefix_rule(
    pattern = ["git", "status"],
)
`)
	policy, err := LoadPolicies([]string{path})
	if err != nil {
		t.Fatalf("LoadPolicies() error = %v", err)
	}
	if len(policy.NetworkRules) != 1 {
		t.Fatalf("NetworkRules = %#v", policy.NetworkRules)
	}
	rule := policy.NetworkRules[0]
	if rule.Host != "example.com" || rule.Protocol != "https" || rule.Decision != DecisionForbidden || rule.Justification != "covered by proxy" {
		t.Fatalf("NetworkRule = %#v", rule)
	}
}

func TestCheckRejectsInvalidNetworkRule(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "wildcard host",
			body: `network_rule(host = "*", protocol = "http", decision = "allow")`,
			want: "wildcards are not allowed",
		},
		{
			name: "scheme host",
			body: `network_rule(host = "https://example.com/path", protocol = "https", decision = "allow")`,
			want: "without scheme or path",
		},
		{
			name: "invalid protocol",
			body: `network_rule(host = "example.com", protocol = "ftp", decision = "allow")`,
			want: "protocol must be one of",
		},
		{
			name: "empty justification",
			body: `network_rule(host = "example.com", protocol = "http", decision = "allow", justification = " ")`,
			want: "justification cannot be empty",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writePolicy(t, tt.body)
			_, err := LoadPolicies([]string{path})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("LoadPolicies() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestCheckNoMatchRendersEmptyMatchedRules(t *testing.T) {
	path := writePolicy(t, `
prefix_rule(
    pattern = ["git", "push"],
    decision = "forbidden",
)
`)
	output, err := Check(&CheckOptions{Rules: []string{path}, Command: []string{"git", "status"}})
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if output.Decision != nil {
		t.Fatalf("Decision = %#v, want nil", output.Decision)
	}
	rendered, err := Render(output, false)
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if rendered != `{"matchedRules":[]}` {
		t.Fatalf("rendered = %s", rendered)
	}
}

func TestCheckResolvesHostExecutable(t *testing.T) {
	git := filepath.Join(t.TempDir(), "git")
	path := writePolicy(t, `
host_executable("git", ["`+filepath.ToSlash(git)+`"])
prefix_rule(
    pattern = ["git", "status"],
)
`)
	output, err := Check(&CheckOptions{
		Rules:                  []string{path},
		Command:                []string{git, "status"},
		ResolveHostExecutables: true,
	})
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if len(output.MatchedRules) != 1 || output.MatchedRules[0].PrefixRuleMatch.ResolvedProgram == "" {
		t.Fatalf("MatchedRules = %#v", output.MatchedRules)
	}
}

func TestCheckHostExecutableRejectsNonAbsolutePath(t *testing.T) {
	path := writePolicy(t, `
host_executable(
    name = "git",
    paths = ["git"],
)
`)
	_, err := Check(&CheckOptions{Rules: []string{path}, Command: []string{"git"}})
	if err == nil || !strings.Contains(err.Error(), "host_executable paths must be absolute") {
		t.Fatalf("Check() error = %v, want non-absolute host_executable path error", err)
	}
}

func TestCheckHostExecutableRequiresPaths(t *testing.T) {
	path := writePolicy(t, `
host_executable(
    name = "git",
)
`)
	_, err := Check(&CheckOptions{Rules: []string{path}, Command: []string{"git"}})
	if err == nil || !strings.Contains(err.Error(), "host_executable missing paths") {
		t.Fatalf("Check() error = %v, want missing paths error", err)
	}
}

func TestCheckHostExecutableRejectsWrongBasename(t *testing.T) {
	rg := filepath.Join(t.TempDir(), "rg")
	path := writePolicy(t, `
host_executable(
    name = "git",
    paths = ["`+filepath.ToSlash(rg)+`"],
)
`)
	_, err := Check(&CheckOptions{Rules: []string{path}, Command: []string{"git"}})
	if err == nil || !strings.Contains(err.Error(), "must have basename") {
		t.Fatalf("Check() error = %v, want wrong basename error", err)
	}
}

func TestCheckHostExecutableEmptyAllowlistBlocksFallback(t *testing.T) {
	git := filepath.Join(t.TempDir(), "git")
	path := writePolicy(t, `
host_executable(
    name = "git",
    paths = [],
)
prefix_rule(
    pattern = ["git"],
    decision = "prompt",
)
`)
	output, err := Check(&CheckOptions{
		Rules:                  []string{path},
		Command:                []string{git},
		ResolveHostExecutables: true,
	})
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if len(output.MatchedRules) != 0 || output.Decision != nil {
		t.Fatalf("output = %#v, want no fallback match", output)
	}
}

func TestCheckHostExecutableLastDefinitionWins(t *testing.T) {
	dir := t.TempDir()
	firstGit := filepath.Join(dir, "first", "git")
	secondGit := filepath.Join(dir, "second", "git")
	path := writePolicy(t, `
host_executable(
    name = "git",
    paths = ["`+filepath.ToSlash(firstGit)+`"],
)
host_executable(
    name = "git",
    paths = ["`+filepath.ToSlash(secondGit)+`"],
)
prefix_rule(
    pattern = ["git"],
    decision = "prompt",
)
`)
	first, err := Check(&CheckOptions{
		Rules:                  []string{path},
		Command:                []string{firstGit},
		ResolveHostExecutables: true,
	})
	if err != nil {
		t.Fatalf("Check(first) error = %v", err)
	}
	if len(first.MatchedRules) != 0 {
		t.Fatalf("first MatchedRules = %#v, want last definition to replace first", first.MatchedRules)
	}

	second, err := Check(&CheckOptions{
		Rules:                  []string{path},
		Command:                []string{secondGit},
		ResolveHostExecutables: true,
	})
	if err != nil {
		t.Fatalf("Check(second) error = %v", err)
	}
	if len(second.MatchedRules) != 1 || second.Decision == nil || *second.Decision != DecisionPrompt {
		t.Fatalf("second output = %#v, want prompt fallback", second)
	}
}

func TestCheckExamplesHonorHostExecutableResolution(t *testing.T) {
	allowedGit := filepath.Join(t.TempDir(), "allowed", "git")
	otherGit := filepath.Join(t.TempDir(), "other", "git")
	path := writePolicy(t, `
prefix_rule(
    pattern = ["git", "status"],
    match = [["`+filepath.ToSlash(allowedGit)+`", "status"]],
    not_match = [["`+filepath.ToSlash(otherGit)+`", "status"]],
)
host_executable(
    name = "git",
    paths = ["`+filepath.ToSlash(allowedGit)+`"],
)
`)
	if _, err := Check(&CheckOptions{Rules: []string{path}, Command: []string{"git", "status"}}); err != nil {
		t.Fatalf("Check() error = %v", err)
	}
}

func TestCheckExactMatchPrecedesHostExecutableFallback(t *testing.T) {
	git := filepath.Join(t.TempDir(), "git")
	commandPath := filepath.ToSlash(git)
	path := writePolicy(t, `
host_executable(
    name = "git",
    paths = ["`+commandPath+`"],
)
prefix_rule(
    pattern = ["`+commandPath+`"],
    decision = "allow",
)
prefix_rule(
    pattern = ["git"],
    decision = "forbidden",
)
`)
	output, err := Check(&CheckOptions{
		Rules:                  []string{path},
		Command:                []string{commandPath},
		ResolveHostExecutables: true,
	})
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if output.Decision == nil || *output.Decision != DecisionAllow {
		t.Fatalf("Decision = %#v, want allow from exact match", output.Decision)
	}
	if len(output.MatchedRules) != 1 || output.MatchedRules[0].PrefixRuleMatch.ResolvedProgram != "" {
		t.Fatalf("MatchedRules = %#v, want exact match only", output.MatchedRules)
	}
}

func writePolicy(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "policy.rules")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}

func TestExecutableLookupKeyForPlatform(t *testing.T) {
	if got := executableLookupKeyForPlatform("Git.EXE", DangerousCommandPlatformWindows); got != "git" {
		t.Fatalf("windows lookup key = %q, want git", got)
	}
	if got := executableLookupKeyForPlatform("Git.EXE", DangerousCommandPlatformPosix); got != "Git.EXE" {
		t.Fatalf("posix lookup key = %q, want Git.EXE", got)
	}
}
