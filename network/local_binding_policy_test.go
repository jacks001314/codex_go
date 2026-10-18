package network

import (
	"strings"
	"testing"

	"codex_go/utils"
)

func TestManagedLocalBindingPolicyLikeRust(t *testing.T) {
	enabled := DefaultProxySettings()
	enabled.Enabled = true
	enabled.AllowLocalBindingSet = false
	spec, err := NewProxySpecForPlatformWithLocalBinding(
		ProxyConfig{Network: enabled},
		nil,
		true,
		utils.PlatformWindows,
		LocalBindingRequireTrue,
	)
	if err != nil {
		t.Fatal(err)
	}
	config := spec.Config()
	if !config.Network.AllowLocalBindingSet || !config.Network.AllowLocalBinding {
		t.Fatalf("MXC omitted local binding = %v/%v, want explicit true", config.Network.AllowLocalBinding, config.Network.AllowLocalBindingSet)
	}

	explicitFalse := DefaultProxySettings()
	explicitFalse.Enabled = true
	explicitFalse.AllowLocalBinding = false
	explicitFalse.AllowLocalBindingSet = true
	_, err = NewProxySpecForPlatformWithLocalBinding(
		ProxyConfig{Network: explicitFalse},
		nil,
		true,
		utils.PlatformWindows,
		LocalBindingRequireTrue,
	)
	if err == nil || !strings.Contains(err.Error(), "allow_local_binding must be true") {
		t.Fatalf("MXC explicit false error = %v", err)
	}

	plain := DefaultProxySettings()
	plain.Enabled = true
	plain.AllowLocalBindingSet = false
	spec, err = NewProxySpecForPlatformWithLocalBinding(
		ProxyConfig{Network: plain},
		nil,
		true,
		utils.PlatformLinux,
		LocalBindingDefaultFalse,
	)
	if err != nil {
		t.Fatal(err)
	}
	config = spec.Config()
	if !config.Network.AllowLocalBindingSet || config.Network.AllowLocalBinding {
		t.Fatalf("non-MXC omitted local binding = %v/%v, want explicit false", config.Network.AllowLocalBinding, config.Network.AllowLocalBindingSet)
	}
}
