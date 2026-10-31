//go:build windows

package windowsandboxservice

import (
	"context"
	"errors"
	"fmt"

	"golang.org/x/sys/windows"

	"codex_go/config"
)

// MachinePolicyValidator is the PolicyValidator the provisioning broker uses:
// it loads managed requirements as the authenticated owner and enforces them
// (Rust machine_policy::validate_provisioning_request).
func MachinePolicyValidator(codexHome string, request *ProvisioningRequest, impersonationToken windows.Token) error {
	return ValidateProvisioningRequest(context.Background(), codexHome, request, impersonationToken, LoadManagedRequirements)
}

// ValidateProvisioningRequest ports machine_policy::validate_provisioning_request.
// A registered-Core refresh retains the existing sandbox and is admitted by the
// registered-runtime transaction, so it never starts the configuration runtime.
// Every other request loads managed policy as the owner and must satisfy it;
// failing to impersonate the client or to load the policy fails closed.
func ValidateProvisioningRequest(
	ctx context.Context,
	codexHome string,
	request *ProvisioningRequest,
	impersonationToken windows.Token,
	loader RequirementsLoader,
) error {
	if request == nil {
		return errors.New("missing provisioning request")
	}
	if request.RegisteredCore && request.RefreshOnly {
		return nil
	}
	if loader == nil {
		return errors.New("no managed-policy requirements loader is configured")
	}
	var requirements *config.ConfigRequirements
	err := withOwnerImpersonation(impersonationToken, func() error {
		loaded, err := loader(ctx, codexHome)
		if err != nil {
			return err
		}
		requirements = loaded
		return nil
	})
	if err != nil {
		if errors.Is(err, errOwnerImpersonation) {
			// Rust reports the worker failure as that message alone: the io cause is
			// printed by the worker, not carried into the request error, so a policy
			// event keeps cause=unavailable.
			return errors.New("configuration runtime worker failed to impersonate the provisioning client")
		}
		return err
	}
	if err := ValidateProvisioningRequirements(request.Settings, request.Listeners, requirements); err != nil {
		return fmt.Errorf("enforce managed provisioning requirements: %w", err)
	}
	return nil
}
