//go:build windows

package windowsandboxservice

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"golang.org/x/sys/windows"

	"codex_go/sandbox/windowssandbox"
	"codex_go/sandbox/windowssandbox/provisionservice"
)

const packagedSetupHelperName = "codex-windows-sandbox-setup.exe"

var procImpersonateLoggedOnUser = advapi32.NewProc("ImpersonateLoggedOnUser")

// OwnerRegistrar records the authenticated uninstall owner and owns the
// duplicated token it receives (Rust on_authenticated_user).
type OwnerRegistrar func(record *windowssandbox.InstallationRecord, token windows.Token, setupRuntime windowssandbox.SetupRuntime) (*windowssandbox.InstallationRecord, error)

// SetupRunner provisions the sandbox through the packaged setup helper.
type SetupRunner func(identity *ClientIdentity, request *ProvisioningRequest) error

// RunProvisioning provisions an authenticated client's sandbox through its
// selected setup path (Rust provisioning::run). A registration-only request
// records the uninstall owner and stops; a legacy request records the owner,
// reuses a matching setup and otherwise launches the packaged helper.
func RunProvisioning(
	identity *ClientIdentity,
	request *ServiceRequest,
	sandboxSID []byte,
	stopped func() bool,
	registerOwner OwnerRegistrar,
	setup SetupRunner,
) (provisionservice.Response, error) {
	if identity == nil {
		return provisionservice.Response{}, errors.New("missing authenticated provisioning client")
	}
	if request == nil {
		return provisionservice.Response{}, errors.New("missing provisioning request")
	}
	if request.Register != nil {
		// Registration does not provision resources; it only records ownership.
		if err := registerAuthenticatedOwner(identity, registerOwner); err != nil {
			return provisionservice.Response{}, err
		}
		return provisionservice.Response{Kind: provisionservice.ResponseOK}, nil
	}
	provision := request.Provision
	if provision == nil {
		return provisionservice.Response{}, errors.New("missing provisioning request")
	}
	if identity.Runtime == windowssandbox.SetupRuntimeRegistered {
		return provisionservice.Response{}, errors.New("registered Core provisioning is not available in this build")
	}
	if err := registerAuthenticatedOwner(identity, registerOwner); err != nil {
		return provisionservice.Response{}, err
	}
	if stopped != nil && stopped() {
		return provisionservice.Response{}, errors.New("service is stopping")
	}
	settings := windowssandbox.OfflineProxySettings{
		ProxyPorts:        append([]uint16(nil), provision.Settings.ProxyPorts...),
		AllowLocalBinding: provision.Settings.AllowLocalBinding,
	}
	complete, err := setupIsCompleteAsOwner(identity, settings)
	if err != nil {
		return provisionservice.Response{}, err
	}
	if complete {
		return provisionservice.Response{Kind: provisionservice.ResponseOK}, nil
	}
	helper, err := packagedSetupHelper()
	if err != nil {
		return provisionservice.Response{}, err
	}
	if setup == nil {
		return provisionservice.Response{}, unavailable("the packaged setup helper " + helper + " is not wired")
	}
	if err := setup(identity, provision); err != nil {
		LogError(EventProvisioningFailed, "Codex sandbox provisioning failed: "+ErrorMessage(err))
		return provisionservice.Response{}, fmt.Errorf("sandbox provisioning failed: %w", err)
	}
	LogInformation(EventProvisioningSucceeded, "Codex sandbox provisioning completed successfully.")
	return provisionservice.Response{Kind: provisionservice.ResponseOK}, nil
}

func registerAuthenticatedOwner(identity *ClientIdentity, registerOwner OwnerRegistrar) error {
	if registerOwner == nil {
		return errors.New("the sandbox lifecycle has no owner registrar")
	}
	token, err := cloneToken(identity.Token)
	if err != nil {
		return fmt.Errorf("retain authenticated uninstall owner: %w", err)
	}
	record := &windowssandbox.InstallationRecord{
		UserSID:             identity.UserSID,
		CodexHome:           identity.CodexHome,
		SessionID:           identity.SessionID,
		DesktopInstallation: identity.DesktopInstallation,
	}
	if _, err := registerOwner(record, token, identity.Runtime); err != nil {
		_ = token.Close()
		return fmt.Errorf("retain authenticated uninstall owner: %w", err)
	}
	return nil
}

func setupIsCompleteAsOwner(identity *ClientIdentity, settings windowssandbox.OfflineProxySettings) (bool, error) {
	complete := false
	err := withOwnerImpersonation(identity.Token, func() error {
		complete = windowssandbox.SandboxSetupIsCompleteWithSettings(identity.CodexHome, settings)
		return nil
	})
	return complete, err
}

func cloneToken(token windows.Token) (windows.Token, error) {
	process := windows.CurrentProcess()
	var duplicate windows.Handle
	if err := windows.DuplicateHandle(process, windows.Handle(token), process, &duplicate, 0, false, windows.DUPLICATE_SAME_ACCESS); err != nil {
		return 0, err
	}
	return windows.Token(duplicate), nil
}

// withOwnerImpersonation runs operation as the authenticated owner. A failed
// revert must not continue as the user, so the process exits instead
// (Rust package_lifecycle::with_owner_impersonation).
func withOwnerImpersonation(token windows.Token, operation func() error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	impersonated, _, callErr := procImpersonateLoggedOnUser.Call(uintptr(token))
	if impersonated == 0 {
		return fmt.Errorf("impersonate the sandbox owner: %w", callErr)
	}
	defer func() {
		if err := windows.RevertToSelf(); err != nil {
			LogError(EventServiceFailed, "unable to revert sandbox-owner impersonation: "+err.Error())
			os.Exit(1)
		}
	}()
	return operation()
}

// packagedSetupHelper resolves the setup helper next to the service binary and
// refuses a missing, non-regular or reparse-point entry.
func packagedSetupHelper() (string, error) {
	executable, err := executablePath()
	if err != nil {
		return "", fmt.Errorf("locate the provisioning service executable: %w", err)
	}
	helper := filepath.Join(filepath.Dir(executable), packagedSetupHelperName)
	info, err := os.Lstat(helper)
	if err != nil {
		return "", fmt.Errorf("inspect packaged setup helper %s: %w", helper, err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("refusing invalid packaged setup helper %s", helper)
	}
	return helper, nil
}
