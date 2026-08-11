package network

import (
	"net"
	"reflect"
	runtimeos "runtime"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func TestSettingsDefaultsAndDomainPrecedence(t *testing.T) {
	settings := DefaultProxySettings()
	if settings.ProxyURL != DefaultProxyURL || !settings.EnableSocks5 || settings.Mode != ProxyModeFull {
		t.Fatalf("defaults = %#v", settings)
	}
	settings.SetDeniedDomains([]string{"example.com"})
	settings.SetAllowedDomains([]string{"example.com"})
	if got := settings.AllowedDomains(); got != nil {
		t.Fatalf("allowed = %#v", got)
	}
	if got := settings.DeniedDomains(); !reflect.DeepEqual(got, []string{"example.com"}) {
		t.Fatalf("denied = %#v", got)
	}
	settings.UpsertDomainPermission("API.Example.com", ProxyDomainAllow, NormalizeProxyHost)
	if got := settings.AllowedDomains(); !reflect.DeepEqual(got, []string{"API.Example.com"}) {
		t.Fatalf("upsert allowed = %#v", got)
	}
}

func TestModeAllowsMethod(t *testing.T) {
	limited := ProxyModeLimited
	full := ProxyModeFull
	if !(&limited).AllowsMethod("GET") || (&limited).AllowsMethod("POST") {
		t.Fatalf("limited method policy wrong")
	}
	if !(&full).AllowsMethod("POST") {
		t.Fatalf("full method policy wrong")
	}
}

func TestParseHostPortAndHostAndPortFormatting(t *testing.T) {
	cases := map[string]ProxySocketAddressParts{
		"127.0.0.1:8080":              {Host: "127.0.0.1", Port: 8080},
		"http://example.com:8080/api": {Host: "example.com", Port: 8080},
		"http://user:pass@host:5555":  {Host: "host", Port: 5555},
		"http://[::1]:9999":           {Host: "::1", Port: 9999},
		"2001:db8::1":                 {Host: "2001:db8::1", Port: 3128},
		"example.com:notaport":        {Host: "example.com", Port: 3128},
	}
	for input, want := range cases {
		got, err := ParseProxyHostPort(input, 3128)
		if err != nil {
			t.Fatalf("ParseProxyHostPort(%q) error = %v", input, err)
		}
		if got != want {
			t.Fatalf("ParseProxyHostPort(%q) = %#v want %#v", input, got, want)
		}
	}
	if got := ProxyHostAndPortFromNetworkAddr("", 1234); got != "<missing>" {
		t.Fatalf("missing = %q", got)
	}
	if got := ProxyHostAndPortFromNetworkAddr("http://[::1]:8080", 3128); got != "[::1]:8080" {
		t.Fatalf("ipv6 = %q", got)
	}
}

func TestResolveRuntimeAndClampBindAddrs(t *testing.T) {
	settings := DefaultProxySettings()
	settings.ProxyURL = "http://0.0.0.0:3128"
	settings.SocksURL = "http://0.0.0.0:8081"
	runtime, err := ResolveProxyRuntime(ProxyConfig{Network: settings})
	if err != nil {
		t.Fatal(err)
	}
	if !runtime.HTTPAddr.IP.IsLoopback() || !runtime.SocksAddr.IP.IsLoopback() {
		t.Fatalf("expected loopback clamp: %#v", runtime)
	}
	settings.DangerouslyAllowNonLoopbackProxy = true
	settings.SetAllowUnixSockets([]string{"/tmp/docker.sock"})
	runtime, err = ResolveProxyRuntime(ProxyConfig{Network: settings})
	if err != nil {
		t.Fatal(err)
	}
	// Rust 9742cc8ed5: Unix socket proxy permissions are macOS-only and are
	// excluded from Windows runtime settings and bind-address clamping.
	if runtimeos.GOOS == "windows" {
		if runtime.HTTPAddr.IP.IsLoopback() || runtime.SocksAddr.IP.IsLoopback() {
			t.Fatalf("windows must ignore unix socket clamping: %#v", runtime)
		}
	} else if !runtime.HTTPAddr.IP.IsLoopback() || !runtime.SocksAddr.IP.IsLoopback() {
		t.Fatalf("unix sockets should force loopback: %#v", runtime)
	}
	settings.SetAllowUnixSockets([]string{"relative.sock"})
	if _, err := ResolveProxyRuntime(ProxyConfig{Network: settings}); err == nil {
		if runtimeos.GOOS != "windows" {
			t.Fatalf("relative unix socket should fail")
		}
	}
}

func TestProxyConfigFromConfigValues(t *testing.T) {
	config, err := ProxyConfigFromConfigValues(map[string]any{
		"network_proxy": map[string]any{
			"enabled":                            true,
			"proxy_url":                          "http://127.0.0.1:43128",
			"enable_socks5":                      false,
			"allow_upstream_proxy":               false,
			"mode":                               "limited",
			"allowed_domains":                    []any{"api.openai.com"},
			"dangerously_allow_all_unix_sockets": true,
			"unix_sockets": map[string]any{
				"/var/run/docker.sock": "allow",
				"/tmp/deny.sock":       "deny",
			},
			"domains": map[string]any{
				"example.com": "deny",
			},
		},
	})
	if err != nil {
		t.Fatalf("ProxyConfigFromConfigValues error = %v", err)
	}
	settings := config.Network
	if !settings.Enabled || settings.ProxyURL != "http://127.0.0.1:43128" || settings.EnableSocks5 || settings.AllowUpstreamProxy {
		t.Fatalf("settings = %#v", settings)
	}
	if settings.Mode != ProxyModeLimited {
		t.Fatalf("mode = %s", settings.Mode)
	}
	if got := settings.AllowedDomains(); !reflect.DeepEqual(got, []string{"api.openai.com"}) {
		t.Fatalf("allowed = %#v", got)
	}
	if got := settings.DeniedDomains(); !reflect.DeepEqual(got, []string{"example.com"}) {
		t.Fatalf("denied = %#v", got)
	}
	if got := settings.AllowUnixSockets(); !reflect.DeepEqual(got, []string{"/var/run/docker.sock"}) {
		t.Fatalf("unix sockets = %#v", got)
	}
}

func TestProxyConfigFromConfigValuesRejectsInvalidRuntime(t *testing.T) {
	_, err := ProxyConfigFromConfigValues(map[string]any{
		"network_proxy": map[string]any{
			"allow_unix_sockets": []any{"relative.sock"},
		},
	})
	if err == nil && runtimeos.GOOS != "windows" {
		t.Fatal("ProxyConfigFromConfigValues returned nil error")
	}
}

func TestProxyConfigFromConfigValuesParsesMITMHooksLikeRust(t *testing.T) {
	config, err := ProxyConfigFromConfigValues(map[string]any{
		"network_proxy": map[string]any{
			"mitm": true,
			"mitm_hooks": []any{map[string]any{
				"host": "api.github.com",
				"match": map[string]any{
					"methods":       []any{"POST"},
					"path_prefixes": []any{"/repos/"},
					"query":         map[string]any{"state": []any{"open"}},
					"headers":       map[string]any{"X-Mode": []any{"write"}},
				},
				"actions": map[string]any{
					"strip_request_headers": []any{"Authorization"},
					"inject_request_headers": []any{map[string]any{
						"name":           "Authorization",
						"secret_env_var": "GH_TOKEN",
						"prefix":         "Bearer ",
					}},
				},
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(config.Network.MITMHooks) != 1 {
		t.Fatalf("MITM hooks = %#v", config.Network.MITMHooks)
	}
	hook := config.Network.MITMHooks[0]
	if hook.Host != "api.github.com" || !reflect.DeepEqual(hook.Match.Methods, []string{"POST"}) || !reflect.DeepEqual(hook.Match.Query["state"], []string{"open"}) {
		t.Fatalf("MITM hook = %#v", hook)
	}
	if len(hook.Actions.InjectRequestHeaders) != 1 || hook.Actions.InjectRequestHeaders[0].SecretEnvVar == nil || *hook.Actions.InjectRequestHeaders[0].SecretEnvVar != "GH_TOKEN" {
		t.Fatalf("MITM hook actions = %#v", hook.Actions)
	}
}

func TestProxyConfigFromTOMLValuesParsesMITMHooksLikeRust(t *testing.T) {
	var values map[string]any
	err := toml.Unmarshal([]byte(`
[network_proxy]
mitm = true

[[network_proxy.mitm_hooks]]
host = "api.github.com"

[network_proxy.mitm_hooks.match]
methods = ["POST"]
path_prefixes = ["/repos/"]

[network_proxy.mitm_hooks.actions]
strip_request_headers = ["Authorization"]

[[network_proxy.mitm_hooks.actions.inject_request_headers]]
name = "Authorization"
secret_env_var = "GH_TOKEN"
prefix = "Bearer "
`), &values)
	if err != nil {
		t.Fatal(err)
	}
	config, err := ProxyConfigFromConfigValues(values)
	if err != nil {
		t.Fatal(err)
	}
	if len(config.Network.MITMHooks) != 1 || config.Network.MITMHooks[0].Host != "api.github.com" {
		t.Fatalf("MITM hooks = %#v", config.Network.MITMHooks)
	}
}

func TestProxyConfigFromPermissionProfileNetworkInheritanceLikeRust(t *testing.T) {
	config, err := ProxyConfigFromConfigValues(map[string]any{
		"default_permissions": "child",
		"permissions": map[string]any{
			"parent": map[string]any{
				"network": map[string]any{
					"enabled": true,
					"mode":    "limited",
					"domains": map[string]any{
						"API.Example.com": "allow",
						"blocked.test":    "deny",
					},
					"unix_sockets": map[string]any{"/tmp/parent.sock": "allow"},
				},
			},
			"child": map[string]any{
				"extends": "parent",
				"network": map[string]any{
					"mode":                "full",
					"allow_local_binding": true,
					"domains": map[string]any{
						"api.example.com": "deny",
						"child.test":      "allow",
					},
					"unix_sockets": map[string]any{"/tmp/child.sock": "allow"},
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	settings := config.Network
	if !settings.Enabled || settings.Mode != ProxyModeFull || !settings.AllowLocalBinding {
		t.Fatalf("settings = %#v", settings)
	}
	if got := settings.AllowedDomains(); !reflect.DeepEqual(got, []string{"child.test"}) {
		t.Fatalf("allowed domains = %#v", got)
	}
	if got := settings.DeniedDomains(); !reflect.DeepEqual(got, []string{"blocked.test", "api.example.com"}) {
		t.Fatalf("denied domains = %#v", got)
	}
	if got := settings.AllowUnixSockets(); !reflect.DeepEqual(got, []string{"/tmp/child.sock", "/tmp/parent.sock"}) {
		t.Fatalf("unix sockets = %#v", got)
	}
}

func TestProxyConfigFromPermissionProfileExtendingBuiltinWorkspaceLikeRust(t *testing.T) {
	var values map[string]any
	if err := toml.Unmarshal([]byte(`
default_permissions = "dev"

[permissions.dev]
extends = ":workspace"

[permissions.dev.network]
enabled = true
proxy_url = "http://127.0.0.1:0"
enable_socks5 = false
allowed_domains = ["api.openai.com"]
`), &values); err != nil {
		t.Fatal(err)
	}
	config, err := ProxyConfigFromConfigValues(values)
	if err != nil {
		t.Fatalf("ProxyConfigFromConfigValues() error = %v", err)
	}
	if config == nil || !config.Network.Enabled || !reflect.DeepEqual(config.Network.AllowedDomains(), []string{"api.openai.com"}) {
		t.Fatalf("proxy config = %#v", config)
	}
}

func TestProxyConfigPermissionProfileInheritanceCyclePreservesRustPathOrder(t *testing.T) {
	_, err := ProxyConfigFromConfigValues(map[string]any{
		"default_permissions": "alpha",
		"permissions": map[string]any{
			"alpha": map[string]any{"extends": "beta", "network": map[string]any{"enabled": true}},
			"beta":  map[string]any{"extends": "gamma"},
			"gamma": map[string]any{"extends": "alpha"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "alpha -> beta -> gamma -> alpha") {
		t.Fatalf("cycle error = %v", err)
	}
}

func TestProxyConfigRejectsUnknownBuiltinDefaultPermissionLikeRust(t *testing.T) {
	_, err := ProxyConfigFromConfigValues(map[string]any{"default_permissions": ":unknown"})
	if err == nil || err.Error() != "default_permissions refers to unknown built-in profile `:unknown`" {
		t.Fatalf("error = %v", err)
	}
}

func TestProxyConfigFromPermissionProfileNamedMITMActionsLikeRust(t *testing.T) {
	var values map[string]any
	err := toml.Unmarshal([]byte(`
default_permissions = "child"

[permissions.parent]

[permissions.parent.network.mitm.actions.auth]
strip_request_headers = ["Authorization"]

[[permissions.parent.network.mitm.actions.auth.inject_request_headers]]
name = "Authorization"
secret_env_var = "GH_TOKEN"
prefix = "Bearer "

[permissions.child]
extends = "parent"

[permissions.child.network]
enabled = true
mode = "full"

[permissions.child.network.domains]
"api.github.com" = "allow"

[permissions.child.network.mitm.hooks.github_write]
host = "api.github.com"
methods = ["POST"]
path_prefixes = ["/repos/"]
action = ["auth"]
`), &values)
	if err != nil {
		t.Fatal(err)
	}
	config, err := ProxyConfigFromConfigValues(values)
	if err != nil {
		t.Fatal(err)
	}
	if !config.Network.Enabled || !config.Network.MITM || len(config.Network.MITMHooks) != 1 {
		t.Fatalf("network config = %#v", config.Network)
	}
	hook := config.Network.MITMHooks[0]
	if hook.Host != "api.github.com" || !reflect.DeepEqual(hook.Match.Methods, []string{"POST"}) || len(hook.Actions.InjectRequestHeaders) != 1 || hook.Actions.InjectRequestHeaders[0].SecretEnvVar == nil || *hook.Actions.InjectRequestHeaders[0].SecretEnvVar != "GH_TOKEN" {
		t.Fatalf("named MITM hook = %#v", hook)
	}
}

func TestProxyConfigPermissionProfileMITMRejectsUndefinedActionLikeRust(t *testing.T) {
	_, err := ProxyConfigFromConfigValues(map[string]any{
		"default_permissions": "dev",
		"permissions": map[string]any{
			"dev": map[string]any{
				"network": map[string]any{
					"mitm": map[string]any{
						"hooks": map[string]any{
							"write": map[string]any{
								"host":          "api.example.test",
								"methods":       []any{"POST"},
								"path_prefixes": []any{"/"},
								"action":        []any{"missing"},
							},
						},
					},
				},
			},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "references undefined action `missing`") {
		t.Fatalf("error = %v", err)
	}
}

func TestProxyConfigRejectsCredentialBrokerWithoutMITMLikeRust(t *testing.T) {
	settings := DefaultProxySettings()
	settings.CredentialBroker = true
	if _, err := ResolveProxyRuntime(ProxyConfig{Network: settings}); err == nil || !strings.Contains(err.Error(), "network.credential_broker requires network.mitm = true") {
		t.Fatalf("ResolveProxyRuntime error = %v", err)
	}
}

func TestNormalizeHostLoopbackAndNonPublicIP(t *testing.T) {
	if NormalizeProxyHost("[fe80::1%25lo0]") != "fe80::1%lo0" {
		t.Fatalf("scoped ipv6 normalization failed")
	}
	host, err := ParseProxyHost("LOCALHOST.")
	if err != nil {
		t.Fatal(err)
	}
	if !IsLoopbackProxyHost(host) {
		t.Fatalf("localhost should be loopback")
	}
	for _, ip := range []string{"127.0.0.1", "10.0.0.1", "100.64.0.1", "192.0.2.1"} {
		if !IsNonPublicProxyIP(net.ParseIP(ip)) {
			t.Fatalf("%s should be non-public", ip)
		}
	}
	if IsNonPublicProxyIP(net.ParseIP("8.8.8.8")) {
		t.Fatalf("8.8.8.8 should be public")
	}
}

func TestDomainPatternAllows(t *testing.T) {
	apex := ParseProxyDomainPattern("**.example.com")
	if !(&apex).Allows(ParseProxyDomainPattern("api.example.com")) || !(&apex).Allows(ParseProxyDomainPattern("example.com")) {
		t.Fatalf("apex pattern should allow apex and subdomains")
	}
	sub := ParseProxyDomainPattern("*.example.com")
	if (&sub).Allows(ParseProxyDomainPattern("example.com")) || !(&sub).Allows(ParseProxyDomainPattern("api.example.com")) {
		t.Fatalf("subdomain pattern mismatch")
	}
	exact := ParseProxyDomainPattern("example.com")
	if !(&exact).Allows(ParseProxyDomainPattern("example.com")) || (&exact).Allows(ParseProxyDomainPattern("api.example.com")) {
		t.Fatalf("exact pattern mismatch")
	}
}

func TestProxyEnvOverrides(t *testing.T) {
	env := map[string]string{"PRESERVED": "value"}
	prepared := PrepareProxyManagedNetwork(env, MustProxyTCPAddr("127.0.0.1", 3128), MustProxyTCPAddr("127.0.0.1", 8081), true, false)
	if prepared.Env["PRESERVED"] != "value" {
		t.Fatalf("base env not preserved")
	}
	if prepared.Env["HTTP_PROXY"] != "http://127.0.0.1:3128" || prepared.Env["ALL_PROXY"] != "socks5h://127.0.0.1:8081" {
		t.Fatalf("proxy env = %#v", prepared.Env)
	}
	if !HasProxyURLEnvVars(map[string]string{"all_proxy": "socks5h://127.0.0.1:8081"}) {
		t.Fatalf("lowercase all_proxy not detected")
	}
	if !reflect.DeepEqual(prepared.SandboxContext.LoopbackPorts, []uint16{3128, 8081}) {
		t.Fatalf("sandbox ports = %#v", prepared.SandboxContext)
	}
}

func TestDecisions(t *testing.T) {
	if DenyProxyDecision("").Reason != ProxyReasonPolicyDenied {
		t.Fatalf("empty deny reason should default")
	}
	ask := AskProxyDecision(ProxyReasonNotAllowed)
	if ask.Allow || ask.Source != ProxyDecisionSourceDecider || ask.Decision != ProxyPolicyDecisionAsk {
		t.Fatalf("ask = %#v", ask)
	}
}
