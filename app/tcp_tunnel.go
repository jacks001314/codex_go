package app

// The hidden tcp-tunnel command has two modes (Rust #60568): a listener that
// reads its bearer from stdin, and an OpenSSH ProxyCommand relay that uses the
// ChatGPT OAuth login. The OAuth mode enforces the application network policy
// before the gateway connection and refuses any credential other than a
// ChatGPT login.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"

	"codex_go/auth"
	"codex_go/cli"
	"codex_go/config"
	"codex_go/tcptunnel"
)

// tcpTunnelSSHOAuthRequired is Rust's message for every non-OAuth credential.
const tcpTunnelSSHOAuthRequired = "ChatGPT OAuth login required for SSH"

func runTcpTunnel(ctx context.Context, parsed *cli.Parsed, stdin io.Reader, stdout, stderr io.Writer) error {
	if parsed == nil {
		return errors.New("missing tcp-tunnel arguments")
	}
	args := &parsed.TcpTunnel.Args
	if args.AuthTokenStdin {
		return tcptunnel.Run(ctx, args, stdin, stdout, stderr)
	}
	return runTcpTunnelSSH(ctx, args, stdin, stdout)
}

// runTcpTunnelSSH relays one SSH session through the gateways the CLI trusts.
func runTcpTunnelSSH(ctx context.Context, args *tcptunnel.Args, stdin io.Reader, stdout io.Writer) error {
	proxyURL, err := url.Parse(args.ProxyURL)
	if err != nil {
		return fmt.Errorf("invalid proxy URL: %w", err)
	}
	codexHome := auth.DefaultCodexHome()
	bootstrap, err := config.Load(codexHome)
	if err != nil {
		return err
	}
	var credential *auth.AuthDotJSON
	if resolved, resolveErr := auth.NewStore(codexHome).Resolve(); resolveErr == nil && resolved != nil {
		snapshot := resolved.Auth
		credential = &snapshot
	}
	effective := bootstrap
	// Rust loads the Cloud config before acquiring the policy, so a managed
	// restriction can refuse the gateway before any handshake.
	if credential != nil {
		if loader, loadErr := cloudConfigBundleLoaderForRun(ctx, codexHome, bootstrap, credential); loadErr == nil && loader != nil {
			if loaded, loadErr := config.LoadWithOptions(codexHome, &config.LoadOptions{CloudConfigBundle: loader}); loadErr == nil {
				effective = loaded
			}
		}
	}
	publishApplicationNetworkPolicy(effective)
	permit, err := effective.NetworkPolicy().Acquire(proxyURL)
	if err != nil {
		return err
	}
	defer permit.Release()

	if credential == nil || credential.Mode() != "chatgpt" {
		return errors.New(tcpTunnelSSHOAuthRequired)
	}
	token := auth.ChatGPTAccessTokenFromAuth(credential)
	if token == "" {
		return errors.New(tcpTunnelSSHOAuthRequired)
	}
	return tcptunnel.RunSSHProxy(ctx, args, func(context.Context) (string, error) {
		if err := permit.Check(); err != nil {
			return "", err
		}
		return token, nil
	}, stdin, stdout)
}
