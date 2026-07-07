package wfp

type ConditionKind string

const (
	ConditionUser       ConditionKind = "user"
	ConditionProtocol   ConditionKind = "protocol"
	ConditionRemotePort ConditionKind = "remote_port"
)

const (
	LayerALEAuthConnectV4              = "{c38d57d1-05a7-4c33-904f-7fbceee60e82}"
	LayerALEAuthConnectV6              = "{4a72393b-319f-44bc-84c3-ba54dcb3b6b4}"
	LayerALEResourceAssignmentV4       = "{1247d66d-0b60-4a15-8d44-7155d0f53a0c}"
	LayerALEResourceAssignmentV6       = "{55a650e1-5f0a-4eca-a653-88f53b26aa8c}"
	ConditionALEUserID                 = "{af043a0a-b34d-4f86-979c-c90371af6e66}"
	ConditionIPProtocol                = "{3971ef2b-623e-4f9a-8cb1-6e79b806b9a7}"
	ConditionIPRemotePort              = "{c35a604d-d22b-4e1a-91b4-68f674ee674b}"
	ProtocolICMP                 uint8 = 1
	ProtocolICMPv6               uint8 = 58
)

type ConditionSpec struct {
	Kind       ConditionKind
	Protocol   uint8
	RemotePort uint16
}

type FilterSpec struct {
	Key         string
	Name        string
	Description string
	LayerKey    string
	Conditions  []ConditionSpec
}

func DefaultFilterSpecs() []FilterSpec {
	return cloneFilterSpecs(filterSpecs)
}

var filterSpecs = []FilterSpec{
	{
		Key:         "{9f5f3812-79f0-4fe9-9615-4c2c92d2f0ff}",
		Name:        "codex_wfp_icmp_connect_v4",
		Description: "Block sandbox-account ICMP connect v4",
		LayerKey:    LayerALEAuthConnectV4,
		Conditions:  []ConditionSpec{userCondition(), protocolCondition(ProtocolICMP)},
	},
	{
		Key:         "{87498484-45ab-4510-845e-ece8b791b3bc}",
		Name:        "codex_wfp_icmp_connect_v6",
		Description: "Block sandbox-account ICMP connect v6",
		LayerKey:    LayerALEAuthConnectV6,
		Conditions:  []ConditionSpec{userCondition(), protocolCondition(ProtocolICMPv6)},
	},
	{
		Key:         "{af4751de-f874-4a7b-a34d-f0d0f22d1d9b}",
		Name:        "codex_wfp_icmp_assign_v4",
		Description: "Block sandbox-account ICMP resource assignment v4",
		LayerKey:    LayerALEResourceAssignmentV4,
		Conditions:  []ConditionSpec{userCondition(), protocolCondition(ProtocolICMP)},
	},
	{
		Key:         "{ea10db66-a928-4b2e-a82e-a376a54f93ba}",
		Name:        "codex_wfp_icmp_assign_v6",
		Description: "Block sandbox-account ICMP resource assignment v6",
		LayerKey:    LayerALEResourceAssignmentV6,
		Conditions:  []ConditionSpec{userCondition(), protocolCondition(ProtocolICMPv6)},
	},
	{
		Key:         "{83172805-f6be-4ae1-9dc6-6847aef04e7f}",
		Name:        "codex_wfp_dns_53_v4",
		Description: "Block sandbox-account DNS TCP or UDP port 53 v4",
		LayerKey:    LayerALEAuthConnectV4,
		Conditions:  []ConditionSpec{userCondition(), remotePortCondition(53)},
	},
	{
		Key:         "{d23b2efb-1efb-46b2-96f3-b0ccda5690c8}",
		Name:        "codex_wfp_dns_53_v6",
		Description: "Block sandbox-account DNS TCP or UDP port 53 v6",
		LayerKey:    LayerALEAuthConnectV6,
		Conditions:  []ConditionSpec{userCondition(), remotePortCondition(53)},
	},
	{
		Key:         "{420b026f-9dc9-4aea-88f4-0f2b9feab39a}",
		Name:        "codex_wfp_dns_853_v4",
		Description: "Block sandbox-account DNS-over-TLS port 853 v4",
		LayerKey:    LayerALEAuthConnectV4,
		Conditions:  []ConditionSpec{userCondition(), remotePortCondition(853)},
	},
	{
		Key:         "{8d917c81-99cc-45e7-84d6-824df860cfb8}",
		Name:        "codex_wfp_dns_853_v6",
		Description: "Block sandbox-account DNS-over-TLS port 853 v6",
		LayerKey:    LayerALEAuthConnectV6,
		Conditions:  []ConditionSpec{userCondition(), remotePortCondition(853)},
	},
	{
		Key:         "{e1d6e0af-ce5f-471b-b2d3-15ca00e966f3}",
		Name:        "codex_wfp_smb_445_v4",
		Description: "Block sandbox-account SMB port 445 v4",
		LayerKey:    LayerALEAuthConnectV4,
		Conditions:  []ConditionSpec{userCondition(), remotePortCondition(445)},
	},
	{
		Key:         "{c2bceca4-66ef-4a0f-ba80-f4f761b8c6f0}",
		Name:        "codex_wfp_smb_445_v6",
		Description: "Block sandbox-account SMB port 445 v6",
		LayerKey:    LayerALEAuthConnectV6,
		Conditions:  []ConditionSpec{userCondition(), remotePortCondition(445)},
	},
	{
		Key:         "{ba10c618-84e7-4b83-8f74-36e22b2fa1ff}",
		Name:        "codex_wfp_smb_139_v4",
		Description: "Block sandbox-account SMB port 139 v4",
		LayerKey:    LayerALEAuthConnectV4,
		Conditions:  []ConditionSpec{userCondition(), remotePortCondition(139)},
	},
	{
		Key:         "{fe7f22b8-5cf5-4adb-b2aa-71fc0a8f5d44}",
		Name:        "codex_wfp_smb_139_v6",
		Description: "Block sandbox-account SMB port 139 v6",
		LayerKey:    LayerALEAuthConnectV6,
		Conditions:  []ConditionSpec{userCondition(), remotePortCondition(139)},
	},
}

func userCondition() ConditionSpec {
	return ConditionSpec{Kind: ConditionUser}
}

func protocolCondition(protocol uint8) ConditionSpec {
	return ConditionSpec{Kind: ConditionProtocol, Protocol: protocol}
}

func remotePortCondition(port uint16) ConditionSpec {
	return ConditionSpec{Kind: ConditionRemotePort, RemotePort: port}
}

func cloneFilterSpecs(specs []FilterSpec) []FilterSpec {
	out := make([]FilterSpec, len(specs))
	for i, spec := range specs {
		out[i] = spec
		out[i].Conditions = append([]ConditionSpec(nil), spec.Conditions...)
	}
	return out
}
