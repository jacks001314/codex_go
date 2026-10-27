package network

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/elazarl/goproxy"
)

func scopedPlaceholderSettings(placeholder string) ProxySettings {
	settings := DefaultProxySettings()
	settings.Enabled = true
	settings.ProxyURL = "http://127.0.0.1:0"
	settings.SocksURL = "http://127.0.0.1:0"
	settings.AllowLocalBinding = true
	settings.MITM = true
	settings.SetAllowedDomains([]string{"api.example.test"})
	settings.MITMHooks = []ProxyMITMHookConfig{{
		Host: "api.example.test",
		Match: ProxyMITMHookMatchConfig{
			Schemes:      []string{"https"},
			Ports:        []int{443},
			Methods:      []string{"POST"},
			PathPrefixes: []string{"/v1/"},
		},
		Actions: ProxyMITMHookActionsConfig{
			InjectRequestHeaders: []ProxyInjectedHeaderConfig{{
				Placeholder:  &placeholder,
				Name:         "X-Api-Key",
				SecretEnvVar: stringPointer("CODEX_TEST_HOOK_SECRET"),
			}},
		},
	}}
	return settings
}

func stringPointer(value string) *string { return &value }

func TestScopedHookMatchesOnlyItsOrigin(t *testing.T) {
	t.Setenv("CODEX_TEST_HOOK_SECRET", "host-secret")
	hooks, err := CompileProxyMITMHooks(ProxyConfig{Network: scopedPlaceholderSettings("guest-placeholder")})
	if err != nil {
		t.Fatalf("compile hooks: %v", err)
	}
	request := ProxyHTTPRequest{Method: http.MethodPost, Path: "/v1/items"}
	if got := EvaluateProxyMITMHooksAtOrigin(hooks, "api.example.test", ProxyHookOrigin{Scheme: "https", Port: 443}, request); got.Kind != ProxyHookEvaluationMatched {
		t.Fatalf("matching origin = %v", got.Kind)
	}
	for _, origin := range []ProxyHookOrigin{{Scheme: "http", Port: 443}, {Scheme: "https", Port: 8443}} {
		if got := EvaluateProxyMITMHooksAtOrigin(hooks, "api.example.test", origin, request); got.Kind != ProxyHookEvaluationHookedNoMatch {
			t.Fatalf("origin %#v = %v", origin, got.Kind)
		}
	}
}

func TestInvalidHookOriginConstraintsAreRejected(t *testing.T) {
	settings := scopedPlaceholderSettings("guest-placeholder")
	settings.MITMHooks[0].Match.Schemes = []string{"ftp"}
	if err := ValidateProxyMITMHookConfig(ProxyConfig{Network: settings}); err == nil {
		t.Fatalf("expected invalid scheme rejection")
	}
	settings = scopedPlaceholderSettings("guest-placeholder")
	settings.MITMHooks[0].Match.Ports = []int{0}
	if err := ValidateProxyMITMHookConfig(ProxyConfig{Network: settings}); err == nil {
		t.Fatalf("expected zero port rejection")
	}
}

func TestCredentialHooksRejectAmbiguousQueryAndHeaderValues(t *testing.T) {
	exact := "guest-placeholder"
	settings := scopedPlaceholderSettings(exact)
	settings.MITMHooks[0].Match.Schemes = nil
	settings.MITMHooks[0].Match.Ports = nil
	settings.MITMHooks[0].Match.Query = map[string][]string{"tenant": {"good"}}
	settings.MITMHooks[0].Match.Headers = map[string][]string{"X-Tenant": {"pattern:good*"}}
	settings.MITMHooks[0].Match.Methods = []string{"post", "ReadItem"}
	t.Setenv("CODEX_TEST_HOOK_SECRET", "host-secret")
	hooks, err := CompileProxyMITMHooks(ProxyConfig{Network: settings})
	if err != nil {
		t.Fatalf("compile hooks: %v", err)
	}
	for _, testCase := range []struct {
		name    string
		query   string
		tenants []string
		allowed bool
	}{
		{"single value", "tenant=good", []string{"good"}, true},
		{"duplicate bad then good", "tenant=bad&tenant=good", []string{"good"}, false},
		{"duplicate good then bad", "tenant=good&tenant=bad", []string{"good"}, false},
		{"encoded duplicate", "tenant=good&%74enant=bad", []string{"good"}, false},
		{"two header values", "tenant=good", []string{"good", "bad"}, false},
		{"comma list", "tenant=good", []string{"good, bad"}, false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			headers := map[string][]string{"X-Tenant": append([]string(nil), testCase.tenants...)}
			request := ProxyHTTPRequest{Method: http.MethodPost, Path: "/v1/items", Query: testCase.query, Headers: headers}
			git := EvaluateProxyMITMHooks(hooks, "api.example.test", request)
			if (git.Kind == ProxyHookEvaluationMatched) != testCase.allowed {
				t.Fatalf("%s %v = %v", testCase.query, testCase.tenants, git.Kind)
			}
			if !testCase.allowed {
				return
			}
			for method, matched := range map[string]bool{"post": false, "POST": true, "ReadItem": true, "readitem": false, "READITEM": false} {
				request.Method = method
				got := EvaluateProxyMITMHooks(hooks, "api.example.test", request)
				if (got.Kind == ProxyHookEvaluationMatched) != matched {
					t.Fatalf("method %q = %v", method, got.Kind)
				}
			}
		})
	}
}

func TestInjectedSecretHeadersRejectUnsafeNamesLikeRust(t *testing.T) {
	secret := "CODEX_TEST_HOOK_SECRET"
	for _, header := range []ProxyInjectedHeaderConfig{
		{Name: "User-Agent", SecretEnvVar: &secret},
		{Name: "User-Agent", Placeholder: stringPointer("guest"), SecretEnvVar: &secret},
		{Name: "Host", Placeholder: stringPointer("guest"), SecretEnvVar: &secret},
		{Name: "Content-Length", Placeholder: stringPointer("guest"), SecretEnvVar: &secret},
		{Name: "Transfer-Encoding", Placeholder: stringPointer("guest"), SecretEnvVar: &secret},
		{Name: "Connection", Placeholder: stringPointer("guest"), SecretEnvVar: &secret},
		{Name: "Trailer", Placeholder: stringPointer("guest"), SecretEnvVar: &secret},
		{Name: "Upgrade", Placeholder: stringPointer("guest"), SecretEnvVar: &secret},
		{Name: "X-Api-Key", Placeholder: stringPointer(""), SecretEnvVar: &secret},
	} {
		if err := validateInjectedHeaders([]ProxyInjectedHeaderConfig{header}); err == nil {
			t.Fatalf("expected rejection for %#v", header)
		}
	}
}

// TestMITMRequestRequiresExactPlaceholderLikeRust mirrors #56003: a
// placeholder-bound secret is substituted only for HTTPS/443 with exactly one
// matching whole header value.
func TestMITMRequestRequiresExactPlaceholderLikeRust(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("CODEX_TEST_HOOK_SECRET", "host-secret")
	prepared := startHookedHostAliasProxy(t, scopedPlaceholderSettings("guest-placeholder"), map[string]string{})
	for _, testCase := range []struct {
		name   string
		values []string
		denied bool
	}{
		{"exact placeholder", []string{"guest-placeholder"}, false},
		{"missing", nil, true},
		{"mismatched", []string{"guest"}, true},
		{"duplicate", []string{"guest-placeholder", "guest-placeholder"}, true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			request, err := http.NewRequest(http.MethodPost, "https://api.example.test/v1/items", strings.NewReader("{}"))
			if err != nil {
				t.Fatal(err)
			}
			for _, value := range testCase.values {
				request.Header.Add("X-Api-Key", value)
			}
			inner := request.Clone(context.WithValue(request.Context(), proxyMITMContextKey{}, proxyMITMRequestContext{
				host: "api.example.test",
				port: 443,
			}))
			inner.URL.Scheme = "https"
			outgoing, response := prepared.server.handleHTTPRequest(inner, &goproxy.ProxyCtx{})
			if testCase.denied {
				if response == nil {
					t.Fatalf("expected denial")
				}
				return
			}
			if response != nil {
				t.Fatalf("unexpected denial %v", response)
			}
			if got := outgoing.Header.Get("X-Api-Key"); got != "host-secret" {
				t.Fatalf("substituted value = %q", got)
			}
		})
	}
}

func TestMITMRequestDeniesPlaceholderOnNonDefaultPort(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("CODEX_TEST_HOOK_SECRET", "host-secret")
	settings := scopedPlaceholderSettings("guest-placeholder")
	settings.MITMHooks[0].Match.Schemes = nil
	settings.MITMHooks[0].Match.Ports = nil
	prepared := startHookedHostAliasProxy(t, settings, map[string]string{})
	request, err := http.NewRequest(http.MethodPost, "https://api.example.test/v1/items", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-Api-Key", "guest-placeholder")
	inner := request.Clone(context.WithValue(request.Context(), proxyMITMContextKey{}, proxyMITMRequestContext{
		host: "api.example.test",
		port: 8443,
	}))
	inner.URL.Scheme = "https"
	if _, response := prepared.server.handleHTTPRequest(inner, &goproxy.ProxyCtx{}); response == nil {
		t.Fatalf("expected denial for placeholder outside HTTPS/443")
	}
}

func TestSecretFilesAreBoundedLikeRust(t *testing.T) {
	path := t.TempDir() + "\\secret"
	writeTestFile(t, path, strings.Repeat("a", ProxySecretFileMaxBytes+1))
	if _, err := readProxySecretFile(path); err == nil {
		t.Fatalf("expected oversize secret rejection")
	}
	writeTestFile(t, path, strings.Repeat("a", ProxySecretFileMaxBytes))
	if _, err := readProxySecretFile(path); err != nil {
		t.Fatalf("unexpected rejection at limit: %v", err)
	}
}
