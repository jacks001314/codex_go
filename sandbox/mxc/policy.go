package mxc

import "errors"

// MXC's native policy uses explicit read/write/deny lists. Kept separate from
// Codex's profile so a profile can be inspected without adding native grants.
type FileSystemPolicy struct {
	ReadWritePaths []string
	ReadOnlyPaths  []string
	DeniedPaths    []string
}

type NetworkAction string

const (
	NetworkActionAllow NetworkAction = "allow"
	NetworkActionDeny  NetworkAction = "deny"
)

type NetworkCIDR struct {
	Address   string `json:"address"`
	PrefixLen uint8  `json:"prefix_length"`
}

type NetworkPeer struct {
	CIDR   NetworkCIDR   `json:"cidr"`
	Except []NetworkCIDR `json:"except,omitempty"`
}

type NetworkRule struct {
	To    []NetworkPeer `json:"to"`
	Ports []uint16      `json:"ports,omitempty"`
}

type NetworkEgressPolicy struct {
	Default NetworkAction `json:"default"`
	Allow   []NetworkRule `json:"allow,omitempty"`
}

type NetworkIngressPolicy struct {
	Default      NetworkAction `json:"default"`
	HostLoopback NetworkAction `json:"host_loopback"`
}

// TranslateNetworkPolicy mirrors Rust #44872. A proxied sandbox denies direct
// non-loopback egress, keeps general ingress denied, and grants host loopback
// only when the caller permits local binding.
func TranslateNetworkPolicy(networkEnabled bool, managedNetwork *ManagedNetworkContext) (NetworkEgressPolicy, NetworkIngressPolicy, error) {
	if err := ValidateManagedNetwork(managedNetwork); err != nil {
		return NetworkEgressPolicy{}, NetworkIngressPolicy{}, err
	}
	proxied := managedNetwork != nil
	defaultAction := NetworkActionAllow
	if proxied || !networkEnabled {
		defaultAction = NetworkActionDeny
	}
	egress := NetworkEgressPolicy{Default: defaultAction}
	if proxied {
		egress.Allow = append(egress.Allow, NetworkRule{
			To: []NetworkPeer{
				{CIDR: NetworkCIDR{Address: "127.0.0.0", PrefixLen: 8}},
				{CIDR: NetworkCIDR{Address: "::1", PrefixLen: 128}},
			},
		})
	}
	hostLoopback := NetworkActionDeny
	if networkEnabled || proxied {
		hostLoopback = NetworkActionAllow
	}
	return egress, NetworkIngressPolicy{Default: defaultAction, HostLoopback: hostLoopback}, nil
}

func errUnsupportedPlatform() error {
	return errors.New("MXC policy preparation requires Windows")
}
