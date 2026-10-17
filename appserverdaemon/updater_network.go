package appserverdaemon

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"codex_go/config"
	"codex_go/network"
)

// updaterHTTPClient loads the user and managed configuration, publishes its
// application network policy, and binds that policy to the updater transport.
// Configuration failures are fatal: falling back to an unrestricted client
// would let a restricted installation bypass its managed destinations.
func updaterHTTPClient(codexHome string) (*http.Client, error) {
	_, client, err := updaterNetworkPolicyAndClient(codexHome)
	return client, err
}

func updaterNetworkPolicyAndClient(codexHome string) (network.NetworkPolicy, *http.Client, error) {
	cfg, err := config.LoadWithOptions(strings.TrimSpace(codexHome), &config.LoadOptions{IncludeManagedConfig: true})
	if err != nil {
		return network.UnmanagedNetworkPolicy(), nil, fmt.Errorf("failed to load updater network configuration: %w", err)
	}
	cfg.BindApplicationNetworkPolicy()
	base := network.NewHTTPClient(cfg.RespectSystemProxyEnabled(), 30*time.Second)
	return cfg.NetworkPolicy(), network.PolicyHTTPClient(cfg.NetworkPolicy(), base), nil
}

func updaterInstallerFetch(codexHome string) (func(context.Context) ([]byte, error), error) {
	client, err := updaterHTTPClient(codexHome)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context) ([]byte, error) {
		return fetchInstallerScriptWithClient(ctx, client)
	}, nil
}

// ConfigureUpdaterNetworkPolicy binds the daemon updater's installer fetch to
// the selected Codex home's application network policy.
func ConfigureUpdaterNetworkPolicy(options *UpdateLoopOptions, codexHome string) error {
	if options == nil {
		return nil
	}
	fetch, err := updaterInstallerFetch(codexHome)
	if err != nil {
		return err
	}
	options.FetchScript = fetch
	return nil
}

// ConfigureMigrationNetworkPolicy applies the same policy to a legacy daemon
// migration that fetches and runs the published installer directly.
func ConfigureMigrationNetworkPolicy(options *MigrationOptions, codexHome string) error {
	if options == nil {
		return nil
	}
	fetch, err := updaterInstallerFetch(codexHome)
	if err != nil {
		return err
	}
	options.FetchScript = fetch
	return nil
}
