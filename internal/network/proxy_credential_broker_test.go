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
	for _, want := range []string{"GH_HOST", "GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN", "OPENAI_API_KEY"} {
		if !sliceContains(keys, want) {
			t.Fatalf("missing %s in %#v", want, keys)
		}
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

func assertCredentialShape(t *testing.T, realValue string, dummyValue string, prefix string) {
	t.Helper()
	if dummyValue == realValue {
		t.Fatalf("dummy value should differ from real value")
	}
	if len(dummyValue) != len(realValue) {
		t.Fatalf("dummy len = %d want %d", len(dummyValue), len(realValue))
	}
	if !strings.HasPrefix(dummyValue, prefix) {
		t.Fatalf("dummy %q missing prefix %q", dummyValue, prefix)
	}
	for index := len(prefix); index < len(realValue); index++ {
		real := realValue[index]
		dummy := dummyValue[index]
		if isASCIIAlphanumeric(real) {
			if !isASCIIAlphanumeric(dummy) {
				t.Fatalf("dummy byte %q at %d should be alphanumeric", dummy, index)
			}
			continue
		}
		if real != dummy {
			t.Fatalf("dummy byte %q at %d should preserve %q", dummy, index, real)
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
