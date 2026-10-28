package app

// Customer-hosted managed configuration bundle source (Rust
// cloud-config/src/config_server.rs, #56295/#56314). Cache entries are scoped
// to the endpoint and the current OAuth sign-in, and each bundle fetch recovers
// rejected credentials at most once.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"codex_go/auth"
	"codex_go/config"
	"codex_go/keyring"
	codexnetwork "codex_go/network"
)

const (
	configServerSignInMessage      = "Sign in to the configuration server to load your organization's settings"
	configServerAuthFailureMessage = "Could not authenticate with the configuration server"
	configServerFetchTimeout       = 15 * time.Second
)

// configServerCloudConfigBundleLoaderForRun builds the managed-configuration
// loader for a customer-hosted configuration server. It does not require a
// ChatGPT sign-in; OAuth credentials are optional and are never requested
// interactively while loading a bundle.
func configServerCloudConfigBundleLoaderForRun(ctx context.Context, codexHome string, bootstrap *config.Config, server *config.ConfigServer) (*config.CloudConfigLoader, error) {
	return configServerCloudConfigBundleLoaderForRunWithDeps(ctx, codexHome, bootstrap, server, keyring.New(), nil)
}

func configServerCloudConfigBundleLoaderForRunWithStore(ctx context.Context, codexHome string, bootstrap *config.Config, server *config.ConfigServer, store keyring.Store) (*config.CloudConfigLoader, error) {
	return configServerCloudConfigBundleLoaderForRunWithDeps(ctx, codexHome, bootstrap, server, store, nil)
}

func configServerCloudConfigBundleLoaderForRunWithDeps(ctx context.Context, codexHome string, bootstrap *config.Config, server *config.ConfigServer, store keyring.Store, client *http.Client) (*config.CloudConfigLoader, error) {
	if bootstrap == nil || server == nil {
		return nil, nil
	}
	endpoint, err := server.BundleURL()
	if err != nil {
		return nil, err
	}
	var manager *auth.GatewayAuthManager
	if server.OAuth != nil {
		manager = auth.NewOAuthSessionManager(configServerOAuthSessionConfig(server.OAuth), codexHome, client, store)
	}
	namespace := configServerCacheNamespace(endpoint, manager)
	loader := config.NewCloudConfigLoader(func() (*config.CloudConfigBundle, error) {
		loadCtx, cancel := context.WithTimeout(ctx, configServerFetchTimeout)
		defer cancel()
		identity := "anonymous"
		if manager != nil {
			authorizationID, err := manager.AuthorizationID(loadCtx)
			if err != nil {
				return nil, configServerAuthenticationError(err)
			}
			identity = authorizationID
		}
		policy := publishApplicationNetworkPolicy(bootstrap)
		sentToken := ""
		buildOptions := func(token string) config.CloudConfigFetchOptions {
			options := config.CloudConfigFetchOptions{
				CodexHome:      codexHome,
				EndpointURL:    endpoint,
				CacheNamespace: namespace,
				ChatGPTUserID:  identity,
				AccountID:      namespace,
				HTTPClient:     withoutRedirects(configServerHTTPClient(bootstrap, client)),
				NetworkPolicy:  &policy,
			}
			if manager != nil {
				options.Authorize = func(requestCtx context.Context, request *http.Request) error {
					accessToken := token
					if accessToken == "" {
						resolved, err := manager.ResolveAccessTokenWithoutLogin(requestCtx)
						if err != nil {
							return configServerAuthenticationMessage(err)
						}
						accessToken = resolved
					}
					sentToken = accessToken
					request.Header.Set("Authorization", "Bearer "+accessToken)
					return nil
				}
			}
			return options
		}
		bundle, err := config.LoadCloudConfigBundle(loadCtx, buildOptions(""))
		if err == nil {
			return bundle, nil
		}
		if manager == nil || sentToken == "" || !configServerUnauthorized(err) {
			return nil, err
		}
		// Rust ConfigServerSource::recover_auth: one 401 recovery per fetch.
		replacement, refreshErr := manager.RefreshAccessToken(loadCtx, sentToken)
		if refreshErr != nil {
			return nil, configServerAuthenticationError(refreshErr)
		}
		retried, retryErr := config.LoadCloudConfigBundle(loadCtx, buildOptions(replacement))
		if retryErr != nil && configServerUnauthorized(retryErr) {
			return nil, config.NewCloudConfigLoadError(config.CloudConfigLoadAuth, nil, configServerAuthFailureMessage)
		}
		return retried, retryErr
	})
	return loader.WithConfigServer(server), nil
}

func configServerHTTPClient(bootstrap *config.Config, client *http.Client) *http.Client {
	if client != nil {
		return client
	}
	return codexnetwork.NewHTTPClient(bootstrap.RespectSystemProxyEnabled(), 0)
}

func configServerOAuthSessionConfig(oauth *config.ConfigServerOAuth) auth.GatewayAuthConfig {
	session := auth.GatewayAuthConfig{
		AuthorizationURL: oauth.AuthorizationURL,
		TokenURL:         oauth.TokenURL,
		ClientID:         oauth.ClientID,
		RedirectPort:     oauth.RedirectPort,
		Scopes:           append([]string(nil), oauth.Scopes...),
	}
	if oauth.Resource != nil {
		session.Resource = *oauth.Resource
	}
	return session
}

// configServerCacheNamespace mirrors Rust's
// sha256(json((bundle_url, credential_id))) cache namespace.
func configServerCacheNamespace(endpoint string, manager *auth.GatewayAuthManager) string {
	var credentialID any
	if manager != nil {
		credentialID = manager.CredentialID()
	}
	payload, _ := json.Marshal([]any{endpoint, credentialID})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func configServerUnauthorized(err error) bool {
	var loadErr *config.CloudConfigLoadError
	return errors.As(err, &loadErr) && loadErr.StatusCode != nil && *loadErr.StatusCode == http.StatusUnauthorized
}

func configServerAuthenticationError(err error) error {
	return config.NewCloudConfigLoadError(config.CloudConfigLoadAuth, nil, configServerAuthenticationMessage(err).Error())
}

func configServerAuthenticationMessage(err error) error {
	if errors.Is(err, auth.ErrGatewayLoginRequired) {
		return errors.New(configServerSignInMessage)
	}
	return errors.New(configServerAuthFailureMessage)
}

// withoutRedirects fetches the bundle exactly once: a config server must not
// redirect the bootstrap request (Rust for_config_endpoint).
func withoutRedirects(client *http.Client) *http.Client {
	noRedirect := func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if client == nil {
		return &http.Client{CheckRedirect: noRedirect}
	}
	clone := *client
	clone.CheckRedirect = noRedirect
	return &clone
}
