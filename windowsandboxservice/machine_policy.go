//go:build windows

package windowsandboxservice

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"codex_go/auth"
	"codex_go/config"
	"codex_go/model"
	codexnetwork "codex_go/network"
	"codex_go/sandbox/windowssandbox/provisionservice"
)

// RequirementsLoader loads the managed requirements that govern one Codex home.
// It runs while the service impersonates the provisioning client, so
// owner-scoped managed configuration is read under the right identity (Rust
// machine_policy::validate_provisioning_request).
type RequirementsLoader func(ctx context.Context, codexHome string) (*config.ConfigRequirements, error)

// ValidateProvisioningRequirements ports machine_policy::validate_requirements:
// managed policy must permit the elevated Windows sandbox and the requested
// proxy ports, local binding and listener identities. A nil requirement set is
// unmanaged and permits everything, while an entry that prohibits the elevated
// sandbox fails closed.
func ValidateProvisioningRequirements(settings provisionservice.Settings, listeners provisionservice.Listeners, requirements *config.ConfigRequirements) error {
	if requirements == nil {
		return nil
	}
	if allowed := requirements.AllowedWindowsSandboxImplementations; allowed != nil {
		elevated := false
		for _, implementation := range allowed {
			if implementation == config.WindowsSandboxImplementationElevated {
				elevated = true
				break
			}
		}
		if !elevated {
			return errors.New("managed policy does not permit the elevated Windows sandbox")
		}
	}
	network := requirements.Network
	if network == nil {
		return nil
	}
	if network.Enabled != nil && !*network.Enabled && (settings.AllowLocalBinding || len(settings.ProxyPorts) > 0) {
		return errors.New("managed policy disables sandbox network access")
	}
	if settings.AllowLocalBinding && network.AllowLocalBinding != nil && !*network.AllowLocalBinding {
		return errors.New("managed policy does not permit local network binding")
	}
	if network.HTTPPort != nil {
		for _, port := range listeners.HTTPPorts {
			if port != *network.HTTPPort {
				return fmt.Errorf("managed policy does not permit HTTP proxy port %d", port)
			}
		}
	}
	if network.SOCKSPort != nil {
		for _, port := range listeners.SOCKSPorts {
			if port != *network.SOCKSPort {
				return fmt.Errorf("managed policy does not permit SOCKS proxy port %d", port)
			}
		}
	}
	// Omitting listener identities must not bypass managed port restrictions.
	if network.HTTPPort != nil || network.SOCKSPort != nil {
		for _, port := range settings.ProxyPorts {
			classified := listenerContainsPort(listeners.HTTPPorts, port) ||
				listenerContainsPort(listeners.SOCKSPorts, port) ||
				(network.HTTPPort != nil && *network.HTTPPort == port) ||
				(network.SOCKSPort != nil && *network.SOCKSPort == port)
			if !classified {
				return fmt.Errorf("managed policy does not permit unclassified proxy port %d", port)
			}
		}
	}
	return nil
}

func listenerContainsPort(ports []uint16, port uint16) bool {
	for _, candidate := range ports {
		if candidate == port {
			return true
		}
	}
	return false
}

// serviceCloudConfigFetchTimeout bounds one managed-configuration bootstrap fetch.
const serviceCloudConfigFetchTimeout = 15 * time.Second

// LoadManagedRequirements ports machine_policy::validate_provisioning_request's
// configuration load: bootstrap the owner's configuration, resolve cloud
// configuration authentication, load the managed bundle and return the merged
// requirements. Without an eligible enterprise identity only the on-disk
// configuration contributes requirements.
func LoadManagedRequirements(ctx context.Context, codexHome string) (*config.ConfigRequirements, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	bootstrap, err := config.LoadEffectiveWithOptions(codexHome, serviceEffectiveOptions(nil))
	if err != nil {
		return nil, fmt.Errorf("load bootstrap configuration: %w", err)
	}
	bundle, err := serviceCloudConfigBundle(ctx, codexHome, bootstrap)
	if err != nil {
		return nil, err
	}
	if bundle == nil {
		return bootstrap.Requirements, nil
	}
	loaded, err := config.LoadEffectiveWithOptions(codexHome, serviceEffectiveOptions(bundle))
	if err != nil {
		return nil, fmt.Errorf("load managed configuration: %w", err)
	}
	return loaded.Requirements, nil
}

func serviceEffectiveOptions(bundle *config.CloudConfigLoader) *config.EffectiveOptions {
	options := &config.EffectiveOptions{
		IncludeManagedConfig: true,
	}
	if bundle != nil {
		options.CloudConfigBundle = bundle
	}
	return options
}

// serviceCloudConfigBundle builds the ChatGPT-backed bundle loader for the
// owner's configuration. Rust always resolves authentication and leaves the
// decision to the loader; a non-enterprise identity contributes no cloud
// requirements here.
func serviceCloudConfigBundle(ctx context.Context, codexHome string, bootstrap *config.Config) (*config.CloudConfigLoader, error) {
	if bootstrap == nil {
		return nil, nil
	}
	snapshot, err := auth.NewStoreWithOptions(codexHome, serviceAuthStoreOptions(bootstrap)).Load()
	if err != nil {
		return nil, fmt.Errorf("resolve cloud configuration authentication: %w", err)
	}
	if !serviceCloudConfigEligibleAuth(snapshot) {
		return nil, nil
	}
	headers, err := model.AuthHeadersFromAuth(*snapshot)
	if err != nil {
		return nil, fmt.Errorf("initialize cloud configuration authentication: %w", err)
	}
	policy := serviceApplicationNetworkPolicy(bootstrap)
	return config.NewCloudConfigLoader(func() (*config.CloudConfigBundle, error) {
		loadCtx, cancel := context.WithTimeout(ctx, serviceCloudConfigFetchTimeout)
		defer cancel()
		// Rust clears the configured base URL for this load, so the default
		// endpoint applies unless a managed requirement overrides it.
		return config.LoadCloudConfigBundle(loadCtx, config.CloudConfigFetchOptions{
			CodexHome:     codexHome,
			BaseURL:       config.DefaultChatGPTBaseURL,
			HTTPClient:    codexnetwork.NewHTTPClient(bootstrap.RespectSystemProxyEnabled(), 0),
			NetworkPolicy: &policy,
			Authorize: func(requestCtx context.Context, request *http.Request) error {
				return headers.Apply(requestCtx, request, nil)
			},
		})
	}), nil
}

func serviceAuthStoreOptions(loaded *config.Config) *auth.StoreOptions {
	options := auth.StoreOptionsFromConfig(loaded.CLIAuthCredentialsStoreMode(), loaded.SecretAuthStorageEnabled())
	options.WorkloadIdentity = &auth.WorkloadIdentityAuthOptions{
		ChatGPTBaseURL: loaded.ChatGPTBaseURL(),
	}
	return options
}

// serviceCloudConfigEligibleAuth mirrors the host's eligibility gate: only an
// enterprise ChatGPT identity contributes a cloud bundle.
func serviceCloudConfigEligibleAuth(snapshot *auth.AuthDotJSON) bool {
	if snapshot == nil {
		return false
	}
	switch snapshot.Mode() {
	case "chatgpt", "chatgptAuthTokens", "personal-access-token", "agent-identity":
	default:
		return false
	}
	account := auth.AccountFromAuth(snapshot)
	return account != nil && account.Type == auth.AccountChatGPT
}

// serviceApplicationNetworkPolicy binds the host network policy so the
// bootstrap fetch observes the same limits as the rest of the configuration.
func serviceApplicationNetworkPolicy(cfg *config.Config) codexnetwork.NetworkPolicy {
	if cfg == nil {
		return codexnetwork.UnmanagedNetworkPolicy()
	}
	if cfg.ApplicationNetworkPolicyOwner != nil {
		cfg.ApplicationNetworkPolicyOwner.Activate(cfg)
	} else {
		cfg.BindApplicationNetworkPolicy()
	}
	return cfg.NetworkPolicy()
}
