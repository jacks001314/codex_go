package win

import (
	"errors"
	"testing"

	"codex_go/internal/sandbox/windowssandbox"
)

func TestBlockedLoopbackTCPRemotePorts(t *testing.T) {
	tests := []struct {
		name  string
		ports []uint16
		want  string
		ok    bool
	}{
		{name: "none", want: "1-65535", ok: true},
		{name: "single", ports: []uint16{8080}, want: "1-8079,8081-65535", ok: true},
		{name: "dedup sorted zero ignored", ports: []uint16{443, 0, 80, 80}, want: "1-79,81-442,444-65535", ok: true},
		{name: "edges", ports: []uint16{1, 65535}, want: "2-65534", ok: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := BlockedLoopbackTCPRemotePorts(tt.ports)
			if got != tt.want || ok != tt.ok {
				t.Fatalf("BlockedLoopbackTCPRemotePorts(%v) = %q, %v; want %q, %v", tt.ports, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestFirewallConstantsMatchRustRules(t *testing.T) {
	if offlineBlockRuleName != "codex_sandbox_offline_block_outbound" {
		t.Fatalf("offlineBlockRuleName = %q", offlineBlockRuleName)
	}
	if offlineBlockLoopbackTCPRuleName != "codex_sandbox_offline_block_loopback_tcp" {
		t.Fatalf("offlineBlockLoopbackTCPRuleName = %q", offlineBlockLoopbackTCPRuleName)
	}
	if offlineBlockLoopbackUDPRuleName != "codex_sandbox_offline_block_loopback_udp" {
		t.Fatalf("offlineBlockLoopbackUDPRuleName = %q", offlineBlockLoopbackUDPRuleName)
	}
	if loopbackRemoteAddresses != "127.0.0.0/8,::/127" {
		t.Fatalf("loopbackRemoteAddresses = %q", loopbackRemoteAddresses)
	}
	if netFwIPProtocolAny != 256 || netFwIPProtocolTCP != 6 || netFwIPProtocolUDP != 17 {
		t.Fatalf("protocol constants = any:%d tcp:%d udp:%d", netFwIPProtocolAny, netFwIPProtocolTCP, netFwIPProtocolUDP)
	}
}

func TestConfigureFirewallRequiresSIDSpecificEntryPoint(t *testing.T) {
	if err := ConfigureFirewall(); !errors.Is(err, windowssandbox.ErrInvalidRequest) {
		t.Fatalf("ConfigureFirewall() error = %v, want invalid request", err)
	}
}
