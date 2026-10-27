package execserver

import (
	"context"
	"strings"
	"testing"
)

func TestSupportsCapabilityDiscoveryV2VersionGateLikeRust(t *testing.T) {
	tests := []struct {
		name          string
		version       string
		capability    bool
		wantSupported bool
	}{
		{name: "minimum stable", version: "0.164.0", capability: true, wantSupported: true},
		{name: "newer stable with build", version: "0.168.0+build", capability: true, wantSupported: true},
		{name: "older", version: "0.163.0", capability: true},
		{name: "cutoff prerelease", version: "0.164.0-alpha.1", capability: true},
		{name: "next minor prerelease", version: "0.165.0-alpha.1", capability: true, wantSupported: true},
		{name: "missing version", version: "", capability: true},
		{name: "malformed version", version: "invalid", capability: true},
		{name: "shorthand version", version: "0.164", capability: true},
		{name: "v-prefixed version", version: "v0.164.0", capability: true},
		{name: "capability absent", version: "0.168.0"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			info := &EnvironmentInfo{
				ExecutorVersion: test.version,
				Capabilities:    EnvironmentCapabilities{CapabilityDiscoveryV2: test.capability},
			}
			if got := SupportsCapabilityDiscoveryV2(info); got != test.wantSupported {
				t.Fatalf("SupportsCapabilityDiscoveryV2() = %v, want %v", got, test.wantSupported)
			}
		})
	}
	if SupportsCapabilityDiscoveryV2(nil) {
		t.Fatal("nil environment info reported V2 support")
	}
}

func TestClientDiscoverCapabilitiesV2RejectsIncompatibleExecutorBeforeRPC(t *testing.T) {
	client := &Client{}
	client.rememberEnvironmentInfo(&EnvironmentInfo{
		ExecutorVersion: "0.163.0",
		Capabilities:    EnvironmentCapabilities{CapabilityDiscoveryV2: true},
	})
	_, err := client.DiscoverCapabilitiesV2(context.Background(), &DiscoverV2CapabilitiesParams{CWD: "file:///repo"})
	if err == nil || !strings.Contains(err.Error(), "capabilities/discoverV2 is unavailable in this environment session") {
		t.Fatalf("DiscoverCapabilitiesV2() error = %v, want the compatibility rejection", err)
	}
}
