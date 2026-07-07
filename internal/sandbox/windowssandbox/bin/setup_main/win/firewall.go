package win

import (
	"sort"
	"strconv"
	"strings"

	"codex_go/internal/sandbox/windowssandbox"
)

const (
	offlineBlockRuleName            = "codex_sandbox_offline_block_outbound"
	offlineBlockLoopbackTCPRuleName = "codex_sandbox_offline_block_loopback_tcp"
	offlineBlockLoopbackUDPRuleName = "codex_sandbox_offline_block_loopback_udp"
	offlineProxyAllowRuleName       = "codex_sandbox_offline_allow_loopback_proxy"

	offlineBlockRuleFriendly            = "Codex Sandbox Offline - Block Non-Loopback Outbound"
	offlineBlockLoopbackTCPRuleFriendly = "Codex Sandbox Offline - Block Loopback TCP (Except Proxy)"
	offlineBlockLoopbackUDPRuleFriendly = "Codex Sandbox Offline - Block Loopback UDP"

	loopbackRemoteAddresses    = "127.0.0.0/8,::/127"
	nonLoopbackRemoteAddresses = "0.0.0.0-126.255.255.255,128.0.0.0-255.255.255.255,::,::2-ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff"

	netFwIPProtocolTCP = 6
	netFwIPProtocolUDP = 17
	netFwIPProtocolAny = 256
)

type firewallBlockRuleSpec struct {
	internalName       string
	friendlyDesc       string
	protocol           int
	localUserSpec      string
	offlineSID         string
	remoteAddresses    string
	remotePorts        string
	remotePortsIsSet   bool
	remoteAddressIsSet bool
}

func ConfigureFirewall() error {
	return windowssandbox.ErrInvalidRequest
}

func firewallLocalUserSpec(offlineSID string) string {
	return "O:LSD:(A;;CC;;;" + strings.TrimSpace(offlineSID) + ")"
}

func BlockedLoopbackTCPRemotePorts(proxyPorts []uint16) (string, bool) {
	return blockedLoopbackTCPRemotePorts(proxyPorts)
}

func blockedLoopbackTCPRemotePorts(proxyPorts []uint16) (string, bool) {
	allowedPorts := make([]uint16, 0, len(proxyPorts))
	for _, port := range proxyPorts {
		if port != 0 {
			allowedPorts = append(allowedPorts, port)
		}
	}
	sort.Slice(allowedPorts, func(i, j int) bool { return allowedPorts[i] < allowedPorts[j] })
	allowedPorts = dedupUint16s(allowedPorts)

	blockedRanges := make([]string, 0, len(allowedPorts)+1)
	start := uint32(1)
	for _, value := range allowedPorts {
		port := uint32(value)
		if port < start {
			continue
		}
		if port > start {
			blockedRanges = append(blockedRanges, portRangeString(start, port-1))
		}
		start = port + 1
	}
	if start <= uint32(^uint16(0)) {
		blockedRanges = append(blockedRanges, portRangeString(start, uint32(^uint16(0))))
	}
	if len(blockedRanges) == 0 {
		return "", false
	}
	return strings.Join(blockedRanges, ","), true
}

func portRangeString(start uint32, end uint32) string {
	if start == end {
		return strconv.FormatUint(uint64(start), 10)
	}
	return strconv.FormatUint(uint64(start), 10) + "-" + strconv.FormatUint(uint64(end), 10)
}

func dedupUint16s(values []uint16) []uint16 {
	if len(values) < 2 {
		return values
	}
	out := values[:1]
	for _, value := range values[1:] {
		if value != out[len(out)-1] {
			out = append(out, value)
		}
	}
	return out
}
