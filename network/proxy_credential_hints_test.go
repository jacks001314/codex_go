package network

import (
	"runtime"
	"testing"
)

// TestCredentialBrokerDestinationHintsSurviveEnvFilteringLikeRust mirrors
// #44068: a filtered-out destination variable is resolved from retained local
// hints, while an explicitly present value (including empty) still wins.
func TestCredentialBrokerDestinationHintsSurviveEnvFilteringLikeRust(t *testing.T) {
	envKey := "VENDOR_API_BASE"
	config := CredentialProviderConfig{
		Env:              []string{"VENDOR_TOKEN"},
		Patterns:         []string{"vk-[a-z0-9]{16}"},
		URLPrefixes:      []string{"https://api.vendor.example"},
		URLPrefixFromEnv: &envKey,
		Auth:             []CredentialAuthMethod{CredentialAuthBearer},
	}
	const realValue = "vk-0123456789abcdef"

	t.Run("hint resolves filtered destination", func(t *testing.T) {
		broker := NewProxyCredentialBrokerWithProviders(true, []*ProxyCredentialProvider{ConfiguredCredentialProvider("vendor", config)})
		broker.SetDestinationHints(map[string]string{envKey: "tenant.vendor.example"})
		child := map[string]string{"VENDOR_TOKEN": realValue}
		broker.VirtualizeChildEnv(child)
		if child["VENDOR_TOKEN"] == realValue || child["VENDOR_TOKEN"] == "" {
			t.Fatalf("env not virtualized: %#v", child)
		}
		if _, leaked := child[envKey]; leaked {
			t.Fatalf("destination hint leaked into child env: %#v", child)
		}
		if !broker.HostRequiresMITM("tenant.vendor.example") {
			t.Fatal("hint destination did not require MITM")
		}
	})

	t.Run("explicit empty value overrides hint", func(t *testing.T) {
		broker := NewProxyCredentialBrokerWithProviders(true, []*ProxyCredentialProvider{ConfiguredCredentialProvider("vendor", config)})
		broker.SetDestinationHints(map[string]string{envKey: "tenant.vendor.example"})
		child := map[string]string{"VENDOR_TOKEN": realValue, envKey: ""}
		broker.VirtualizeChildEnv(child)
		if broker.HostRequiresMITM("tenant.vendor.example") {
			t.Fatal("explicit empty destination value must override the hint")
		}
		if !broker.HostRequiresMITM("api.vendor.example") {
			t.Fatal("static destination binding lost")
		}
	})

	t.Run("destination rotation rebinds existing aliases", func(t *testing.T) {
		broker := NewProxyCredentialBrokerWithProviders(true, []*ProxyCredentialProvider{ConfiguredCredentialProvider("vendor", config)})
		broker.SetDestinationHints(map[string]string{envKey: "tenant1.vendor.example"})
		broker.VirtualizeChildEnv(map[string]string{"VENDOR_TOKEN": realValue})
		if !broker.HostRequiresMITM("tenant1.vendor.example") {
			t.Fatal("initial destination missing")
		}
		broker.SetDestinationHints(map[string]string{envKey: "tenant2.vendor.example"})
		broker.VirtualizeChildEnv(map[string]string{"VENDOR_TOKEN": realValue})
		if broker.HostRequiresMITM("tenant1.vendor.example") {
			t.Fatal("stale destination alias was not reconciled")
		}
		if !broker.HostRequiresMITM("tenant2.vendor.example") {
			t.Fatal("rotated destination missing")
		}
	})

	t.Run("absent destination preserves the registration", func(t *testing.T) {
		broker := NewProxyCredentialBrokerWithProviders(true, []*ProxyCredentialProvider{ConfiguredCredentialProvider("vendor", config)})
		broker.SetDestinationHints(map[string]string{envKey: "tenant1.vendor.example"})
		broker.VirtualizeChildEnv(map[string]string{"VENDOR_TOKEN": realValue})
		broker.SetDestinationHints(map[string]string{})
		broker.VirtualizeChildEnv(map[string]string{"VENDOR_TOKEN": realValue})
		if !broker.HostRequiresMITM("tenant1.vendor.example") {
			t.Fatal("registration lost when the destination variable became absent")
		}
	})
}

// TestCredentialBrokerDisablesOnAmbiguousWindowsProviderEnvLikeRust mirrors
// #44068 on Windows: provider overrides that differ only by case with different
// values disable brokerage rather than guessing which one wins.
func TestCredentialBrokerDisablesOnAmbiguousWindowsProviderEnvLikeRust(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-only environment key casing rule")
	}
	broker := NewProxyCredentialBrokerWithProviders(true, nil)
	broker.SetDestinationHints(map[string]string{"GH_HOST": "a.example", "gh_host": "b.example"})
	if broker.Enabled() {
		t.Fatal("ambiguous case-insensitive provider keys must disable brokerage")
	}

	unambiguous := NewProxyCredentialBrokerWithProviders(true, nil)
	unambiguous.SetDestinationHints(map[string]string{"GH_HOST": "a.example"})
	if !unambiguous.Enabled() {
		t.Fatal("unambiguous provider environment must keep brokerage enabled")
	}
}
