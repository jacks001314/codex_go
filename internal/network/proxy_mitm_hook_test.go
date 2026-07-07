package network

import (
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestValidateMITMHookConfigRequiresMITM(t *testing.T) {
	settings := DefaultProxySettings()
	settings.MITM = false
	settings.MITMHooks = []ProxyMITMHookConfig{testGitHubHook()}
	err := ValidateProxyMITMHookConfig(ProxyConfig{Network: settings})
	if err == nil || !stringsContains(err.Error(), "network.mitm_hooks requires network.mitm = true") {
		t.Fatalf("ValidateProxyMITMHookConfig error = %v", err)
	}
}

func TestCompileMITMHooksResolvesEnvAndMatchesRequest(t *testing.T) {
	settings := DefaultProxySettings()
	settings.MITM = true
	settings.MITMHooks = []ProxyMITMHookConfig{testGitHubHook()}
	hooks, err := CompileProxyMITMHooksWithResolvers(
		ProxyConfig{Network: settings},
		func(name string) (string, bool) {
			return "ghp-secret", name == "CODEX_GITHUB_TOKEN"
		},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	evaluation := EvaluateProxyMITMHooks(hooks, "API.GitHub.com", ProxyHTTPRequest{
		Method: "POST",
		Path:   "/repos/openai/codex/issues",
		Query:  "state=open",
		Headers: map[string][]string{
			"X-GitHub-Api-Version": {"2022-11-28-preview"},
		},
	})
	if evaluation.Kind != ProxyHookEvaluationMatched {
		t.Fatalf("evaluation = %#v", evaluation)
	}
	if got := evaluation.Actions.InjectRequestHeaders[0].Value; got != "Bearer ghp-secret" {
		t.Fatalf("injected value = %q", got)
	}
	if got := evaluation.Actions.StripRequestHeaders; !reflect.DeepEqual(got, []string{"Authorization"}) {
		t.Fatalf("strip headers = %#v", got)
	}
}

func TestCompileMITMHooksResolvesFileSecret(t *testing.T) {
	tempDir := t.TempDir()
	secretPath := filepath.Join(tempDir, "token.txt")
	writeTestFile(t, secretPath, "ghp-file-secret\n")
	hook := testGitHubHook()
	hook.Actions.InjectRequestHeaders[0].SecretEnvVar = nil
	hook.Actions.InjectRequestHeaders[0].SecretFile = &secretPath
	settings := DefaultProxySettings()
	settings.MITM = true
	settings.MITMHooks = []ProxyMITMHookConfig{hook}
	hooks, err := CompileProxyMITMHooks(ProxyConfig{Network: settings})
	if err != nil {
		t.Fatal(err)
	}
	got := hooks["api.github.com"][0].Actions.InjectRequestHeaders[0].Value
	if got != "Bearer ghp-file-secret" {
		t.Fatalf("file-backed value = %q", got)
	}
}

func TestMITMHookGlobAndLiteralMatching(t *testing.T) {
	hook := testGitHubHook()
	hook.Match.PathPrefixes = []string{"pattern:/repos/*/codex/issues*"}
	hook.Match.Query = map[string][]string{"state": {"pattern:op*"}}
	hook.Match.Headers = map[string][]string{"x-github-api-version": {"literal:pattern:*"}}
	settings := DefaultProxySettings()
	settings.MITM = true
	settings.MITMHooks = []ProxyMITMHookConfig{hook}
	hooks, err := CompileProxyMITMHooksWithResolvers(
		ProxyConfig{Network: settings},
		func(string) (string, bool) { return "abc", true },
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	matched := EvaluateProxyMITMHooks(hooks, "api.github.com", ProxyHTTPRequest{
		Method: "POST",
		Path:   "/repos/openai/codex/issues",
		Query:  "state=open",
		Headers: map[string][]string{
			"x-github-api-version": {"pattern:*"},
		},
	})
	if matched.Kind != ProxyHookEvaluationMatched {
		t.Fatalf("matched = %#v", matched)
	}
	nested := EvaluateProxyMITMHooks(hooks, "api.github.com", ProxyHTTPRequest{
		Method: "POST",
		Path:   "/repos/openai/private/codex/issues",
		Query:  "state=open",
		Headers: map[string][]string{
			"x-github-api-version": {"pattern:*"},
		},
	})
	if nested.Kind != ProxyHookEvaluationHookedNoMatch {
		t.Fatalf("nested = %#v", nested)
	}
}

func TestValidateMITMHookConfigRejectsUnsupportedAndInvalidFields(t *testing.T) {
	settings := DefaultProxySettings()
	settings.MITM = true
	hook := testGitHubHook()
	hook.Match.Body = &ProxyMITMHookBodyConfig{Raw: map[string]string{"repository": "openai/codex"}}
	settings.MITMHooks = []ProxyMITMHookConfig{hook}
	if err := ValidateProxyMITMHookConfig(ProxyConfig{Network: settings}); err == nil || !stringsContains(err.Error(), "match.body is reserved") {
		t.Fatalf("body matcher error = %v", err)
	}
	hook = testGitHubHook()
	hook.Host = "*.github.com"
	settings.MITMHooks = []ProxyMITMHookConfig{hook}
	if err := ValidateProxyMITMHookConfig(ProxyConfig{Network: settings}); err == nil || !stringsContains(err.Error(), "cannot contain wildcards") {
		t.Fatalf("wildcard host error = %v", err)
	}
	hook = testGitHubHook()
	hook.Match.PathPrefixes = []string{"pattern:/repos/["}
	settings.MITMHooks = []ProxyMITMHookConfig{hook}
	if err := ValidateProxyMITMHookConfig(ProxyConfig{Network: settings}); err == nil || !stringsContains(err.Error(), "invalid glob pattern") {
		t.Fatalf("glob error = %v", err)
	}
}

func TestNewHTTPRequestFromHTTP(t *testing.T) {
	req, err := http.NewRequest("PUT", "https://api.github.com/repos/openai/codex/issues?state=open", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("x-trace", "1")
	got := NewProxyHTTPRequestFromHTTP(req)
	if got.Method != "PUT" || got.Path != "/repos/openai/codex/issues" || got.Query != "state=open" {
		t.Fatalf("request = %#v", got)
	}
	if got.Headers["X-Trace"][0] != "1" {
		t.Fatalf("headers = %#v", got.Headers)
	}
}

func testGitHubHook() ProxyMITMHookConfig {
	envVar := "CODEX_GITHUB_TOKEN"
	return ProxyMITMHookConfig{
		Host: "api.github.com",
		Match: ProxyMITMHookMatchConfig{
			Methods:      []string{"POST", "PUT"},
			PathPrefixes: []string{"/repos/openai/"},
			Query:        map[string][]string{"state": {"open", "triage"}},
			Headers:      map[string][]string{"x-github-api-version": {"pattern:2022*preview"}},
		},
		Actions: ProxyMITMHookActionsConfig{
			StripRequestHeaders: []string{"authorization"},
			InjectRequestHeaders: []ProxyInjectedHeaderConfig{
				{Name: "authorization", SecretEnvVar: &envVar, Prefix: "Bearer "},
			},
		},
	}
}

func writeTestFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func stringsContains(value string, needle string) bool {
	return strings.Contains(value, needle)
}
