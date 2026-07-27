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
	settings.DangerouslyAllowAllUnixSockets = true
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
	if !got.Enabled || got.EnableSocks5 || got.EnableSocks5UDP || got.AllowUpstreamProxy || !got.DangerouslyAllowAllUnixSockets || got.Mode != network.ProxyModeLimited || !got.AllowLocalBinding {
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
