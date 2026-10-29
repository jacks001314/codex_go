package appserver

import (
	"reflect"
	"testing"

	"github.com/pelletier/go-toml/v2"

	"codex_go/config"
	"codex_go/session"
	"codex_go/utils"
)

// Mirrors Rust #46331 at the app-server boundary: an invalid domain pattern or
// socket path in the selected permission profile must not fail while the proxy
// configuration is parsed, because managed requirements replace it before the
// composed policy is validated.
func TestManagedNetworkReplacesInvalidSelectedPolicyBeforeValidationLikeRust(t *testing.T) {
	var values map[string]any
	if err := toml.Unmarshal([]byte(`
default_permissions = "dev"

[permissions.dev.network]
enabled = true
mode = "full"

[permissions.dev.network.domains]
"[" = "allow"

[permissions.dev.network.unix_sockets]
"relative.sock" = "allow"
`), &values); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	enabled := true
	hardDeny := true
	router := NewDefaultRuntimeRouterWithOptions(session.NewStore(home), home, &RuntimeRouterOptions{
		Requirements: &config.ConfigRequirements{Network: &config.NetworkRequirements{
			Enabled:                   &enabled,
			HTTPPort:                  uint16Ptr(0),
			SOCKSPort:                 uint16Ptr(0),
			ManagedAllowedDomainsOnly: &hardDeny,
			AllowedDomains:            []string{"managed.example.com"},
			UnixSockets:               map[string]config.NetworkPermission{},
		}},
	})
	defer router.Close()
	proxyConfig, active, err := router.buildManagedNetworkProxyConfigForCWDWithPlatform(values, home, utils.PlatformUnknown)
	if err != nil {
		t.Fatalf("managed replacement should validate: %v", err)
	}
	if !active || proxyConfig == nil {
		t.Fatalf("managed network inactive: config=%#v active=%v", proxyConfig, active)
	}
	settings := proxyConfig.Network
	if got := settings.AllowedDomains(); !reflect.DeepEqual(got, []string{"managed.example.com"}) {
		t.Fatalf("allowed domains = %#v, want the managed replacement", got)
	}
	if got := settings.AllowUnixSockets(); len(got) != 0 {
		t.Fatalf("socket allow entries = %#v, want the managed replacement", got)
	}
}
