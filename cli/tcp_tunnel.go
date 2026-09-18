package cli

import (
	"codex_go/tcptunnel"
)

// TcpTunnelOptions holds the hidden `codex tcp-tunnel` arguments.
type TcpTunnelOptions struct {
	// Args is the parsed HTTP/3 CONNECT tunnel configuration.
	Args tcptunnel.Args
}

// parseTcpTunnel mirrors Rust's `TcpTunnel(codex_tcp_tunnel::Args)` clap
// subcommand (codex-rs/cli/src/main.rs).
func parseTcpTunnel(args []string, options *TcpTunnelOptions) error {
	parsed, err := tcptunnel.ParseArgs(args)
	if err != nil {
		return err
	}
	options.Args = *parsed
	return nil
}
