package execserver

import (
	"encoding/json"
	"strings"
	"testing"

	"codex_go/network"
)

func TestNetworkPolicyRustJSONShapes(t *testing.T) {
	p := NetworkPolicyRequestParams{ProcessID: "proc-1", Request: ExecServerNetworkPolicyRequest{Protocol: NetworkProtocolHTTPSConnect, Host: "example.com", Port: 443}}
	b, _ := json.Marshal(p)
	if string(b) != `{"processId":"proc-1","request":{"protocol":"https_connect","host":"example.com","port":443}}` {
		t.Fatalf("params=%s", b)
	}
	for _, d := range []ExecServerNetworkPolicyDecision{AllowNetworkPolicyDecision(), DenyNetworkPolicyDecision("blocked"), AskNetworkPolicyDecision("approval")} {
		b, _ = json.Marshal(d)
		var got ExecServerNetworkPolicyDecision
		if err := json.Unmarshal(b, &got); err != nil {
			t.Fatal(err)
		}
	}
}
func TestRemoteProxyPolicyDecisionTimeoutUsesLaunchLevelRustShape(t *testing.T) {
	b, _ := json.Marshal(RemoteNetworkProxyLaunchConfig{})
	if strings.Contains(string(b), "requestPolicyDecisions") {
		t.Fatalf("unexpected field: %s", b)
	}
	if strings.Contains(string(b), "policyDecisionTimeoutMs") {
		t.Fatalf("unexpected timeout field: %s", b)
	}
	timeout := uint64(900000)
	b, _ = json.Marshal(RemoteNetworkProxyLaunchConfig{PolicyDecisionTimeoutMS: &timeout})
	if !strings.Contains(string(b), `"policyDecisionTimeoutMs":900000`) {
		t.Fatalf("missing field: %s", b)
	}
}

func TestRemoteNetworkProxyConfigRoundTripsSupportedRustFields(t *testing.T) {
	settings := network.DefaultProxySettings()
	settings.Enabled = true
	settings.EnableSocks5 = false
	settings.EnableSocks5UDP = false
	settings.AllowUpstreamProxy = false
	allowAllUnixSockets := true
	settings.DangerouslyAllowAllUnixSockets = &allowAllUnixSockets
	settings.Mode = network.ProxyModeLimited
	settings.AllowLocalBinding = true
	settings.Domains = &network.ProxyDomainPermissions{Entries: []network.ProxyDomainPermissionEntry{
		{Pattern: "allowed.example", Permission: network.ProxyDomainAllow},
		{Pattern: "blocked.example", Permission: network.ProxyDomainDeny},
	}}
	settings.UnixSockets = &network.ProxyUnixSocketPermissions{Entries: map[string]network.ProxyUnixSocketPermission{
		"/tmp/allowed.sock": network.ProxyUnixSocketAllow,
		"/tmp/denied.sock":  network.ProxyUnixSocketDeny,
	}}
	remote, err := RemoteNetworkProxyConfigFromProxyConfig(network.ProxyConfig{Network: settings})
	if err != nil {
		t.Fatal(err)
	}
	roundTrip, err := remote.ProxyConfig()
	if err != nil {
		t.Fatal(err)
	}
	got := roundTrip.Network
	if !got.Enabled || got.EnableSocks5 || got.EnableSocks5UDP || got.AllowUpstreamProxy || got.DangerouslyAllowAllUnixSockets == nil || !*got.DangerouslyAllowAllUnixSockets || got.Mode != network.ProxyModeLimited || !got.AllowLocalBinding {
		t.Fatalf("round-trip settings = %#v", got)
	}
	if got.Domains == nil || len(got.Domains.Entries) != 2 || got.UnixSockets == nil || len(got.UnixSockets.Entries) != 2 {
		t.Fatalf("round-trip permissions = domains=%#v sockets=%#v", got.Domains, got.UnixSockets)
	}
}

func TestRemoteNetworkProxyConfigRejectsUnsupportedRustFeatures(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*network.ProxySettings)
	}{
		{name: "mitm", mutate: func(settings *network.ProxySettings) { settings.MITM = true }},
		{name: "credential-broker", mutate: func(settings *network.ProxySettings) { settings.CredentialBroker = true }},
		{name: "plaintext-injection", mutate: func(settings *network.ProxySettings) { settings.DangerouslyAllowPlaintextCredentialInjection = true }},
		{name: "mitm-hooks", mutate: func(settings *network.ProxySettings) { settings.MITMHooks = []network.ProxyMITMHookConfig{{}} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			settings := network.DefaultProxySettings()
			settings.Enabled = true
			test.mutate(&settings)
			if _, err := RemoteNetworkProxyConfigFromProxyConfig(network.ProxyConfig{Network: settings}); err == nil || !strings.Contains(err.Error(), "does not support MITM, credential injection, or MITM hooks") {
				t.Fatalf("error = %v", err)
			}
		})
	}
	for _, remote := range []RemoteNetworkProxyConfig{
		{Enabled: true, Mode: "unsupported"},
		{Enabled: true, Mode: string(network.ProxyModeFull), Domains: map[string]string{"example.com": "ask"}},
		{Enabled: true, Mode: string(network.ProxyModeFull), UnixSockets: map[string]string{"/tmp/test.sock": "ask"}},
	} {
		if _, err := remote.ProxyConfig(); err == nil {
			t.Fatalf("unsupported config accepted: %#v", remote)
		}
	}
}

func TestRemoteCredentialMaskingCarriesOnlyBrokerActivationLikeRust(t *testing.T) {
	settings := network.DefaultProxySettings()
	settings.Enabled = true
	settings.SetCredentialBrokerEnabled(true)
	settings.CredentialBrokerOpenAIHost = "gateway.example"
	remote, err := RemoteNetworkProxyConfigFromProxyConfigWithCredentialMasking(network.ProxyConfig{Network: settings}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !remote.CredentialMasking || !remote.Enabled {
		t.Fatalf("remote config = %#v", remote)
	}
	data, err := json.Marshal(remote)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"credentialMasking":true`) || strings.Contains(string(data), "gateway.example") {
		t.Fatalf("remote wire = %s", data)
	}
	roundTrip, err := remote.ProxyConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !roundTrip.Network.CredentialBroker || !roundTrip.Network.MITM {
		t.Fatalf("round-trip broker settings = %#v", roundTrip.Network)
	}
	if _, err := RemoteNetworkProxyConfigFromProxyConfig(network.ProxyConfig{Network: settings}); err == nil {
		t.Fatal("legacy conversion unexpectedly accepted credential brokerage")
	}

	// Rust #56049: configured provider rules travel with the launch, so the
	// executor-local broker can resolve them.
	settings.CredentialProviders = map[string]network.CredentialProviderConfig{
		"vendor": {Env: []string{"VENDOR_TOKEN"}, Patterns: []string{"v-[a-z]+"}, Auth: []network.CredentialAuthMethod{network.CredentialAuthToken}},
	}
	configured, err := RemoteNetworkProxyConfigFromProxyConfigWithCredentialMasking(network.ProxyConfig{Network: settings}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !configured.CredentialMasking || len(configured.CredentialProviders) != 1 {
		t.Fatalf("configured providers were not transported: %#v", configured)
	}
	data, err = json.Marshal(configured)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"credentialMasking":true`) ||
		!strings.Contains(string(data), `"credentialProviders":{"vendor":{"env":["VENDOR_TOKEN"],"patterns":["v-[a-z]+"],"auth":["token"]}}`) {
		t.Fatalf("configured remote wire = %s", data)
	}
	roundTrip, err = configured.ProxyConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !roundTrip.Network.CredentialBroker || len(roundTrip.Network.CredentialProviders) != 1 {
		t.Fatalf("round-trip providers = %#v", roundTrip.Network)
	}

	// An executor that masks only built-in providers keeps the unmasked
	// fallback for configured providers.
	fallback, err := RemoteNetworkProxyConfigFromProxyConfigWithCredentialMaskingMode(network.ProxyConfig{Network: settings}, RemoteCredentialMaskingBuiltInOnly)
	if err != nil {
		t.Fatal(err)
	}
	if fallback.CredentialMasking || len(fallback.CredentialProviders) != 0 {
		t.Fatalf("built-in-only masking accepted configured providers: %#v", fallback)
	}

	// Provider rules sent with masking off are discarded on decode.
	var normalized RemoteNetworkProxyConfig
	if err := json.Unmarshal([]byte(`{"enabled":true,"mode":"full","credentialProviders":{"vendor":{"env":["VENDOR_TOKEN"]}}}`), &normalized); err != nil {
		t.Fatal(err)
	}
	if len(normalized.CredentialProviders) != 0 {
		t.Fatalf("providers survived disabled masking: %#v", normalized)
	}
}

func TestEnvironmentCapabilitiesNetworkProxyModesLikeRust(t *testing.T) {
	tests := []struct {
		name     string
		info     EnvironmentCapabilities
		expected RemoteCredentialMasking
	}{
		{name: "disabled", expected: RemoteCredentialMaskingDisabled},
		{name: "custom providers without masking", info: EnvironmentCapabilities{CredentialMaskingCustomProviders: true}, expected: RemoteCredentialMaskingDisabled},
		{name: "built-in only", info: EnvironmentCapabilities{CredentialMasking: true}, expected: RemoteCredentialMaskingBuiltInOnly},
		{name: "configured providers", info: EnvironmentCapabilities{CredentialMasking: true, CredentialMaskingCustomProviders: true}, expected: RemoteCredentialMaskingWithConfiguredProviders},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.info.NetworkProxy().CredentialMasking; got != test.expected {
				t.Fatalf("NetworkProxy().CredentialMasking = %v, want %v", got, test.expected)
			}
		})
	}
	if !(EnvironmentCapabilities{NetworkProxyLaunch: true}).NetworkProxy().Launch {
		t.Fatal("NetworkProxy().Launch = false, want true")
	}
}
