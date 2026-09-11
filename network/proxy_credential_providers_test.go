package network

import (
	"reflect"
	"strings"
	"testing"
)

func TestProxyConfigParsesConfiguredCredentialProvidersLikeRust(t *testing.T) {
	config, err := ProxyConfigFromConfigValues(map[string]any{
		"features": map[string]any{"network_proxy": map[string]any{
			"enabled":           true,
			"credential_broker": true,
			"mitm":              true,
			"credentials": map[string]any{
				"vendor": map[string]any{
					"env":                 []any{"VENDOR_TOKEN"},
					"patterns":            []any{"vk-[a-z0-9]{16}"},
					"url_prefixes":        []any{"https://api.vendor.example/v1"},
					"url_prefix_from_env": "VENDOR_API_BASE",
					"auth":                []any{"bearer"},
				},
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	provider, ok := config.Network.CredentialProviders["vendor"]
	if !ok {
		t.Fatalf("providers = %#v", config.Network.CredentialProviders)
	}
	if !reflect.DeepEqual(provider.Env, []string{"VENDOR_TOKEN"}) || !reflect.DeepEqual(provider.Patterns, []string{"vk-[a-z0-9]{16}"}) {
		t.Fatalf("provider = %#v", provider)
	}
	if provider.URLPrefixFromEnv == nil || *provider.URLPrefixFromEnv != "VENDOR_API_BASE" {
		t.Fatalf("url_prefix_from_env = %#v", provider.URLPrefixFromEnv)
	}
	if !reflect.DeepEqual(provider.Auth, []CredentialAuthMethod{CredentialAuthBearer}) {
		t.Fatalf("auth = %#v", provider.Auth)
	}
	destinations, err := ParseCredentialDestinations(provider)
	if err != nil {
		t.Fatal(err)
	}
	if len(destinations) != 1 || destinations[0].Scheme != "https" || destinations[0].Host != "api.vendor.example" || destinations[0].Port != 443 || destinations[0].PathPrefix != "/v1" {
		t.Fatalf("destinations = %#v", destinations)
	}
}

func TestCredentialDestinationParsingLikeRust(t *testing.T) {
	cases := []struct {
		value string
		want  CredentialDestination
	}{
		{value: "api.example.com", want: CredentialDestination{Scheme: "https", Host: "api.example.com", Port: 443}},
		{value: "localhost:8080", want: CredentialDestination{Scheme: "http", Host: "localhost", Port: 8080}},
		{value: "127.0.0.1", want: CredentialDestination{Scheme: "http", Host: "127.0.0.1", Port: 80}},
		{value: "http://127.0.0.1:8080", want: CredentialDestination{Scheme: "http", Host: "127.0.0.1", Port: 8080}},
		{value: "https://api.example.com:8443/prefix", want: CredentialDestination{Scheme: "https", Host: "api.example.com", Port: 8443, PathPrefix: "/prefix"}},
		{value: "*.example.com", want: CredentialDestination{Scheme: "https", Host: "example.com", Wildcard: true, Port: 443}},
	}
	for _, tc := range cases {
		got, err := parseCredentialDestination(tc.value)
		if err != nil {
			t.Fatalf("parseCredentialDestination(%q) error = %v", tc.value, err)
		}
		if got != tc.want {
			t.Fatalf("parseCredentialDestination(%q) = %#v, want %#v", tc.value, got, tc.want)
		}
	}
	if _, err := parseCredentialDestination("ftp://example.com"); err == nil {
		t.Fatal("ftp destination should be rejected")
	}
	if _, err := parseCredentialDestination("http://api.example.com"); err == nil {
		t.Fatal("plaintext http for a non-loopback host should be rejected")
	}
	if _, err := parseCredentialDestination("https://user@api.example.com"); err == nil {
		t.Fatal("destination with user information should be rejected")
	}
}

func TestCredentialDestinationMatchingLikeRust(t *testing.T) {
	destination, err := parseCredentialDestination("https://api.vendor.example/v1")
	if err != nil {
		t.Fatal(err)
	}
	if !destination.MatchesRequest("https", "api.vendor.example", 443, "/v1/accounts") {
		t.Fatal("matching request rejected")
	}
	if destination.MatchesRequest("http", "api.vendor.example", 443, "/v1/accounts") {
		t.Fatal("http request should not match an https destination")
	}
	if destination.MatchesRequest("https", "api.vendor.example", 443, "/v2/accounts") {
		t.Fatal("path outside the prefix should not match")
	}
	if destination.MatchesRequest("https", "api.vendor.example", 443, "/v1/../v2") {
		t.Fatal("unsafe path should not match")
	}
	if !destination.RequiresMITM("api.vendor.example", 443) || destination.RequiresMITM("api.vendor.example", 8443) {
		t.Fatal("requires-mitm mismatch")
	}
	wildcard, err := parseCredentialDestination("*.vendor.example")
	if err != nil {
		t.Fatal(err)
	}
	if !wildcard.MatchesHost("tenant.vendor.example", 443) || wildcard.MatchesHost("vendor.example", 443) || wildcard.MatchesHost("evilvendor.example", 443) {
		t.Fatal("wildcard host matching mismatch")
	}
}

func TestCredentialProviderValidationLikeRust(t *testing.T) {
	valid := map[string]any{
		"env":          []any{"VENDOR_TOKEN"},
		"patterns":     []any{"vk-[a-z0-9]{16}"},
		"url_prefixes": []any{"https://api.vendor.example"},
		"auth":         []any{"bearer"},
	}
	cases := []struct {
		name   string
		mutate func(map[string]any)
		want   string
	}{
		{name: "missing env", mutate: func(m map[string]any) { delete(m, "env") }, want: "no environment keys"},
		{name: "missing patterns", mutate: func(m map[string]any) { delete(m, "patterns") }, want: "no credential patterns"},
		{name: "missing prefixes", mutate: func(m map[string]any) { delete(m, "url_prefixes") }, want: "no destination URL prefixes"},
		{name: "invalid env key", mutate: func(m map[string]any) { m["env"] = []any{"1BAD"} }, want: "invalid environment key"},
		{name: "builtin overlap", mutate: func(m map[string]any) { m["env"] = []any{"GH_TOKEN"} }, want: "overlaps a built-in credential source"},
		{name: "invalid destination", mutate: func(m map[string]any) { m["url_prefixes"] = []any{"ftp://example.com"} }, want: "invalid destination"},
		{name: "header without header auth", mutate: func(m map[string]any) { m["header"] = "X-Token" }, want: "header name exactly when using header authentication"},
		{name: "prefix without header", mutate: func(m map[string]any) { m["prefix"] = "Bearer " }, want: "requires header authentication to use a header prefix"},
		{name: "invalid auth", mutate: func(m map[string]any) { m["auth"] = []any{"digest"} }, want: "invalid authentication method"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			table := map[string]any{}
			for key, value := range valid {
				table[key] = value
			}
			tc.mutate(table)
			_, err := ParseCredentialProviderConfigs(map[string]any{"vendor": table})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want containing %q", err, tc.want)
			}
		})
	}

	if _, err := ParseCredentialProviderConfigs([]any{}); err == nil || !strings.Contains(err.Error(), "must be a table") {
		t.Fatalf("non-table credentials error = %v", err)
	}
}
