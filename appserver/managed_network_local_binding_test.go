package appserver

import (
	"testing"

	"codex_go/network"
	"codex_go/utils"
)

func TestManagedNetworkLocalBindingPolicyForMXC(t *testing.T) {
	cases := []struct {
		name   string
		values map[string]any
		want   network.LocalBindingPolicy
	}{
		{name: "explicit mxc", values: map[string]any{"windows": map[string]any{"sandbox": "mxc"}}, want: network.LocalBindingRequireTrue},
		{name: "prefer mxc", values: map[string]any{"features": map[string]any{"prefer_mxc": true}}, want: network.LocalBindingRequireTrue},
		{name: "default", values: map[string]any{}, want: network.LocalBindingDefaultFalse},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := managedNetworkLocalBindingPolicyForPlatform(testCase.values, utils.PlatformWindows); got != testCase.want {
				t.Fatalf("policy = %v, want %v", got, testCase.want)
			}
		})
	}
	if got := managedNetworkLocalBindingPolicyForPlatform(map[string]any{"windows": map[string]any{"sandbox": "mxc"}}, utils.PlatformLinux); got != network.LocalBindingDefaultFalse {
		t.Fatalf("non-Windows executor policy = %v, want default false", got)
	}
}
