//go:build windows

package windowsandboxservice

import (
	"context"
	"errors"
	"strings"
	"testing"

	"codex_go/config"
	"codex_go/sandbox/windowssandbox/provisionservice"
)

var machinePolicyNetworkTables = []string{"network_proxy", "experimental_network", "permissions.network"}

func validateRequirementsFromTOML(t *testing.T, settings provisionservice.Settings, listeners provisionservice.Listeners, contents string) error {
	t.Helper()
	requirements, err := config.ParseRequirementsTOML([]byte(contents))
	if err != nil {
		return err
	}
	return ValidateProvisioningRequirements(settings, listeners, requirements)
}

func TestOnlyRegisteredRefreshSkipsConfigurationLoading(t *testing.T) {
	home := t.TempDir()
	for _, testCase := range []struct {
		registeredCore bool
		refreshOnly    bool
	}{
		{registeredCore: true, refreshOnly: true},
		{registeredCore: true, refreshOnly: false},
		{registeredCore: false, refreshOnly: false},
		{registeredCore: false, refreshOnly: true},
	} {
		loaded := false
		loader := func(context.Context, string) (*config.ConfigRequirements, error) {
			loaded = true
			return nil, errors.New("the configuration loader must not start for a refresh")
		}
		request := &ProvisioningRequest{CodexHome: home, RegisteredCore: testCase.registeredCore, RefreshOnly: testCase.refreshOnly}
		// A zero token makes any attempt to load configuration fail closed.
		err := ValidateProvisioningRequest(context.Background(), home, request, 0, loader)
		wantOK := testCase.registeredCore && testCase.refreshOnly
		if (err == nil) != wantOK {
			t.Fatalf("ValidateProvisioningRequest(%+v) error = %v, want ok = %t", testCase, err, wantOK)
		}
		if err != nil && !strings.Contains(err.Error(), "failed to impersonate") {
			t.Fatalf("ValidateProvisioningRequest(%+v) error = %v", testCase, err)
		}
		if loaded {
			t.Fatalf("ValidateProvisioningRequest(%+v) started the configuration loader", testCase)
		}
	}
}

func TestUnmanagedPolicyAllowsDefaultAndRequestedNetworkSettings(t *testing.T) {
	if err := validateRequirementsFromTOML(t, provisionservice.Settings{}, provisionservice.Listeners{}, ""); err != nil {
		t.Fatalf("unmanaged policy rejected defaults: %v", err)
	}
	err := validateRequirementsFromTOML(t,
		provisionservice.Settings{ProxyPorts: []uint16{3128, 8080, 9000}, AllowLocalBinding: true},
		provisionservice.Listeners{HTTPPorts: []uint16{3128}, SOCKSPorts: []uint16{8080}},
		"allowed_sandbox_modes = [\"read-only\", \"workspace-write\"]",
	)
	if err != nil {
		t.Fatalf("unmanaged policy rejected requested settings: %v", err)
	}
}

func TestElevatedSandboxIsAllowedWhenIncluded(t *testing.T) {
	err := validateRequirementsFromTOML(t, provisionservice.Settings{}, provisionservice.Listeners{},
		"[windows]\nallowed_sandbox_implementations = [\"unelevated\", \"elevated\"]")
	if err != nil {
		t.Fatalf("policy allowing elevated was rejected: %v", err)
	}
}

func TestElevatedSandboxIsRejectedWhenProhibitedOrEmpty(t *testing.T) {
	for _, implementations := range []string{"[\"unelevated\"]", "[]"} {
		policy := "[windows]\nallowed_sandbox_implementations = " + implementations
		if err := validateRequirementsFromTOML(t, provisionservice.Settings{}, provisionservice.Listeners{}, policy); err == nil {
			t.Fatalf("policy %q admitted the elevated sandbox", implementations)
		}
	}
}

func TestForbiddenLocalBindingIsRejected(t *testing.T) {
	settings := provisionservice.Settings{AllowLocalBinding: true}
	for _, table := range machinePolicyNetworkTables {
		policy := "[" + table + "]\nallow_local_binding = false"
		err := validateRequirementsFromTOML(t, settings, provisionservice.Listeners{}, policy)
		if err == nil || err.Error() != "managed policy does not permit local network binding" {
			t.Fatalf("%s: error = %v", table, err)
		}
	}
}

func TestDisabledNetworkRejectsProxyPortsAndLocalBinding(t *testing.T) {
	for _, table := range machinePolicyNetworkTables {
		policy := "[" + table + "]\nenabled = false"
		if err := validateRequirementsFromTOML(t, provisionservice.Settings{}, provisionservice.Listeners{}, policy); err != nil {
			t.Fatalf("%s: defaults were rejected: %v", table, err)
		}
		for _, settings := range []provisionservice.Settings{
			{ProxyPorts: []uint16{3128}},
			{AllowLocalBinding: true},
		} {
			if err := validateRequirementsFromTOML(t, settings, provisionservice.Listeners{}, policy); err == nil {
				t.Fatalf("%s: settings %+v were admitted by a disabled network", table, settings)
			}
		}
	}
}

func TestEnabledNetworkAllowsUnrestrictedProxyPorts(t *testing.T) {
	err := validateRequirementsFromTOML(t,
		provisionservice.Settings{ProxyPorts: []uint16{3128, 8080, 8081, 9000}},
		provisionservice.Listeners{HTTPPorts: []uint16{3128, 8080}, SOCKSPorts: []uint16{8081}},
		"[experimental_network]\nenabled = true",
	)
	if err != nil {
		t.Fatalf("enabled network rejected proxy ports: %v", err)
	}
}

func TestDisabledLocalBindingIsAllowedWhenPolicyPermitsBinding(t *testing.T) {
	err := validateRequirementsFromTOML(t, provisionservice.Settings{}, provisionservice.Listeners{},
		"[experimental_network]\nallow_local_binding = true")
	if err != nil {
		t.Fatalf("policy permitting binding rejected defaults: %v", err)
	}
}

func TestManagedProxyPortsAllowOnlyConfiguredPorts(t *testing.T) {
	allowed := provisionservice.Settings{ProxyPorts: []uint16{1080, 3128}}
	prohibited := provisionservice.Settings{ProxyPorts: []uint16{1080, 3128, 8080}}
	for _, table := range machinePolicyNetworkTables {
		policy := "[" + table + "]\nhttp_port = 3128\nsocks_port = 1080"
		for _, listeners := range []provisionservice.Listeners{
			{HTTPPorts: []uint16{3128}, SOCKSPorts: []uint16{1080}},
			{},
		} {
			if err := validateRequirementsFromTOML(t, allowed, listeners, policy); err != nil {
				t.Fatalf("%s: allowed listeners %+v were rejected: %v", table, listeners, err)
			}
		}
		for _, listeners := range []provisionservice.Listeners{
			{HTTPPorts: []uint16{3128, 8080}, SOCKSPorts: []uint16{1080}},
			{HTTPPorts: []uint16{3128}, SOCKSPorts: []uint16{1080, 8080}},
			{HTTPPorts: []uint16{3128}, SOCKSPorts: []uint16{1080}},
			{},
		} {
			if err := validateRequirementsFromTOML(t, prohibited, listeners, policy); err == nil {
				t.Fatalf("%s: prohibited listeners %+v were admitted", table, listeners)
			}
		}
	}
}

func TestManagedHTTPPortDoesNotRestrictTheUnmanagedSOCKSListener(t *testing.T) {
	err := validateRequirementsFromTOML(t,
		provisionservice.Settings{ProxyPorts: []uint16{3128, 8081}},
		provisionservice.Listeners{HTTPPorts: []uint16{3128}, SOCKSPorts: []uint16{8081}},
		"[experimental_network]\nhttp_port = 3128",
	)
	if err != nil {
		t.Fatalf("managed HTTP port restricted the SOCKS listener: %v", err)
	}
}

func TestManagedSOCKSPortDoesNotRestrictTheUnmanagedHTTPListener(t *testing.T) {
	err := validateRequirementsFromTOML(t,
		provisionservice.Settings{ProxyPorts: []uint16{1080, 3128}},
		provisionservice.Listeners{HTTPPorts: []uint16{3128}, SOCKSPorts: []uint16{1080}},
		"[experimental_network]\nsocks_port = 1080",
	)
	if err != nil {
		t.Fatalf("managed SOCKS port restricted the HTTP listener: %v", err)
	}
}

func TestManagedProxyPortsRejectSwappedListenerRoles(t *testing.T) {
	err := validateRequirementsFromTOML(t,
		provisionservice.Settings{ProxyPorts: []uint16{1080, 3128}},
		provisionservice.Listeners{HTTPPorts: []uint16{1080}, SOCKSPorts: []uint16{3128}},
		"[experimental_network]\nhttp_port = 3128\nsocks_port = 1080",
	)
	if err == nil {
		t.Fatal("swapped listener roles were admitted")
	}
}

func TestManagedSOCKSPortRejectsAMismatchedSOCKSListener(t *testing.T) {
	err := validateRequirementsFromTOML(t,
		provisionservice.Settings{ProxyPorts: []uint16{1080, 3128, 8081}},
		provisionservice.Listeners{HTTPPorts: []uint16{3128}, SOCKSPorts: []uint16{1080, 8081}},
		"[experimental_network]\nhttp_port = 3128\nsocks_port = 1080",
	)
	if err == nil {
		t.Fatal("mismatched SOCKS listener was admitted")
	}
}

func TestManagedSOCKSPortDoesNotRequireADisabledSOCKSListener(t *testing.T) {
	err := validateRequirementsFromTOML(t,
		provisionservice.Settings{ProxyPorts: []uint16{3128}},
		provisionservice.Listeners{HTTPPorts: []uint16{3128}},
		"[experimental_network]\nhttp_port = 3128\nsocks_port = 1080",
	)
	if err != nil {
		t.Fatalf("a disabled SOCKS listener was required: %v", err)
	}
}

func TestMalformedMachinePolicyFailsClosed(t *testing.T) {
	for _, table := range machinePolicyNetworkTables {
		policy := "[" + table + "]\nhttp_port = \"invalid\""
		if err := validateRequirementsFromTOML(t, provisionservice.Settings{}, provisionservice.Listeners{}, policy); err == nil {
			t.Fatalf("%s: a malformed port was accepted", table)
		}
	}
	for _, table := range []string{"network_proxy", "experimental_network"} {
		policy := "[" + table + "]\nenabled = true\n[permissions.network]\nenabled = true"
		if err := validateRequirementsFromTOML(t, provisionservice.Settings{}, provisionservice.Listeners{}, policy); err == nil {
			t.Fatalf("%s: conflicting network tables were accepted", table)
		}
	}
	if err := validateRequirementsFromTOML(t, provisionservice.Settings{}, provisionservice.Listeners{},
		"[windows]\nallowed_sandbox_implementations = [\"invalid\"]"); err == nil {
		t.Fatal("an invalid sandbox implementation was accepted")
	}
}

func TestValidateProvisioningRequestEnforcesTheLoadedRequirements(t *testing.T) {
	home := t.TempDir()
	requirements := &config.ConfigRequirements{
		Network: &config.NetworkRequirements{AllowLocalBinding: new(bool)},
	}
	loader := func(context.Context, string) (*config.ConfigRequirements, error) {
		return requirements, nil
	}
	request := &ProvisioningRequest{CodexHome: home, Settings: provisionservice.Settings{AllowLocalBinding: true}}
	err := ValidateProvisioningRequest(context.Background(), home, request, openTestToken(t), loader)
	if err == nil || !strings.Contains(err.Error(), "enforce managed provisioning requirements") {
		t.Fatalf("ValidateProvisioningRequest() error = %v", err)
	}
	request.Settings.AllowLocalBinding = false
	if err := ValidateProvisioningRequest(context.Background(), home, request, openTestToken(t), loader); err != nil {
		t.Fatalf("ValidateProvisioningRequest() rejected a permitted request: %v", err)
	}
}

func TestValidateProvisioningRequestFailsClosedWhenPolicyCannotLoad(t *testing.T) {
	home := t.TempDir()
	loader := func(context.Context, string) (*config.ConfigRequirements, error) {
		return nil, errors.New("managed configuration is unavailable")
	}
	request := &ProvisioningRequest{CodexHome: home}
	err := ValidateProvisioningRequest(context.Background(), home, request, openTestToken(t), loader)
	if err == nil || !strings.Contains(err.Error(), "managed configuration is unavailable") {
		t.Fatalf("ValidateProvisioningRequest() error = %v", err)
	}
}
