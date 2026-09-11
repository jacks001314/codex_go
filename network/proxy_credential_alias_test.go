package network

import (
	"regexp"
	"strings"
	"testing"
)

func aliasTestProvider(patterns []string) *ProxyCredentialProvider {
	return ConfiguredCredentialProvider("vendor", CredentialProviderConfig{
		Env:         []string{"VENDOR_TOKEN"},
		Patterns:    patterns,
		URLPrefixes: []string{"https://api.vendor.example"},
		Auth:        []CredentialAuthMethod{CredentialAuthBearer},
	})
}

// TestCredentialBrokerVirtualizesEmbeddedAliasesLikeRust mirrors #44066's alias
// discovery for configured providers: a credential-shaped token embedded in an
// unrelated environment value is replaced with a matching dummy and can still
// be substituted at an authorized destination.
func TestCredentialBrokerVirtualizesEmbeddedAliasesLikeRust(t *testing.T) {
	const realValue = "vk-0123456789abcdef"
	provider := aliasTestProvider([]string{"vk-[a-z0-9]{16}"})
	broker := NewProxyCredentialBrokerWithProviders(true, []*ProxyCredentialProvider{provider})

	env := map[string]string{
		"VENDOR_TOKEN": realValue,
		"OTHER_VAR":    "export TOKEN='" + realValue + "' # trailing",
	}
	broker.VirtualizeChildEnv(env)
	if strings.Contains(env["OTHER_VAR"], realValue) {
		t.Fatalf("embedded credential was not virtualized: %q", env["OTHER_VAR"])
	}
	embedded := regexp.MustCompile("vk-[a-z0-9]{16}").FindString(env["OTHER_VAR"])
	if embedded == "" || embedded == realValue {
		t.Fatalf("embedded dummy missing: %q", env["OTHER_VAR"])
	}

	headers := map[string][]string{"Authorization": {"Bearer " + embedded}}
	broker.InjectRequestHeadersForDestination("https", "api.vendor.example", 443, "/v1/models", headers)
	if got := headers["Authorization"]; len(got) != 1 || got[0] != "Bearer "+realValue {
		t.Fatalf("embedded alias injection = %#v", headers)
	}
}

func TestCredentialBrokerEmbeddedAliasGuardsLikeRust(t *testing.T) {
	const realValue = "vk-0123456789abcdef"

	t.Run("partial token is not a credential", func(t *testing.T) {
		broker := NewProxyCredentialBrokerWithProviders(true, []*ProxyCredentialProvider{aliasTestProvider([]string{"vk-[a-z0-9]{16}"})})
		env := map[string]string{"OTHER_VAR": "prefix" + realValue + "suffix"}
		broker.VirtualizeChildEnv(env)
		if env["OTHER_VAR"] != "prefix"+realValue+"suffix" {
			t.Fatalf("partial token rewritten: %q", env["OTHER_VAR"])
		}
	})

	t.Run("short match below the embedded minimum is ignored", func(t *testing.T) {
		broker := NewProxyCredentialBrokerWithProviders(true, []*ProxyCredentialProvider{aliasTestProvider([]string{"vk-[a-z0-9]{4}"})})
		env := map[string]string{"OTHER_VAR": "token vk-abcd here"}
		broker.VirtualizeChildEnv(env)
		if env["OTHER_VAR"] != "token vk-abcd here" {
			t.Fatalf("short match rewritten: %q", env["OTHER_VAR"])
		}
	})

	t.Run("distinctive prefix allows a short embedded credential", func(t *testing.T) {
		broker := NewProxyCredentialBrokerWithProviders(true, []*ProxyCredentialProvider{aliasTestProvider([]string{"sk-live-[a-z]{4}"})})
		env := map[string]string{"OTHER_VAR": "token sk-live-abcd here"}
		broker.VirtualizeChildEnv(env)
		if strings.Contains(env["OTHER_VAR"], "sk-live-abcd") {
			t.Fatalf("distinctive short credential not virtualized: %q", env["OTHER_VAR"])
		}
		// The same length without a distinctive literal prefix stays ignored.
		plain := NewProxyCredentialBrokerWithProviders(true, []*ProxyCredentialProvider{aliasTestProvider([]string{"[a-z]{4}-[0-9]{4}"})})
		plainEnv := map[string]string{"OTHER_VAR": "token abcd-1234 here"}
		plain.VirtualizeChildEnv(plainEnv)
		if plainEnv["OTHER_VAR"] != "token abcd-1234 here" {
			t.Fatalf("non-distinctive short match rewritten: %q", plainEnv["OTHER_VAR"])
		}
	})

	t.Run("virtualization is idempotent for dummies", func(t *testing.T) {
		broker := NewProxyCredentialBrokerWithProviders(true, []*ProxyCredentialProvider{aliasTestProvider([]string{"vk-[a-z0-9]{16}"})})
		env := map[string]string{"OTHER_VAR": "token " + realValue}
		broker.VirtualizeChildEnv(env)
		first := env["OTHER_VAR"]
		broker.VirtualizeChildEnv(env)
		if env["OTHER_VAR"] != first {
			t.Fatalf("re-virtualization changed the value: %q -> %q", first, env["OTHER_VAR"])
		}
	})
}
