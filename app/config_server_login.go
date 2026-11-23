package app

// Config-server credential lifecycle for the CLI login and logout flows
// (Rust cloud-config/src/bundle_loader.rs ensure_config_server_login /
// managed_config_auth_manager, #57175).

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"

	"codex_go/auth"
	"codex_go/config"
	"codex_go/keyring"
	codexnetwork "codex_go/network"
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

// ConfigServerLoginFailureMessage mirrors Rust CONFIG_SERVER_LOGIN_FAILURE: a
// config-server sign-in failure points at its own configuration instead of the
// generic gateway guidance.
const ConfigServerLoginFailureMessage = "Config server sign-in failed. Check config_server.oauth and the credential store."

// ConfigServerLoginMethod selects how a missing config-server grant is obtained.
// The CLI login flow shares one method with the ChatGPT sign-in it precedes, so
// `--device-auth` cannot silently fall back to a browser authorization (#62079).
type ConfigServerLoginMethod struct {
	// OnURL receives the browser authorization URL. Device-code sign-ins ignore it.
	OnURL func(string)
	// DeviceCode requests device authorization instead of browser sign-in.
	DeviceCode bool
	// Present receives the device verification URL and code.
	Present func(auth.GatewayDeviceCodePrompt)
}

// configServerDeviceCodeMessage renders the prompt a config-server device sign-in
// shows (Rust config_server_device_code_message).
func configServerDeviceCodeMessage(prompt auth.GatewayDeviceCodePrompt) string {
	return fmt.Sprintf("To sign in to the config server, open %s and enter code %s.", prompt.VerificationURL, prompt.UserCode)
}

// ensureConfigServerLogin reuses a stored config-server grant and otherwise
// signs in with the selected method, leaving refresh handling to the bundle
// loader (Rust ensure_config_server_login).
func ensureConfigServerLogin(ctx context.Context, codexHome string, requirements *config.ConfigRequirements, method ConfigServerLoginMethod) error {
	return ensureConfigServerLoginWith(ctx, codexHome, requirements, nil, keyring.New(), method)
}

// ensureConfigServerLoginWith is the dependency-injected form used by tests.
func ensureConfigServerLoginWith(ctx context.Context, codexHome string, requirements *config.ConfigRequirements, client *http.Client, store keyring.Store, method ConfigServerLoginMethod) error {
	if requirements == nil {
		return nil
	}
	manager := newConfigServerAuthManager(codexHome, requirements.ConfigServer, client, store)
	if manager == nil {
		return nil
	}
	// The grant check and the reservation happen together, so a saved grant is
	// reused and a concurrent sign-in for the same credential is refused rather
	// than raced (Rust #62079 start_login_if_required).
	attempt, err := manager.LoginIfRequired()
	if err != nil {
		return err
	}
	if attempt == nil {
		return nil
	}
	defer func() { _ = attempt.Close() }()
	if !method.DeviceCode {
		return attempt.LoginWithBrowser(ctx, method.OnURL, ConfigServerLoginFailureMessage)
	}
	if !manager.SupportsDeviceAuthorization() {
		return errors.New("Device sign-in requires config_server.oauth.device_authorization_url")
	}
	return attempt.LoginWithDeviceCode(ctx, method.Present, ConfigServerLoginFailureMessage)
}

// logoutConfigServer removes the config server's stored grant and reports
// whether one existed (Rust logout_config_server). Discovery during logout
// honors the local application network policy (Rust #62824).
func logoutConfigServer(ctx context.Context, codexHome string, loaded *config.Config) (bool, error) {
	if loaded == nil || loaded.Requirements == nil {
		return false, nil
	}
	policy := publishApplicationNetworkPolicy(loaded)
	return logoutConfigServerWithPolicy(ctx, codexHome, loaded.Requirements, configServerHTTPClient(loaded, nil), keyring.New(), &policy)
}

// logoutConfigServerWith is the dependency-injected form used by tests.
func logoutConfigServerWith(ctx context.Context, codexHome string, requirements *config.ConfigRequirements, client *http.Client, store keyring.Store) (bool, error) {
	return logoutConfigServerWithPolicy(ctx, codexHome, requirements, client, store, nil)
}

func logoutConfigServerWithPolicy(ctx context.Context, codexHome string, requirements *config.ConfigRequirements, client *http.Client, store keyring.Store, policy *codexnetwork.NetworkPolicy) (bool, error) {
	if requirements == nil || requirements.ConfigServer == nil {
		return false, nil
	}
	resolved, err := resolveConfigServerOAuth(ctx, requirements.ConfigServer, client, policy)
	if err != nil {
		return false, err
	}
	manager := newConfigServerAuthManager(codexHome, resolved, client, store)
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
