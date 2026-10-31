package servicepolicy

import (
	"reflect"
	"testing"

	"codex_go/config"
	"codex_go/sandbox/windowssandbox/provisionservice"
)

func boolPointer(value bool) *bool     { return &value }
func portPointer(value uint16) *uint16 { return &value }

func TestNoRequirementsPermitsEverything(t *testing.T) {
	settings := provisionservice.Settings{ProxyPorts: []uint16{1, 2}, AllowLocalBinding: true}
	if err := ValidateProvisioningRequirements(settings, provisionservice.Listeners{}, nil); err != nil {
		t.Fatalf("error = %v", err)
	}
}

func TestNetworkDisabledRefusesProxySettings(t *testing.T) {
	requirements := &config.ConfigRequirements{Network: &config.NetworkRequirements{Enabled: boolPointer(false)}}
	if err := ValidateProvisioningRequirements(provisionservice.Settings{ProxyPorts: []uint16{7897}}, provisionservice.Listeners{}, requirements); err == nil {
		t.Fatal("proxy ports must be refused when managed network is disabled")
	}
	if err := ValidateProvisioningRequirements(provisionservice.Settings{AllowLocalBinding: true}, provisionservice.Listeners{}, requirements); err == nil {
		t.Fatal("local binding must be refused when managed network is disabled")
	}
	if err := ValidateProvisioningRequirements(provisionservice.Settings{}, provisionservice.Listeners{}, requirements); err != nil {
		t.Fatalf("empty settings error = %v", err)
	}
}

func TestLocalBindingRequirement(t *testing.T) {
	requirements := &config.ConfigRequirements{Network: &config.NetworkRequirements{AllowLocalBinding: boolPointer(false)}}
	if err := ValidateProvisioningRequirements(provisionservice.Settings{AllowLocalBinding: true}, provisionservice.Listeners{}, requirements); err == nil {
		t.Fatal("managed policy must refuse local binding")
	}
	if err := ValidateProvisioningRequirements(provisionservice.Settings{}, provisionservice.Listeners{}, requirements); err != nil {
		t.Fatalf("error = %v", err)
	}
}

func TestRequiredProxyPortsMustMatchListeners(t *testing.T) {
	requirements := &config.ConfigRequirements{Network: &config.NetworkRequirements{
		HTTPPort:  portPointer(7897),
		SOCKSPort: portPointer(1080),
	}}
	if err := ValidateProvisioningRequirements(
		provisionservice.Settings{ProxyPorts: []uint16{7897, 1080}},
		provisionservice.Listeners{HTTPPorts: []uint16{7897}, SOCKSPorts: []uint16{1080}},
		requirements,
	); err != nil {
		t.Fatalf("error = %v", err)
	}
	if err := ValidateProvisioningRequirements(
		provisionservice.Settings{},
		provisionservice.Listeners{HTTPPorts: []uint16{8080}},
		requirements,
	); err == nil {
		t.Fatal("a different HTTP listener port must be refused")
	}
	if err := ValidateProvisioningRequirements(
		provisionservice.Settings{},
		provisionservice.Listeners{SOCKSPorts: []uint16{9050}},
		requirements,
	); err == nil {
		t.Fatal("a different SOCKS listener port must be refused")
	}
}

func TestUnclassifiedProxyPortsAreRefused(t *testing.T) {
	requirements := &config.ConfigRequirements{Network: &config.NetworkRequirements{
		HTTPPort:  portPointer(7897),
		SOCKSPort: portPointer(1080),
	}}
	// A saved port that is neither the required ports nor a classified listener
	// must not bypass the managed restriction.
	err := ValidateProvisioningRequirements(
		provisionservice.Settings{ProxyPorts: []uint16{7897, 9999}},
		provisionservice.Listeners{HTTPPorts: []uint16{7897}},
		requirements,
	)
	if err == nil {
		t.Fatal("unclassified proxy port must be refused")
	}
	want := "managed policy does not permit unclassified proxy port 9999"
	if err.Error() != want {
		t.Fatalf("error = %v, want %s", err, want)
	}
}

func TestAllowedImplementationsMustPermitElevated(t *testing.T) {
	requirements := &config.ConfigRequirements{
		AllowedWindowsSandboxImplementations: []config.WindowsSandboxImplementation{config.WindowsSandboxImplementation("unelevated")},
	}
	if err := ValidateProvisioningRequirements(provisionservice.Settings{}, provisionservice.Listeners{}, requirements); err == nil {
		t.Fatal("managed policy must refuse elevated setup")
	}
	allowed := &config.ConfigRequirements{
		AllowedWindowsSandboxImplementations: []config.WindowsSandboxImplementation{config.WindowsSandboxImplementation("elevated")},
	}
	if err := ValidateProvisioningRequirements(provisionservice.Settings{}, provisionservice.Listeners{}, allowed); err != nil {
		t.Fatalf("error = %v", err)
	}
}

func TestRequiredPortSetIsNotMutated(t *testing.T) {
	requirements := &config.ConfigRequirements{Network: &config.NetworkRequirements{HTTPPort: portPointer(7897)}}
	settings := provisionservice.Settings{ProxyPorts: []uint16{7897}}
	before := append([]uint16{}, settings.ProxyPorts...)
	_ = ValidateProvisioningRequirements(settings, provisionservice.Listeners{HTTPPorts: []uint16{7897}}, requirements)
	if !reflect.DeepEqual(settings.ProxyPorts, before) {
		t.Fatalf("settings mutated: %v", settings.ProxyPorts)
	}
}
