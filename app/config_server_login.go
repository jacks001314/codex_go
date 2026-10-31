package app

// Config-server credential lifecycle for the CLI login and logout flows
// (Rust cloud-config/src/bundle_loader.rs ensure_config_server_login /
// managed_config_auth_manager, #57175).

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"

	"codex_go/auth"
	"codex_go/config"
	"codex_go/keyring"
)

// newConfigServerAuthManager builds the OAuth manager that owns a config
// server's credentials, or nil when the server declares no OAuth endpoints
// (Rust managed_config_auth_manager returns None for an anonymous server).
func newConfigServerAuthManager(codexHome string, server *config.ConfigServer, client *http.Client, store keyring.Store) *auth.GatewayAuthManager {
	if server == nil || server.OAuth == nil {
		return nil
	}
	return auth.NewOAuthSessionManager(configServerOAuthSessionConfig(server.OAuth), codexHome, client, store)
}

// ensureConfigServerLogin reuses a stored config-server grant and otherwise
// signs in interactively, leaving refresh handling to the bundle loader
// (Rust ensure_config_server_login).
func ensureConfigServerLogin(ctx context.Context, codexHome string, requirements *config.ConfigRequirements, onURL func(string)) error {
	return ensureConfigServerLoginWith(ctx, codexHome, requirements, nil, keyring.New(), onURL)
}

// ensureConfigServerLoginWith is the dependency-injected form used by tests.
func ensureConfigServerLoginWith(ctx context.Context, codexHome string, requirements *config.ConfigRequirements, client *http.Client, store keyring.Store, onURL func(string)) error {
	if requirements == nil {
		return nil
	}
	manager := newConfigServerAuthManager(codexHome, requirements.ConfigServer, client, store)
	if manager == nil {
		return nil
	}
	if _, err := manager.AuthorizationID(ctx); err != nil {
		if !errors.Is(err, auth.ErrGatewayLoginRequired) {
			return err
		}
		if err := manager.LoginWithBrowser(ctx, onURL); err != nil {
			return err
		}
	}
	return nil
}

// logoutConfigServer removes the config server's stored grant and reports
// whether one existed (Rust logout_config_server).
func logoutConfigServer(ctx context.Context, codexHome string, requirements *config.ConfigRequirements) (bool, error) {
	return logoutConfigServerWith(ctx, codexHome, requirements, nil, keyring.New())
}

// logoutConfigServerWith is the dependency-injected form used by tests.
func logoutConfigServerWith(ctx context.Context, codexHome string, requirements *config.ConfigRequirements, client *http.Client, store keyring.Store) (bool, error) {
	if requirements == nil {
		return false, nil
	}
	manager := newConfigServerAuthManager(codexHome, requirements.ConfigServer, client, store)
	if manager == nil {
		return false, nil
	}
	return manager.Logout(ctx)
}

// loadManagedLoginConfig reloads the effective configuration with the
// config-server bundle applied (Rust load_managed_login_config_or_exit): a
// configuration that requires a remote bundle keeps failing startup checks
// until the bundle from the required server is applied.
func loadManagedLoginConfig(ctx context.Context, codexHome string, overrides []string) (*config.Config, error) {
	opts := &config.EffectiveOptions{RawOverrides: append([]string(nil), overrides...)}
	bootstrap, err := config.LoadEffectiveWithOptions(codexHome, opts)
	if err != nil {
		return nil, err
	}
	if !bootstrap.RequiresRemoteConfig() {
		return bootstrap, nil
	}
	loader, err := configServerCloudConfigBundleLoaderForRun(ctx, codexHome, bootstrap, bootstrap.Requirements.ConfigServer)
	if err != nil {
		return nil, err
	}
	opts.IncludeManagedConfig = true
	opts.ManagedConfigPath = filepath.Join(codexHome, "managed_config.toml")
	opts.CloudConfigBundle = loader
	return loadEffectiveWithAppliedConfigServer(codexHome, opts, loader)
}
