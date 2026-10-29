package network

import (
	"reflect"
	"testing"

	"github.com/pelletier/go-toml/v2"

	"codex_go/utils"
)

// Mirrors Rust #46331 network_config_tests: an invalid domain pattern or
// unix-socket path in the selected configuration survives projection, fails
// final validation only when it is retained, and passes when managed
// requirements replace it.
func TestProxyProfileNetworkDefersDomainAndSocketValidationLikeRust(t *testing.T) {
	for _, raw := range []string{
		"[unix_sockets]\n'relative.sock' = 'allow'",
		"[unix_sockets]\n'~/allowed.sock' = 'allow'",
		"[unix_sockets]\n\"/tmp/\\u0000allowed.sock\" = 'allow'",
		"[domains]\n'[' = 'allow'",
	} {
		var table map[string]any
		if err := toml.Unmarshal([]byte(raw), &table); err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		values := map[string]any{
			"default_permissions": "dev",
			"permissions": map[string]any{
				"dev": map[string]any{"network": table},
			},
		}
		config, err := ProxyConfigFromConfigValues(values)
		if err != nil {
			t.Fatalf("%s: projection rejected the selected policy: %v", raw, err)
		}
		if _, err := NewProxySpecForPlatform(*config, nil, true, utils.PlatformUnknown); err == nil {
			t.Fatalf("%s: final validation accepted the retained invalid entry", raw)
		}

		// Managed requirements replace the invalid Allow entries before validation.
		replacedDomains := &ProxyDomainPermissions{Entries: []ProxyDomainPermissionEntry{
			{Pattern: "replaced.example", Permission: ProxyDomainAllow},
		}}
		replacedSockets := &ProxyUnixSocketPermissions{Entries: map[string]ProxyUnixSocketPermission{}}
		spec, err := NewProxySpecForPlatform(*config, &ProxyRequirements{
			Domains:                   replacedDomains,
			UnixSockets:               replacedSockets,
			ManagedAllowedDomainsOnly: true,
		}, true, utils.PlatformUnknown)
		if err != nil {
			t.Fatalf("%s: managed replacement should validate: %v", raw, err)
		}
		composed := spec.Config()
		if got := composed.Network.AllowedDomains(); !reflect.DeepEqual(got, []string{"replaced.example"}) {
			t.Fatalf("%s: replaced allowed domains = %#v", raw, got)
		}
		if got := composed.Network.AllowUnixSockets(); len(got) != 0 {
			t.Fatalf("%s: replaced socket allow entries = %#v", raw, got)
		}
	}
}

// The composed policy still rejects malformed patterns that no requirement
// replaces, matching Rust build_config_state's allow/deny/review compilation.
func TestComposedProxyPolicyRejectsMalformedDomainPatterns(t *testing.T) {
	settings := DefaultProxySettings()
	settings.Domains = &ProxyDomainPermissions{Entries: []ProxyDomainPermissionEntry{
		{Pattern: "[", Permission: ProxyDomainAllow},
	}}
	if _, err := NewProxySpecForPlatform(ProxyConfig{Network: settings}, nil, true, utils.PlatformUnknown); err == nil {
		t.Fatal("malformed allowed-domain pattern was accepted")
	}
	settings = DefaultProxySettings()
	settings.ReviewDomains = []string{"["}
	if _, err := NewProxySpecForPlatform(ProxyConfig{Network: settings}, nil, true, utils.PlatformUnknown); err == nil {
		t.Fatal("malformed review-domain pattern was accepted")
	}
}
