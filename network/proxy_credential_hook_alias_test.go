package network

// Rust #52661 (network-proxy/src/credential_broker.rs
// request_matches_hooked_host_alias + runtime.rs evaluate_mitm_hook_request):
// a brokered credential bound to a host with MITM hooks must not be injected at
// an alias host that has no hooks, because replacing the dummy there would hand
// the real credential to a host whose hook policy was never enforced.

import (
	"context"
	"net/http"
	"testing"

	"github.com/elazarl/goproxy"
)

const hookedHostAliasRealToken = "ghp-cloud-real"

// TestCredentialBrokerHookedHostAliasDetectionLikeRust mirrors the Rust unit
// cases selected_openai_credential_matches_ipv6_alias_with_plaintext_fallback
// and the alias assertions added to
// openai_credentials_bind_only_to_default_and_configured_trusted_hosts.
func TestCredentialBrokerHookedHostAliasDetectionLikeRust(t *testing.T) {
	broker := NewProxyCredentialBroker(true)
	env := map[string]string{
		"GH_TOKEN":            hookedHostAliasRealToken,
		"OPENAI_API_KEY":      "sk-real",
		"GH_HOST":             "enterprise.example",
		"GH_ENTERPRISE_TOKEN": "ghp-enterprise-real",
	}
	broker.VirtualizeChildEnv(env)
	github := bearerHeaders(env["GH_TOKEN"])
	openai := bearerHeaders(env["OPENAI_API_KEY"])
	enterprise := bearerHeaders(env["GH_ENTERPRISE_TOKEN"])

	for _, testCase := range []struct {
		name        string
		destination string
		headers     map[string][]string
		hookedHosts []string
		want        bool
	}{
		{
			name:        "cloud credential used at an unhooked alias host",
			destination: "https://github.com/repos/openai/codex/issues",
			headers:     github,
			hookedHosts: []string{"api.github.com"},
			want:        true,
		},
		{
			name:        "scheme-less host destination",
			destination: "github.com",
			headers:     github,
			hookedHosts: []string{"api.github.com"},
			want:        true,
		},
		{
			name:        "suffix-bound alias host",
			destination: "https://api.astemu.ghe.com/repos/openai/codex/issues",
			headers:     github,
			hookedHosts: []string{"api.github.com"},
			want:        true,
		},
		{
			name:        "hook on the request host itself is left to hook evaluation",
			destination: "https://github.com/repos/openai/codex/issues",
			headers:     github,
			hookedHosts: []string{"github.com"},
			want:        false,
		},
		{
			name:        "another bound host stays hooked",
			destination: "https://github.com/repos/openai/codex/issues",
			headers:     github,
			hookedHosts: []string{"api.github.com", "github.com"},
			want:        true,
		},
		{
			name:        "no configured hooks",
			destination: "https://github.com/repos/openai/codex/issues",
			headers:     github,
			hookedHosts: nil,
			want:        false,
		},
		{
			name:        "request without a brokered dummy credential",
			destination: "https://github.com/repos/openai/codex/issues",
			headers:     map[string][]string{},
			hookedHosts: []string{"api.github.com"},
			want:        false,
		},
		{
			name:        "unrelated provider credential",
			destination: "https://github.com/repos/openai/codex/issues",
			headers:     openai,
			hookedHosts: []string{"api.github.com"},
			want:        false,
		},
		{
			name:        "credential bound only to its own host",
			destination: "https://enterprise.example/repos/openai/codex/issues",
			headers:     enterprise,
			hookedHosts: []string{"api.github.com"},
			want:        false,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got := broker.RequestMatchesHookedHostAlias(testCase.destination, testCase.headers, testCase.hookedHosts)
			if got != testCase.want {
				t.Fatalf("RequestMatchesHookedHostAlias(%q, hooked=%v) = %v, want %v",
					testCase.destination, testCase.hookedHosts, got, testCase.want)
			}
		})
	}

	t.Run("OPENAI_BASE_URL alias including an IPv6 destination", func(t *testing.T) {
		openAIBroker := NewProxyCredentialBroker(true)
		openAIEnv := map[string]string{
			"OPENAI_API_KEY":  "sk-real",
			"OPENAI_BASE_URL": "https://[fd00::1]/v1",
		}
		openAIBroker.VirtualizeChildEnv(openAIEnv)
		headers := bearerHeaders(openAIEnv["OPENAI_API_KEY"])
		for _, destination := range []string{"https://[fd00::1]/v1/models", "fd00::1"} {
			if !openAIBroker.RequestMatchesHookedHostAlias(destination, headers, []string{"api.openai.com"}) {
				t.Fatalf("destination %q did not match the hooked default host", destination)
			}
		}
		if openAIBroker.RequestMatchesHookedHostAlias("https://api.openai.com/v1/models", headers, []string{"api.openai.com"}) {
			t.Fatal("the hooked host itself must be left to hook evaluation")
		}
	})

	t.Run("disabled broker never matches", func(t *testing.T) {
		disabled := NewProxyCredentialBroker(false)
		if disabled.RequestMatchesHookedHostAlias("https://github.com/x", github, []string{"api.github.com"}) {
			t.Fatal("disabled broker matched a hooked host alias")
		}
	})
}

// TestBrokerCredentialDestinationParsingLikeRust pins the destination parsing
// and hostname matching shared by injection and the alias check (#52661).
func TestBrokerCredentialDestinationParsingLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		destination string
		wantOK      bool
		wantScheme  string
		wantHost    string
		wantPort    uint16
		wantPath    string
	}{
		{destination: "https://github.com/repos/openai/codex/issues", wantOK: true, wantScheme: "https", wantHost: "github.com", wantPort: 443, wantPath: "/repos/openai/codex/issues"},
		{destination: "http://github.com/x", wantOK: true, wantScheme: "http", wantHost: "github.com", wantPort: 80, wantPath: "/x"},
		{destination: "https://alias.example:8443/v2/models", wantOK: true, wantScheme: "https", wantHost: "alias.example", wantPort: 8443, wantPath: "/v2/models"},
		{destination: "https://[fd00::1]/v1/models", wantOK: true, wantScheme: "https", wantHost: "fd00::1", wantPort: 443, wantPath: "/v1/models"},
		{destination: "github.com", wantOK: true, wantHost: "github.com"},
		{destination: "https://github.com", wantOK: true, wantScheme: "https", wantHost: "github.com", wantPort: 443, wantPath: "/"},
		{destination: "ftp://github.com/x", wantOK: false},
		{destination: "https://", wantOK: false},
	} {
		scheme, host, port, path, ok := ParseBrokerCredentialDestination(testCase.destination)
		if ok != testCase.wantOK {
			t.Fatalf("ParseBrokerCredentialDestination(%q) ok = %v, want %v", testCase.destination, ok, testCase.wantOK)
		}
		if !ok {
			continue
		}
		if scheme != testCase.wantScheme || host != testCase.wantHost || port != testCase.wantPort || path != testCase.wantPath {
			t.Fatalf("ParseBrokerCredentialDestination(%q) = (%q, %q, %d, %q), want (%q, %q, %d, %q)",
				testCase.destination, scheme, host, port, path,
				testCase.wantScheme, testCase.wantHost, testCase.wantPort, testCase.wantPath)
		}
	}

	wildcard := CredentialDestination{Scheme: "https", Host: "hooked.example", Wildcard: true, Port: 443}
	if !wildcard.MatchesHostname("api.hooked.example") {
		t.Fatal("wildcard destination must match a subdomain regardless of port or path")
	}
	if !wildcard.MatchesHostname("API.HOOKED.EXAMPLE") {
		t.Fatal("wildcard destination must match case-insensitively")
	}
	if wildcard.MatchesHostname("hooked.example") {
		t.Fatal("wildcard destination must not match the bare host")
	}
	if wildcard.MatchesHostname("other.example") {
		t.Fatal("wildcard destination matched an unrelated host")
	}
	scoped := CredentialDestination{Scheme: "https", Host: "alias.example", Port: 8443, PathPrefix: "/v2"}
	if !scoped.MatchesHostname("alias.example") {
		t.Fatal("hostname matching must ignore the destination port and path")
	}
	if scoped.MatchesHost("alias.example", 443) {
		t.Fatal("host matching must still require the declared port")
	}
}

// hookedHostAliasSettings builds the #52661 scenario: credential brokerage with
// MITM, a hook on api.github.com, and unrestricted plaintext injection so both
// the forwarding and the MITM path may replace dummy credentials.
func hookedHostAliasSettings(hookedHosts []string) ProxySettings {
	settings := DefaultProxySettings()
	settings.Enabled = true
	settings.ProxyURL = "http://127.0.0.1:0"
	settings.SocksURL = "http://127.0.0.1:0"
	settings.AllowLocalBinding = true
	settings.MITM = true
	settings.CredentialBroker = true
	settings.DangerouslyAllowPlaintextCredentialInjection = true
	settings.SetAllowedDomains([]string{"api.github.com", "github.com", "api.astemu.ghe.com", "enterprise.example"})
	hooks := make([]ProxyMITMHookConfig, 0, len(hookedHosts))
	for _, host := range hookedHosts {
		hooks = append(hooks, ProxyMITMHookConfig{
			Host: host,
			Match: ProxyMITMHookMatchConfig{
				Methods:      []string{http.MethodGet, http.MethodPost},
				PathPrefixes: []string{"/repos/openai/"},
			},
		})
	}
	settings.MITMHooks = hooks
	return settings
}

func startHookedHostAliasProxy(t *testing.T, settings ProxySettings, baseEnv map[string]string) *PreparedProxyManagedNetwork {
	t.Helper()
	t.Setenv("CODEX_HOME", t.TempDir())
	prepared, err := StartProxyManagedNetwork(context.Background(), ProxyConfig{Network: settings}, baseEnv)
	if err != nil {
		t.Fatalf("StartProxyManagedNetwork() error = %v", err)
	}
	t.Cleanup(func() { _ = prepared.Close() })
	return prepared
}

// TestHTTPForwardingDeniesBrokeredProviderAliasBeforeInjectionLikeRust mirrors
// http_proxy.rs::http_plain_proxy_blocks_brokered_provider_alias_before_credential_injection:
// the forwarding path must evaluate the hook policy on the arriving request,
// before the brokered dummy credential is replaced.
func TestHTTPForwardingDeniesBrokeredProviderAliasBeforeInjectionLikeRust(t *testing.T) {
	prepared := startHookedHostAliasProxy(t, hookedHostAliasSettings([]string{"api.github.com"}), map[string]string{"GH_TOKEN": hookedHostAliasRealToken})
	dummy := prepared.Env["GH_TOKEN"]
	if dummy == "" || dummy == hookedHostAliasRealToken {
		t.Fatalf("broker did not virtualize GH_TOKEN: %q", dummy)
	}

	for _, testCase := range []struct {
		name          string
		method        string
		target        string
		authorization string
		wantBlocked   bool
	}{
		{
			name:          "unhooked alias is denied",
			method:        http.MethodPost,
			target:        "http://github.com/repos/openai/codex/issues",
			authorization: "Bearer " + dummy,
			wantBlocked:   true,
		},
		{
			name:        "unhooked alias without a dummy is allowed",
			method:      http.MethodPost,
			target:      "http://github.com/repos/openai/codex/issues",
			wantBlocked: false,
		},
		{
			name:          "hooked host with a matching request is left to hook evaluation",
			method:        http.MethodPost,
			target:        "http://api.github.com/repos/openai/codex/issues",
			authorization: "Bearer " + dummy,
			wantBlocked:   false,
		},
		{
			name:          "hooked host with a non-matching request stays denied",
			method:        http.MethodPost,
			target:        "http://api.github.com/other",
			authorization: "Bearer " + dummy,
			wantBlocked:   true,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			request, err := http.NewRequest(testCase.method, testCase.target, nil)
			if err != nil {
				t.Fatal(err)
			}
			if testCase.authorization != "" {
				request.Header.Set("Authorization", testCase.authorization)
			}
			_, response := prepared.server.handleHTTPRequest(request, &goproxy.ProxyCtx{})
			assertHookedHostAliasBlock(t, response, testCase.wantBlocked)
		})
	}
}

// TestMITMRequestDeniesBrokeredProviderAliasLikeRust mirrors
// mitm_tests.rs::mitm_policy_enforces_brokered_aliases_without_blocking_unrelated_credentials.
func TestMITMRequestDeniesBrokeredProviderAliasLikeRust(t *testing.T) {
	for _, hookedHosts := range [][]string{{"api.github.com"}, {"api.github.com", "github.com"}, nil} {
		prepared := startHookedHostAliasProxy(t, hookedHostAliasSettings(hookedHosts), map[string]string{
			"GH_TOKEN":            hookedHostAliasRealToken,
			"GH_HOST":             "enterprise.example",
			"GH_ENTERPRISE_TOKEN": "ghp-enterprise-real",
			"OPENAI_API_KEY":      "sk-real",
		})
		env := prepared.Env
		github := "Bearer " + env["GH_TOKEN"]
		token := "token " + env["GH_TOKEN"]
		openai := "Bearer " + env["OPENAI_API_KEY"]
		enterprise := "Bearer " + env["GH_ENTERPRISE_TOKEN"]

		for _, testCase := range []struct {
			name          string
			host          string
			authorization string
			usesCloud     bool
		}{
			{name: "cloud credential", host: "github.com", authorization: github, usesCloud: true},
			{name: "ghe.com suffix alias", host: "api.astemu.ghe.com", authorization: github, usesCloud: true},
			{name: "token-scheme cloud credential", host: "github.com", authorization: token, usesCloud: true},
			{name: "no authorization", host: "github.com", usesCloud: false},
			{name: "unrelated token", host: "github.com", authorization: "Bearer unrelated-token", usesCloud: false},
			{name: "openai credential", host: "github.com", authorization: openai, usesCloud: false},
			{name: "enterprise credential on its own host", host: "enterprise.example", authorization: enterprise, usesCloud: false},
		} {
			denied := testCase.usesCloud && len(hookedHosts) > 0 && !stringSliceContains(hookedHosts, testCase.host)
			t.Run(testCase.name+" hosts="+stringSliceJoin(hookedHosts), func(t *testing.T) {
				request, err := http.NewRequest(http.MethodGet, "https://"+testCase.host+"/repos/openai/codex/issues", nil)
				if err != nil {
					t.Fatal(err)
				}
				if testCase.authorization != "" {
					request.Header.Set("Authorization", testCase.authorization)
				}
				inner := request.Clone(context.WithValue(request.Context(), proxyMITMContextKey{}, proxyMITMRequestContext{
					host: testCase.host,
					port: 443,
				}))
				_, response := prepared.server.handleHTTPRequest(inner, &goproxy.ProxyCtx{})
				assertHookedHostAliasBlock(t, response, denied)
			})
		}
	}
}

// TestMITMRequestChecksConfiguredCredentialAliasAtTheRequestDestinationLikeRust
// mirrors mitm_tests.rs::mitm_policy_checks_configured_credential_aliases_at_the_request_destination:
// the hooked host need not share the alias's port or path prefix, and the alias
// check uses the same destination rules as injection.
func TestMITMRequestChecksConfiguredCredentialAliasAtTheRequestDestinationLikeRust(t *testing.T) {
	settings := hookedHostAliasSettings([]string{"api.hooked.example"})
	settings.SetAllowedDomains([]string{"alias.example", "api.hooked.example"})
	settings.CredentialProviders = map[string]CredentialProviderConfig{
		"custom": {
			Env:         []string{"PROVIDER_TOKEN"},
			Patterns:    []string{"provider_[a-z]{24}"},
			URLPrefixes: []string{"https://*.hooked.example/v1", "https://alias.example:8443/v2"},
			Auth:        []CredentialAuthMethod{CredentialAuthBearer},
		},
	}
	prepared := startHookedHostAliasProxy(t, settings, map[string]string{"PROVIDER_TOKEN": "provider_abcdefghijklmnopqrstuvwx"})
	dummy := prepared.Env["PROVIDER_TOKEN"]
	if dummy == "" || dummy == "provider_abcdefghijklmnopqrstuvwx" {
		t.Fatalf("configured provider did not virtualize PROVIDER_TOKEN: %q", dummy)
	}

	for _, testCase := range []struct {
		path        string
		wantBlocked bool
	}{
		{path: "/v2/models", wantBlocked: true},
		{path: "/v20/models", wantBlocked: false},
		{path: "/v2/../v2/models", wantBlocked: false},
	} {
		t.Run(testCase.path, func(t *testing.T) {
			request, err := http.NewRequest(http.MethodGet, "https://alias.example:8443"+testCase.path, nil)
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Authorization", "Bearer "+dummy)
			inner := request.Clone(context.WithValue(request.Context(), proxyMITMContextKey{}, proxyMITMRequestContext{
				host: "alias.example",
				port: 8443,
			}))
			_, response := prepared.server.handleHTTPRequest(inner, &goproxy.ProxyCtx{})
			assertHookedHostAliasBlock(t, response, testCase.wantBlocked)
		})
	}
}

func assertHookedHostAliasBlock(t *testing.T, response *http.Response, wantBlocked bool) {
	t.Helper()
	if !wantBlocked {
		if response != nil {
			t.Fatalf("request blocked with %d (%q), want it to pass", response.StatusCode, response.Header.Get("X-Proxy-Error"))
		}
		return
	}
	if response == nil {
		t.Fatal("request was not blocked")
	}
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusForbidden)
	}
	if got := response.Header.Get("X-Proxy-Error"); got != "blocked-by-mitm-hook" {
		t.Fatalf("x-proxy-error = %q, want %q", got, "blocked-by-mitm-hook")
	}
}

func stringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func stringSliceJoin(values []string) string {
	if len(values) == 0 {
		return "none"
	}
	joined := ""
	for index, value := range values {
		if index > 0 {
			joined += "+"
		}
		joined += value
	}
	return joined
}
