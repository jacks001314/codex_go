package mxc

import (
	"strings"
	"testing"
)

func TestValidateManagedNetworkLikeRust(t *testing.T) {
	for _, test := range []struct {
		name    string
		network *ManagedNetworkContext
		wantErr string
	}{
		{name: "disabled"},
		{name: "missing ports", network: &ManagedNetworkContext{AllowLocalBinding: true}, wantErr: "dedicated proxy ports"},
		{name: "zero port", network: &ManagedNetworkContext{LoopbackPorts: []uint16{0}, AllowLocalBinding: true}, wantErr: "nonzero"},
		{name: "client only", network: &ManagedNetworkContext{LoopbackPorts: []uint16{43123}}, wantErr: "allow_local_binding=false"},
		{name: "valid", network: &ManagedNetworkContext{LoopbackPorts: []uint16{43123, 43124}, AllowLocalBinding: true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateManagedNetwork(test.network)
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateManagedNetwork() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("ValidateManagedNetwork() error = %v, want %q", err, test.wantErr)
			}
		})
	}
}

func TestTranslateNetworkPolicyLikeRust(t *testing.T) {
	disabled, disabledIngress, err := TranslateNetworkPolicy(false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if disabled.Default != NetworkActionDeny || disabledIngress.Default != NetworkActionDeny || disabledIngress.HostLoopback != NetworkActionDeny {
		t.Fatalf("disabled network = %#v %#v", disabled, disabledIngress)
	}

	enabled, enabledIngress, err := TranslateNetworkPolicy(true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if enabled.Default != NetworkActionAllow || enabledIngress.Default != NetworkActionAllow || enabledIngress.HostLoopback != NetworkActionAllow {
		t.Fatalf("enabled network = %#v %#v", enabled, enabledIngress)
	}

	proxied, proxiedIngress, err := TranslateNetworkPolicy(true, &ManagedNetworkContext{
		LoopbackPorts:     []uint16{43123, 43124},
		AllowLocalBinding: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if proxied.Default != NetworkActionDeny || proxiedIngress.Default != NetworkActionDeny ||
		proxiedIngress.HostLoopback != NetworkActionAllow {
		t.Fatalf("proxied network = %#v %#v", proxied, proxiedIngress)
	}
	if len(proxied.Allow) != 1 || len(proxied.Allow[0].To) != 2 ||
		proxied.Allow[0].To[0].CIDR.Address != "127.0.0.0" ||
		proxied.Allow[0].To[0].CIDR.PrefixLen != 8 ||
		proxied.Allow[0].To[1].CIDR.Address != "::1" ||
		proxied.Allow[0].To[1].CIDR.PrefixLen != 128 {
		t.Fatalf("loopback egress rule = %#v", proxied.Allow)
	}
}
