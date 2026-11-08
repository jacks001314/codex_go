package mxc

import (
	"fmt"
	"net"
	"sort"
	"strings"

	flatbuffers "github.com/google/flatbuffers/go"
)

// PSEC (process security environment) wire format.
//
// The layout is Microsoft's `ProcessSecurityEnvironment.fbs` from the MXC SDK
// (microsoft/mxc, rev b21120168d70befe12356411423266bfef168446,
// `external/windows-sdk/ProcessSecurityEnvironment.fbs`): namespace
// `ProcessSecurityEnvironmentLayout`, root type `ProcessSecurityEnvironment`,
// file identifier "PSEC", schema version 1.0 / current 1.1. Field slots below
// follow that schema verbatim; nested tables are encoded in the same order
// mxc-sdk's `build_psec_v1_security_environment_spec` writes them
// (`backends/process_container/common/base_container_helpers.rs`).
const (
	psecFileIdentifier = "PSEC"
	psecVersionMajor   = 1
	psecVersionMinor   = 1

	// ProcessSecurityEnvironment slots.
	psecSlotVersion         = 0
	psecSlotCapabilities    = 1
	psecSlotDisallowWin32k  = 2
	psecSlotUIRestrictions  = 3
	psecSlotReadWrite       = 4
	psecSlotReadOnly        = 5
	psecSlotDeny            = 6
	psecSlotNetworkPolicy   = 7
	psecSlotEnumerate       = 8
	psecProcessSecurityEnvFields = 9

	// NetworkPolicy slots.
	psecNetworkFields        = 4
	psecSlotNetworkProxy     = 0
	psecSlotNetworkEgress    = 1
	psecSlotNetworkPeer      = 2
	psecSlotNetworkIngress   = 3

	// EndpointPolicy slots.
	psecEndpointPolicyFields = 3
	psecSlotEndpointDefault  = 0
	psecSlotEndpointAllow    = 1
	psecSlotEndpointDeny     = 2

	// EndpointRule slots.
	psecEndpointRuleFields  = 2
	psecSlotRuleDestinations = 0
	psecSlotRulePorts        = 1

	// DestinationRule slots.
	psecDestinationRuleFields = 2
	psecSlotDestinationSubnet = 0
	psecSlotDestinationExcept = 1

	// IpSubnet slots.
	psecIpSubnetFields       = 2
	psecSlotSubnetAddress    = 0
	psecSlotSubnetPrefixLen  = 1

	// PortRule slots.
	psecPortRuleFields     = 3
	psecSlotPortProtocol   = 0
	psecSlotPortStart      = 1
	psecSlotPortEnd        = 2
)

// PSEC enum values (`FilterAction`, `IpProtocol`).
const (
	psecFilterDeny  byte = 0
	psecFilterAllow byte = 1

	psecProtocolAny    byte = 0
	psecProtocolTCP    byte = 1
	psecProtocolUDP    byte = 2
	psecProtocolICMPv4 byte = 3
	psecProtocolICMPv6 byte = 4
)

// PSEC-capability names mxc-sdk manages itself; callers cannot pin them.
const (
	psecCapabilityInternetClient        = "internetClient"
	psecCapabilityPrivateNetwork        = "privateNetworkClientServer"
	psecCapabilityNetworkLoopback       = "networkLoopback"
	psecLoopbackNetworkPeer             = "MXC-Loopback"
)

var psecPolicyOwnedCapabilities = []string{
	psecCapabilityInternetClient,
	"internetClientServer",
	psecCapabilityPrivateNetwork,
	psecCapabilityNetworkLoopback,
}

// SecurityEnvironmentSpec is the resolved policy that becomes the `"PSEC"`
// FlatBuffer handed to `CreateProcessSecurityEnvironment`. It mirrors
// `ProcessSecurityEnvironmentT` so tests can assert field-by-field.
type SecurityEnvironmentSpec struct {
	Major                       uint16
	Minor                       uint16
	Capabilities                []string
	DisallowWin32kSystemCalls   bool
	UIRestrictions              uint64
	ReadWritePaths              []string
	ReadOnlyPaths               []string
	DeniedPaths                 []string
	EnumeratePaths              []string
	Network                     *SecurityEnvironmentNetwork
}

// SecurityEnvironmentNetwork mirrors `NetworkPolicyT`: either a proxy URL or an
// egress policy, plus the optional AppContainer peer identity.
type SecurityEnvironmentNetwork struct {
	ProxyURL             string
	Egress               *SecurityEnvironmentEgress
	AllowedAppContainerPeer string
}

// SecurityEnvironmentEgress mirrors `EndpointPolicyT`.
type SecurityEnvironmentEgress struct {
	Default byte
	Allow   []SecurityEnvironmentEndpointRule
	Deny    []SecurityEnvironmentEndpointRule
}

// SecurityEnvironmentEndpointRule mirrors `EndpointRuleT`.
type SecurityEnvironmentEndpointRule struct {
	Destinations []SecurityEnvironmentDestination
	Ports        []SecurityEnvironmentPort
}

// SecurityEnvironmentDestination mirrors `DestinationRuleT`.
type SecurityEnvironmentDestination struct {
	Address    string
	PrefixLen  uint8
	Except     []SecurityEnvironmentSubnet
}

// SecurityEnvironmentSubnet mirrors `IpSubnetT`.
type SecurityEnvironmentSubnet struct {
	Address   string
	PrefixLen uint8
}

// SecurityEnvironmentPort mirrors `PortRuleT`.
type SecurityEnvironmentPort struct {
	Protocol byte
	Start    uint16
	End      uint16
}

// BuildSecurityEnvironmentSpec serializes the spec into the exact CPSE
// FlatBuffer layout. The buffer starts with the root offset followed by the
// "PSEC" file identifier, as `flatc`/`finish_process_security_environment_buffer`
// produce it.
func BuildSecurityEnvironmentSpec(spec SecurityEnvironmentSpec) ([]byte, error) {
	major, minor := spec.Major, spec.Minor
	if major == 0 {
		major, minor = psecVersionMajor, psecVersionMinor
	}
	b := flatbuffers.NewBuilder(1024)

	network := 0
	if spec.Network != nil {
		var err error
		if network, err = buildNetworkPolicy(b, spec.Network); err != nil {
			return nil, err
		}
	}
	readWrite := buildStringVector(b, spec.ReadWritePaths)
	readOnly := buildStringVector(b, spec.ReadOnlyPaths)
	deny := buildStringVector(b, spec.DeniedPaths)
	enumerate := buildStringVector(b, spec.EnumeratePaths)
	var capabilities flatbuffers.UOffsetT
	if joined := strings.Join(nonEmptyStrings(spec.Capabilities), ","); joined != "" {
		capabilities = b.CreateString(joined)
	}

	b.StartObject(psecProcessSecurityEnvFields)
	// `version` is a required inline struct (SchemaVersion{u16,u16}).
	b.Prep(2, 4)
	b.PrependUint16(minor)
	b.PrependUint16(major)
	b.PrependStructSlot(psecSlotVersion, b.Offset(), 0)
	b.PrependUOffsetTSlot(psecSlotCapabilities, capabilities, 0)
	b.PrependBoolSlot(psecSlotDisallowWin32k, spec.DisallowWin32kSystemCalls, false)
	b.PrependUint64Slot(psecSlotUIRestrictions, spec.UIRestrictions, 0)
	b.PrependUOffsetTSlot(psecSlotReadWrite, flatbuffers.UOffsetT(readWrite), 0)
	b.PrependUOffsetTSlot(psecSlotReadOnly, flatbuffers.UOffsetT(readOnly), 0)
	b.PrependUOffsetTSlot(psecSlotDeny, flatbuffers.UOffsetT(deny), 0)
	b.PrependUOffsetTSlot(psecSlotNetworkPolicy, flatbuffers.UOffsetT(network), 0)
	b.PrependUOffsetTSlot(psecSlotEnumerate, flatbuffers.UOffsetT(enumerate), 0)
	root := b.EndObject()
	b.FinishWithFileIdentifier(root, []byte(psecFileIdentifier))
	return b.FinishedBytes(), nil
}

func buildNetworkPolicy(b *flatbuffers.Builder, network *SecurityEnvironmentNetwork) (int, error) {
	proxy := 0
	if network.ProxyURL != "" {
		url := b.CreateString(network.ProxyURL)
		b.StartObject(1)
		b.PrependUOffsetTSlot(0, flatbuffers.UOffsetT(url), 0)
		proxy = int(b.EndObject())
	}
	egress := 0
	if network.Egress != nil && network.ProxyURL == "" {
		allow := buildEndpointRules(b, network.Egress.Allow)
		deny := buildEndpointRules(b, network.Egress.Deny)
		b.StartObject(psecEndpointPolicyFields)
		b.PrependByteSlot(psecSlotEndpointDefault, network.Egress.Default, psecFilterDeny)
		b.PrependUOffsetTSlot(psecSlotEndpointAllow, flatbuffers.UOffsetT(allow), 0)
		b.PrependUOffsetTSlot(psecSlotEndpointDeny, flatbuffers.UOffsetT(deny), 0)
		egress = int(b.EndObject())
	}
	peer := 0
	if network.AllowedAppContainerPeer != "" {
		peer = int(b.CreateString(network.AllowedAppContainerPeer))
	}
	b.StartObject(psecNetworkFields)
	b.PrependUOffsetTSlot(psecSlotNetworkProxy, flatbuffers.UOffsetT(proxy), 0)
	b.PrependUOffsetTSlot(psecSlotNetworkEgress, flatbuffers.UOffsetT(egress), 0)
	b.PrependUOffsetTSlot(psecSlotNetworkPeer, flatbuffers.UOffsetT(peer), 0)
	return int(b.EndObject()), nil
}

func buildEndpointRules(b *flatbuffers.Builder, rules []SecurityEnvironmentEndpointRule) int {
	if len(rules) == 0 {
		return 0
	}
	offsets := make([]flatbuffers.UOffsetT, len(rules))
	for i := range rules {
		offsets[i] = flatbuffers.UOffsetT(buildEndpointRule(b, rules[i]))
	}
	b.StartVector(4, len(offsets), 4)
	for i := len(offsets) - 1; i >= 0; i-- {
		b.PrependUOffsetT(offsets[i])
	}
	return int(b.EndVector(len(offsets)))
}

func buildEndpointRule(b *flatbuffers.Builder, rule SecurityEnvironmentEndpointRule) int {
	destinations := 0
	if len(rule.Destinations) > 0 {
		offsets := make([]flatbuffers.UOffsetT, len(rule.Destinations))
		for i := range rule.Destinations {
			offsets[i] = flatbuffers.UOffsetT(buildDestinationRule(b, rule.Destinations[i]))
		}
		b.StartVector(4, len(offsets), 4)
		for i := len(offsets) - 1; i >= 0; i-- {
			b.PrependUOffsetT(offsets[i])
		}
		destinations = int(b.EndVector(len(offsets)))
	}
	ports := 0
	if len(rule.Ports) > 0 {
		offsets := make([]flatbuffers.UOffsetT, len(rule.Ports))
		for i, port := range rule.Ports {
			b.StartObject(psecPortRuleFields)
			b.PrependByteSlot(psecSlotPortProtocol, port.Protocol, psecProtocolAny)
			b.PrependUint16Slot(psecSlotPortStart, port.Start, 0)
			b.PrependUint16Slot(psecSlotPortEnd, port.End, 0)
			offsets[i] = b.EndObject()
		}
		b.StartVector(4, len(offsets), 4)
		for i := len(offsets) - 1; i >= 0; i-- {
			b.PrependUOffsetT(offsets[i])
		}
		ports = int(b.EndVector(len(offsets)))
	}
	b.StartObject(psecEndpointRuleFields)
	b.PrependUOffsetTSlot(psecSlotRuleDestinations, flatbuffers.UOffsetT(destinations), 0)
	b.PrependUOffsetTSlot(psecSlotRulePorts, flatbuffers.UOffsetT(ports), 0)
	return int(b.EndObject())
}

func buildDestinationRule(b *flatbuffers.Builder, destination SecurityEnvironmentDestination) int {
	subnet := buildSubnet(b, SecurityEnvironmentSubnet{Address: destination.Address, PrefixLen: destination.PrefixLen})
	except := 0
	if len(destination.Except) > 0 {
		offsets := make([]flatbuffers.UOffsetT, len(destination.Except))
		for i, entry := range destination.Except {
			offsets[i] = flatbuffers.UOffsetT(buildSubnet(b, entry))
		}
		b.StartVector(4, len(offsets), 4)
		for i := len(offsets) - 1; i >= 0; i-- {
			b.PrependUOffsetT(offsets[i])
		}
		except = int(b.EndVector(len(offsets)))
	}
	b.StartObject(psecDestinationRuleFields)
	b.PrependUOffsetTSlot(psecSlotDestinationSubnet, flatbuffers.UOffsetT(subnet), 0)
	b.PrependUOffsetTSlot(psecSlotDestinationExcept, flatbuffers.UOffsetT(except), 0)
	return int(b.EndObject())
}

func buildSubnet(b *flatbuffers.Builder, subnet SecurityEnvironmentSubnet) int {
	address := 0
	if subnet.Address != "" {
		address = int(b.CreateString(subnet.Address))
	}
	b.StartObject(psecIpSubnetFields)
	b.PrependUOffsetTSlot(psecSlotSubnetAddress, flatbuffers.UOffsetT(address), 0)
	b.PrependByteSlot(psecSlotSubnetPrefixLen, subnet.PrefixLen, 0)
	return int(b.EndObject())
}

func buildStringVector(b *flatbuffers.Builder, values []string) int {
	values = nonEmptyStrings(values)
	if len(values) == 0 {
		return 0
	}
	offsets := make([]flatbuffers.UOffsetT, len(values))
	for i, value := range values {
		offsets[i] = b.CreateString(value)
	}
	b.StartVector(4, len(offsets), 4)
	for i := len(offsets) - 1; i >= 0; i-- {
		b.PrependUOffsetT(offsets[i])
	}
	return int(b.EndVector(len(offsets)))
}

func nonEmptyStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			out = append(out, value)
		}
	}
	return out
}

// securityEnvironmentSpec resolves the helper's policy into the PSEC spec the
// way mxc-sdk's `build_psec_v1_security_environment_spec` does, including the
// capability set mxc-sdk derives from the network policy.
func (p NativeContainerPolicy) securityEnvironmentSpec() SecurityEnvironmentSpec {
	capabilities := effectiveCapabilities(p)
	unrestrictedLoopback := p.NetworkIngress.HostLoopback == NetworkActionAllow
	if unrestrictedLoopback {
		capabilities = ensureCapability(capabilities, psecCapabilityNetworkLoopback)
	}
	return SecurityEnvironmentSpec{
		Major:                     psecVersionMajor,
		Minor:                     psecVersionMinor,
		Capabilities:              capabilities,
		DisallowWin32kSystemCalls: p.UI.Disable,
		UIRestrictions:            EffectiveUIRestrictionMask(p.UI, p.BaseProcessUI),
		ReadWritePaths:            append([]string(nil), p.ReadWritePaths...),
		ReadOnlyPaths:             append([]string(nil), p.ReadOnlyPaths...),
		DeniedPaths:               append([]string(nil), p.DeniedPaths...),
		Network:                   p.securityEnvironmentNetwork(capabilities),
	}
}

func (p NativeContainerPolicy) securityEnvironmentNetwork(capabilities []string) *SecurityEnvironmentNetwork {
	network := &SecurityEnvironmentNetwork{}
	if p.NetworkIngress.HostLoopback == NetworkActionAllow {
		network.AllowedAppContainerPeer = psecLoopbackNetworkPeer
	}
	if p.ProxyURL != "" {
		// Proxy and direct egress are mutually exclusive PSEC policy forms
		// (Rust `build_psec_v1_network_policy`).
		network.ProxyURL = p.ProxyURL
		return network
	}
	if !p.NetworkSpecified {
		return network
	}
	egress := &SecurityEnvironmentEgress{Default: psecFilterDeny}
	if p.NetworkEgress.Default == NetworkActionAllow {
		egress.Default = psecFilterAllow
	}
	for _, rule := range p.NetworkEgress.Allow {
		egress.Allow = append(egress.Allow, psecEndpointRule(rule))
	}
	network.Egress = egress
	return network
}

func psecEndpointRule(rule NetworkRule) SecurityEnvironmentEndpointRule {
	endpoint := SecurityEnvironmentEndpointRule{}
	for _, peer := range rule.To {
		destination := SecurityEnvironmentDestination{
			Address:   peer.CIDR.Address,
			PrefixLen: peer.CIDR.PrefixLen,
		}
		for _, exception := range peer.Except {
			destination.Except = append(destination.Except, SecurityEnvironmentSubnet{
				Address:   exception.Address,
				PrefixLen: exception.PrefixLen,
			})
		}
		endpoint.Destinations = append(endpoint.Destinations, destination)
	}
	for _, port := range rule.Ports {
		endpoint.Ports = append(endpoint.Ports, SecurityEnvironmentPort{Protocol: psecProtocolAny, Start: port})
	}
	return endpoint
}

// effectiveCapabilities mirrors mxc-sdk `effective_capabilities` +
// `add_default_network_capabilities`: policy-owned network capabilities are
// dropped when the request carries a network policy, then re-derived from the
// egress/ingress defaults.
func effectiveCapabilities(policy NativeContainerPolicy) []string {
	capabilities := make([]string, 0, len(policy.Capabilities)+2)
	for _, capability := range policy.Capabilities {
		if strings.TrimSpace(capability) != "" {
			capabilities = append(capabilities, capability)
		}
	}
	if policy.NetworkSpecified || policy.NetworkModeSpecified {
		filtered := capabilities[:0]
		for _, capability := range capabilities {
			if !capabilityIn(capability, psecPolicyOwnedCapabilities) {
				filtered = append(filtered, capability)
			}
		}
		capabilities = filtered
	}
	allowsEgress := policy.NetworkEgress.Default == NetworkActionAllow || len(policy.NetworkEgress.Allow) > 0
	if allowsEgress {
		capabilities = ensureCapability(capabilities, psecCapabilityInternetClient)
	}
	return capabilities
}

func capabilityIn(candidate string, set []string) bool {
	for _, entry := range set {
		if strings.EqualFold(candidate, entry) {
			return true
		}
	}
	return false
}

func ensureCapability(capabilities []string, capability string) []string {
	if capabilityIn(capability, capabilities) {
		return capabilities
	}
	return append(capabilities, capability)
}

// Job-object UI limit bits (`winnt.h` JOB_OBJECT_UILIMIT_*), including the two
// values the `windows` crate does not export.
const (
	jobUIlimitHandles           uint64 = 0x0000_0001
	jobUIlimitReadClipboard     uint64 = 0x0000_0002
	jobUIlimitWriteClipboard    uint64 = 0x0000_0004
	jobUIlimitSystemParameters  uint64 = 0x0000_0008
	jobUIlimitDisplaySettings   uint64 = 0x0000_0010
	jobUIlimitGlobalAtoms       uint64 = 0x0000_0020
	jobUIlimitDesktop           uint64 = 0x0000_0040
	jobUIlimitExitWindows       uint64 = 0x0000_0080
	jobUIlimitIME               uint64 = 0x0000_0100
	jobUIlimitInjection         uint64 = 0x0000_0200
)

// EffectiveUIRestrictionMask ports mxc-sdk `resolve_ui_restrictions` +
// `to_job_object_uilimit_mask` for the fields this helper models. The fields it
// does not model keep the SDK defaults: clipboard "none", injection disabled,
// desktopSystemControl disabled, systemSettings "none", ime disabled. Only the
// isolation level is caller-controlled because PowerShell needs desktop
// handles during startup (Rust #44903).
func EffectiveUIRestrictionMask(ui UIPolicy, base BaseProcessUI) uint64 {
	var mask uint64
	if ui.Disable {
		return jobUIlimitHandles | jobUIlimitReadClipboard | jobUIlimitWriteClipboard |
			jobUIlimitSystemParameters | jobUIlimitDisplaySettings | jobUIlimitGlobalAtoms |
			jobUIlimitDesktop | jobUIlimitExitWindows | jobUIlimitIME | jobUIlimitInjection
	}
	// Clipboard defaults to "none": block both directions.
	mask |= jobUIlimitReadClipboard | jobUIlimitWriteClipboard
	// Injection defaults to disabled.
	mask |= jobUIlimitInjection
	// Isolation "desktop" means no UI-object isolation restrictions; anything
	// else falls back to MXC's "container" default (external objects + atoms).
	if isolation := strings.TrimSpace(base.Isolation); !strings.EqualFold(isolation, "desktop") {
		mask |= jobUIlimitHandles | jobUIlimitGlobalAtoms
	}
	// desktopSystemControl defaults to disabled: no desktop switching, no logoff.
	mask |= jobUIlimitDesktop | jobUIlimitExitWindows
	// systemSettings defaults to "none": block parameters and display changes.
	mask |= jobUIlimitSystemParameters | jobUIlimitDisplaySettings
	// IME defaults to disabled.
	mask |= jobUIlimitIME
	return mask
}

// ValidateNetworkCIDR reports which network peers are not parseable CIDRs; the
// native API requires canonical address strings.
func ValidateNetworkCIDR(peers []NetworkPeer) error {
	for _, peer := range peers {
		if _, _, err := net.ParseCIDR(fmt.Sprintf("%s/%d", peer.CIDR.Address, peer.CIDR.PrefixLen)); err != nil {
			return fmt.Errorf("invalid MXC network peer %s/%d: %w", peer.CIDR.Address, peer.CIDR.PrefixLen, err)
		}
	}
	return nil
}

// sortUniquePaths keeps the emitted paths deterministic for golden tests.
func sortUniquePaths(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}
