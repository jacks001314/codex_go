//go:build !windows

package win

import (
	"io"

	"codex_go/internal/sandbox/windowssandbox"
)

func EnsureOfflineProxyAllowlist(offlineSID string, proxyPorts []uint16, allowLocalBinding bool, log io.Writer) error {
	return windowssandbox.Unsupported("bin.setup_main.win.firewall.ensure_offline_proxy_allowlist")
}

func EnsureOfflineOutboundBlock(offlineSID string, log io.Writer) error {
	return windowssandbox.Unsupported("bin.setup_main.win.firewall.ensure_offline_outbound_block")
}
