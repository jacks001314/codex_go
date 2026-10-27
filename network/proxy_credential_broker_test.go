package network

import (
	"reflect"
	"strings"
	"testing"
)

func TestCredentialBrokerVirtualizesSupportedCredentials(t *testing.T) {
	broker := NewProxyCredentialBroker(true)
	githubToken := "github_pat_11AA0bbCC_abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGH"
	openAIKey := "sk-proj-abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_"
	env := map[string]string{
		"GH_TOKEN":       githubToken,
		"OPENAI_API_KEY": openAIKey,
	}
	broker.VirtualizeChildEnv(env)
	if env[CredentialBrokerActiveEnvKey] != "1" {
		t.Fatalf("broker active env missing")
	}
	assertCredentialShape(t, githubToken, env["GH_TOKEN"], "github_pat_")
	assertCredentialShape(t, openAIKey, env["OPENAI_API_KEY"], "sk-proj-")
	env["OPENAI_API_KEY"] = "sk-user-override"
	if got := ProxyBrokeredCredentialDummyEnvKeys(env); !reflect.DeepEqual(got, []string{"GH_TOKEN"}) {
		t.Fatalf("dummy keys = %#v", got)
	}
}

// TestCredentialBrokerShapesMaskedDummiesLikeRust mirrors Rust #56297: short,
// opaque, punctuation-carrying and ordinary built-in credentials all receive a
// fresh dummy with the provider prefix, the `masked` marker and no copied
// punctuation.
func TestCredentialBrokerShapesMaskedDummiesLikeRust(t *testing.T) {
	cases := []struct {
		name   string
		envVar string
		value  string
		prefix string
	}{
		{name: "openai projected key", envVar: "OPENAI_API_KEY", value: "sk-proj-abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_", prefix: "sk-proj-"},
		{name: "openai short key", envVar: "OPENAI_API_KEY", value: "sk-proj-short", prefix: "sk-proj-"},
		{name: "openai classic key", envVar: "OPENAI_API_KEY", value: "sk-abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789", prefix: "sk-"},
		{name: "github fine grained", envVar: "GH_TOKEN", value: "github_pat_11AA0bbCC_abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGH", prefix: "github_pat_"},
		{name: "github classic", envVar: "GH_TOKEN", value: "ghp_abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMN", prefix: "ghp_"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			broker := NewProxyCredentialBroker(true)
			env := map[string]string{test.envVar: test.value}
			broker.VirtualizeChildEnv(env)
			dummy := env[test.envVar]
			assertCredentialShape(t, test.value, dummy, test.prefix)
			// The marker is visible in the child environment, and the real value
			// survives only for the proxy's own substitution.
			if !strings.Contains(dummy, "masked") {
				t.Fatalf("dummy %q does not mark masking", dummy)
			}
			restored := map[string]string{test.envVar: dummy}
			broker.RestoreAndDisableChildEnv(restored)
			if restored[test.envVar] != test.value {
				t.Fatalf("restored = %q, want the real credential", restored[test.envVar])
			}
		})
	}
}

// TestCredentialBrokerRejectsKnownDummyCollisionsLikeRust mirrors Rust #56297:
// a candidate that duplicates a credential the broker already handed out is
// rejected instead of being re-registered.
func TestCredentialBrokerRejectsKnownDummyCollisionsLikeRust(t *testing.T) {
	first := constantDummyProvider("vendor_masked_abcdefghijklmnopqrstuvwxyz")
	second := constantDummyProvider("vendor_masked_abcdefghijklmnopqrstuvwxyz")
	second.Sources = []ProxyCredentialSource{{
		EnvVars: []string{"B_TOKEN"},
		HostBinding: func(map[string]string) (ProxyCredentialHostBinding, bool) {
			return ProxyCredentialHostBinding{ExactHosts: []string{"api.b.example"}}, true
		},
	}}
	broker := NewProxyCredentialBrokerWithProviders(true, []*ProxyCredentialProvider{first, second})
	env := map[string]string{"A_TOKEN": "a-real-secret-value", "B_TOKEN": "b-real-secret-value"}
	broker.VirtualizeChildEnv(env)
	if env["A_TOKEN"] != "vendor_masked_abcdefghijklmnopqrstuvwxyz" {
		t.Fatalf("first dummy = %q", env["A_TOKEN"])
	}
	if env["B_TOKEN"] != "b-real-secret-value" {
		t.Fatalf("second dummy = %q, want the colliding candidate rejected", env["B_TOKEN"])
	}
}

func TestCredentialBrokerInjectsMatchingDummyHeader(t *testing.T) {
	broker := NewProxyCredentialBroker(true)
	env := map[string]string{
		"GH_TOKEN":       "ghp-real-one",
		"GITHUB_TOKEN":   "ghp-real-two",
		"OPENAI_API_KEY": "sk-real",
	}
	broker.VirtualizeChildEnv(env)
	headers := map[string][]string{}
	broker.InjectRequestHeaders("api.github.com", headers)
	if authorizationValue(headers) != "" {
		t.Fatalf("ambiguous credential should not inject: %#v", headers)
	}
	headers = bearerHeaders(env["GITHUB_TOKEN"])
	broker.InjectRequestHeaders("api.github.com", headers)
	if got := authorizationValue(headers); got != "Bearer ghp-real-two" {
		t.Fatalf("github authorization = %q", got)
	}
	openAIHeaders := bearerHeaders(env["OPENAI_API_KEY"])
	broker.InjectRequestHeaders("api.openai.com", openAIHeaders)
	if got := authorizationValue(openAIHeaders); got != "Bearer sk-real" {
		t.Fatalf("openai authorization = %q", got)
	}
}

func TestCredentialBrokerHostBindings(t *testing.T) {
	broker := NewProxyCredentialBroker(true)
	env := map[string]string{
		"GH_HOST":             "github.example.com",
		"GH_TOKEN":            "ghp-cloud-real",
		"GH_ENTERPRISE_TOKEN": "ghp-enterprise-real",
	}
	broker.VirtualizeChildEnv(env)
	if !broker.HostRequiresMITM("github.example.com") || !broker.HostRequiresMITM("api.github.com") {
		t.Fatalf("expected both enterprise and cloud hosts to require mitm")
	}
	enterpriseHeaders := bearerHeaders(env["GH_ENTERPRISE_TOKEN"])
	broker.InjectRequestHeaders("github.example.com", enterpriseHeaders)
	if got := authorizationValue(enterpriseHeaders); got != "Bearer ghp-enterprise-real" {
		t.Fatalf("enterprise authorization = %q", got)
	}
	cloudHeaders := bearerHeaders(env["GH_TOKEN"])
	broker.InjectRequestHeaders("github.example.com", cloudHeaders)
	if got := authorizationValue(cloudHeaders); got == "Bearer ghp-cloud-real" {
		t.Fatalf("cloud credential should not bind to GHES host")
	}
}

func TestCredentialBrokerCloudGHESHostSuffix(t *testing.T) {
	broker := NewProxyCredentialBroker(true)
	env := map[string]string{"GH_HOST": "astemu.ghe.com", "GH_TOKEN": "ghp-real"}
	broker.VirtualizeChildEnv(env)
	headers := bearerHeaders(env["GH_TOKEN"])
	broker.InjectRequestHeaders("api.astemu.ghe.com", headers)
	if got := authorizationValue(headers); got != "Bearer ghp-real" {
		t.Fatalf("ghe cloud authorization = %q", got)
	}
}

func TestCredentialBrokerDisabledRemovesMarkers(t *testing.T) {
	broker := NewProxyCredentialBroker(false)
	env := map[string]string{
		CredentialBrokerActiveEnvKey: "1",
		BrokeredCredentialsEnvKey:    "[]",
		"OPENAI_API_KEY":             "sk-real",
	}
	broker.VirtualizeChildEnv(env)
	if _, ok := env[CredentialBrokerActiveEnvKey]; ok {
		t.Fatalf("active marker should be removed")
	}
	if _, ok := env[BrokeredCredentialsEnvKey]; ok {
		t.Fatalf("brokered marker should be removed")
	}
	if env["OPENAI_API_KEY"] != "sk-real" {
		t.Fatalf("credential should not be touched")
	}
	if got := ProxyBrokeredCredentialEnvKeys(env); got != nil {
		t.Fatalf("env keys = %#v", got)
	}
}

func TestCredentialBrokerEnvKeys(t *testing.T) {
	env := map[string]string{CredentialBrokerActiveEnvKey: "1"}
	keys := ProxyBrokeredCredentialEnvKeys(env)
	for _, want := range []string{"GH_HOST", "GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN", "OPENAI_API_KEY", "OPENAI_BASE_URL"} {
		if !sliceContains(keys, want) {
			t.Fatalf("missing %s in %#v", want, keys)
		}
	}
}

// The built-in OpenAI provider binds OPENAI_BASE_URL as a destination context
// key like Rust (network-proxy/src/credential_broker/providers/openai.rs), so a
// base-URL override is a provider env key rather than a plain child variable.
func TestCredentialBrokerTreatsOpenAIBaseURLAsProviderEnvKey(t *testing.T) {
	if !sliceContains(ProxyCredentialBrokerEnvKeys(), "OPENAI_BASE_URL") {
		t.Fatalf("provider env keys = %#v", ProxyCredentialBrokerEnvKeys())
	}
	broker := NewProxyCredentialBroker(true)
	broker.SetDestinationHints(map[string]string{"OPENAI_BASE_URL": "https://tenant.openai.example/v1"})
	child := map[string]string{"OPENAI_API_KEY": "sk-proj-0123456789abcdefghijklmnopqrstuvwxyzABCDEFGH"}
	broker.VirtualizeChildEnv(child)
	if _, leaked := child["OPENAI_BASE_URL"]; leaked {
		t.Fatalf("OPENAI_BASE_URL destination hint leaked into the child env: %#v", child)
	}
	if !broker.HostRequiresMITM("tenant.openai.example") {
		t.Fatal("the OPENAI_BASE_URL hint did not contribute a destination")
	}
	if child["OPENAI_API_KEY"] == "sk-proj-0123456789abcdefghijklmnopqrstuvwxyzABCDEFGH" {
		t.Fatalf("credential was not virtualized: %#v", child)
	}
}

// An untrusted OPENAI_BASE_URL invalidates the OpenAI binding instead of
// brokering the credential to the default host (Rust invalidates_host_binding).
func TestCredentialBrokerOpenAIBaseURLInvalidatesUntrustedBinding(t *testing.T) {
	broker := NewProxyCredentialBroker(true)
	broker.SetDestinationHints(map[string]string{"OPENAI_BASE_URL": "http://tenant.openai.example/v1"})
	child := map[string]string{"OPENAI_API_KEY": "sk-proj-0123456789abcdefghijklmnopqrstuvwxyzABCDEFGH"}
	broker.VirtualizeChildEnv(child)
	if child["OPENAI_API_KEY"] != "sk-proj-0123456789abcdefghijklmnopqrstuvwxyzABCDEFGH" {
		t.Fatalf("an untrusted base URL still virtualized the credential: %#v", child)
	}
	if broker.HostRequiresMITM("api.openai.com") {
		t.Fatal("an untrusted base URL still bound the default host")
	}
}

// The trusted configured openai_base_url host is bound alongside the default
// host (Rust credential_broker_openai_host passed to openai_provider::host_binding).
func TestCredentialBrokerConfiguredOpenAIHostBinding(t *testing.T) {
	broker := NewProxyCredentialBrokerWithOpenAIHost(true, nil, "gateway.example")
	child := map[string]string{"OPENAI_API_KEY": "sk-proj-0123456789abcdefghijklmnopqrstuvwxyzABCDEFGH"}
	broker.VirtualizeChildEnv(child)
	if child["OPENAI_API_KEY"] == "sk-proj-0123456789abcdefghijklmnopqrstuvwxyzABCDEFGH" {
		t.Fatalf("credential was not virtualized: %#v", child)
	}
	for _, host := range []string{"api.openai.com", "gateway.example"} {
		if !broker.HostRequiresMITM(host) {
			t.Fatalf("configured binding did not cover %s", host)
		}
	}
	if broker.HostRequiresMITM("other.example") {
		t.Fatal("the configured binding covered an unrelated host")
	}
}

// The configured host is normalized, deduplicated against the default host, and
// still joined by a trusted OPENAI_BASE_URL host.
func TestCredentialBrokerConfiguredOpenAIHostBindingOrderLikeRust(t *testing.T) {
	provider := openAICredentialProvider("API.OpenAI.com")
	binding, ok := provider.Sources[0].HostBinding(map[string]string{"OPENAI_BASE_URL": "https://tenant.openai.example/v1"})
	if !ok {
		t.Fatal("the binding was dropped")
	}
	want := []string{"api.openai.com", "tenant.openai.example"}
	if len(binding.ExactHosts) != len(want) {
		t.Fatalf("hosts = %#v, want %#v", binding.ExactHosts, want)
	}
	for index := range want {
		if binding.ExactHosts[index] != want[index] {
			t.Fatalf("hosts = %#v, want %#v", binding.ExactHosts, want)
		}
	}
	// An untrusted OPENAI_BASE_URL still disables the whole binding.
	if _, ok := provider.Sources[0].HostBinding(map[string]string{"OPENAI_BASE_URL": "http://tenant.openai.example/v1"}); ok {
		t.Fatal("an untrusted base URL did not disable the configured binding")
	}
}

func bearerHeaders(value string) map[string][]string {
	return map[string][]string{"Authorization": {"Bearer " + value}}
}

func authorizationValue(headers map[string][]string) string {
	if values := headers["Authorization"]; len(values) > 0 {
		return values[0]
	}
	if values := headers["authorization"]; len(values) > 0 {
		return values[0]
	}
	return ""
}

// assertCredentialShape mirrors Rust #56297: a built-in dummy keeps its
// provider prefix, marks itself with `masked`, carries over 128 random
// alphanumeric bits after the marker and copies no punctuation from the real
// credential.
func assertCredentialShape(t *testing.T, realValue string, dummyValue string, prefix string) {
	t.Helper()
	if dummyValue == realValue {
		t.Fatalf("dummy value should differ from real value")
	}
	marker := prefix + "masked"
	if !strings.HasPrefix(dummyValue, marker) {
		t.Fatalf("dummy %q missing masked marker %q", dummyValue, marker)
	}
	if len(dummyValue) < len(marker)+minDummyRandomAlphanumeric {
		t.Fatalf("dummy %q carries fewer than %d random bytes", dummyValue, minDummyRandomAlphanumeric)
	}
	if len(dummyValue) < len(realValue) {
		t.Fatalf("dummy len = %d want at least %d", len(dummyValue), len(realValue))
	}
	for index := len(marker); index < len(dummyValue); index++ {
		if !isASCIIAlphanumeric(dummyValue[index]) {
			t.Fatalf("dummy byte %q at %d should be alphanumeric", dummyValue[index], index)
		}
	}
}

func sliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
