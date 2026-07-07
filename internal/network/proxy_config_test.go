package network

import (
	"net"
	"reflect"
	"testing"
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
	if !runtime.HTTPAddr.IP.IsLoopback() || !runtime.SocksAddr.IP.IsLoopback() {
		t.Fatalf("unix sockets should force loopback: %#v", runtime)
	}
	settings.SetAllowUnixSockets([]string{"relative.sock"})
	if _, err := ResolveProxyRuntime(ProxyConfig{Network: settings}); err == nil {
		t.Fatalf("relative unix socket should fail")
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
	if err == nil {
		t.Fatal("ProxyConfigFromConfigValues returned nil error")
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
