package cli

import (
	"errors"

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
		// The tunnel parser raises clap-shaped diagnostics for its own
		// arguments; they must reach the CLI as clap blocks with exit status 2.
		var diagnostic *tcptunnel.Diagnostic
		if errors.As(err, &diagnostic) {
			return &UsageError{Message: diagnostic.Message, Usage: diagnostic.Usage}
		}
		return err
	}
	options.Args = *parsed
	return nil
}
