//go:build windows

package windowsandboxservice

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

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
		return RunRegisteredProvisioning(identity, provision, sandboxSID, stopped, registerOwner, setup)
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

// RunPackagedSetup launches the packaged setup helper for one authenticated
// legacy provisioning request, keeping the client directory pins open until the
// helper exits (Rust provisioning::run legacy arm).
func RunPackagedSetup(identity *ClientIdentity, request *ProvisioningRequest) error {
	if identity == nil || request == nil {
		return errors.New("missing authenticated provisioning request")
	}
	if _, err := packagedSetupHelper(); err != nil {
		return err
	}
	settings := windowssandbox.OfflineProxySettings{
		ProxyPorts:        append([]uint16(nil), request.Settings.ProxyPorts...),
		AllowLocalBinding: request.Settings.AllowLocalBinding,
	}
	return windowssandbox.RunElevatedProvisioningSetupWithRetainedHandles(
		identity.CodexHome,
		identity.Account,
		settings,
		identity.Runtime,
		identity.DirectoryHandles,
	)
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

const (
	// userFlagAccountDisable and userFlagPasswordExpired force full setup.
	userFlagAccountDisable  = 0x00000002
	userFlagPasswordExpired = 0x800000
	// sandboxGroupChanged is Rust SANDBOX_GROUP_CHANGED.
	sandboxGroupChanged = "sandbox group changed before authentication"
)

// runtimeProcessPackageFamily and runtimeProcessPackageFullName identify the
// running service package (Rust Package::Current()).
var (
	runtimeProcessPackageFamily   = windowssandbox.CurrentProcessPackageFamilyName
	runtimeProcessPackageFullName = windowssandbox.CurrentProcessPackageFullName
)

// registerAuthenticatedOwnerRecord records the authenticated owner and returns
// the persisted installation record for the registered transaction.
func registerAuthenticatedOwnerRecord(identity *ClientIdentity, registerOwner OwnerRegistrar) (*windowssandbox.InstallationRecord, error) {
	if registerOwner == nil {
		return nil, errors.New("the sandbox lifecycle has no owner registrar")
	}
	token, err := cloneToken(identity.Token)
	if err != nil {
		return nil, fmt.Errorf("retain authenticated uninstall owner: %w", err)
	}
	record := &windowssandbox.InstallationRecord{
		UserSID:             identity.UserSID,
		CodexHome:           identity.CodexHome,
		SessionID:           identity.SessionID,
		DesktopInstallation: identity.DesktopInstallation,
	}
	registered, err := registerOwner(record, token, identity.Runtime)
	if err != nil {
		_ = token.Close()
		return nil, fmt.Errorf("retain authenticated uninstall owner: %w", err)
	}
	if registered == nil {
		return nil, errors.New("the sandbox lifecycle returned no installation record")
	}
	return registered, nil
}

// RunRegisteredProvisioning ports provisioning/registered.rs::run: admit the
// owner, provision the managed accounts when setup is missing or broken, then
// register both accounts in the current package and publish readiness under the
// machine-wide setup lock.
func RunRegisteredProvisioning(
	identity *ClientIdentity,
	request *ProvisioningRequest,
	sandboxSID []byte,
	stopped func() bool,
	registerOwner OwnerRegistrar,
	setup SetupRunner,
) (provisionservice.Response, error) {
	if request == nil {
		return provisionservice.Response{}, errors.New("missing provisioning request")
	}
	// Keep admission, account provisioning and package registration in one
	// transaction.
	lock, err := windowssandbox.AcquireSandboxSetupLock(pipeSetupLockTimeoutMillis)
	if err != nil {
		return provisionservice.Response{}, fmt.Errorf("acquire the sandbox setup lock: %w", err)
	}
	defer lock.Release()
	group, err := runtimeResolveSIDString(runtimeSandboxUsersGroup)
	if err != nil {
		return provisionservice.Response{}, err
	}
	if len(sandboxSID) == 0 || group != windowssandbox.StringFromSIDBytes(sandboxSID) {
		return provisionservice.Response{}, errors.New(sandboxGroupChanged)
	}
	settings := windowssandbox.OfflineProxySettings{
		ProxyPorts:        append([]uint16(nil), request.Settings.ProxyPorts...),
		AllowLocalBinding: request.Settings.AllowLocalBinding,
	}
	setupComplete, err := setupIsCompleteAsOwner(identity, settings)
	if err != nil {
		return provisionservice.Response{}, err
	}
	if request.RefreshOnly {
		previous, err := windowssandbox.LoadInstallation()
		if err != nil {
			return provisionservice.Response{}, err
		}
		if previous == nil {
			return provisionservice.Response{}, errors.New("registration refresh requires completed setup")
		}
		runtime, err := previous.RuntimeRegistration()
		if err != nil {
			return provisionservice.Response{}, err
		}
		if err := checkRegistrationRefreshAdmission(previous, runtime, identity, setupComplete); err != nil {
			return provisionservice.Response{}, err
		}
	}
	installation, err := registerAuthenticatedOwnerRecord(identity, registerOwner)
	if err != nil {
		return provisionservice.Response{}, err
	}
	family, hasFamily, err := runtimeProcessPackageFamily()
	if err != nil {
		return provisionservice.Response{}, err
	}
	fullName, hasFullName, err := runtimeProcessPackageFullName()
	if err != nil {
		return provisionservice.Response{}, err
	}
	if installation.Runtime == nil {
		if !hasFamily {
			return provisionservice.Response{}, errors.New("the sandbox service has no package identity")
		}
		installation.Runtime = &windowssandbox.RuntimeRegistration{
			PackageFamily: family,
			Accounts:      []windowssandbox.RuntimeAccountRegistration{},
			MetadataRoots: []string{},
		}
	}
	// Persist the authenticated owner before setup can rotate shared credentials.
	if err := windowssandbox.SaveInstallation(installation); err != nil {
		return provisionservice.Response{}, fmt.Errorf("persist registered sandbox owner: %w", err)
	}
	// Missing, disabled or expired accounts require full setup before runtime logon.
	for _, account := range []windowssandbox.SandboxRuntimeAccount{
		windowssandbox.SandboxRuntimeAccountOffline,
		windowssandbox.SandboxRuntimeAccountOnline,
	} {
		flags, found, err := runtimeLocalUserFlags(account.Username())
		if err != nil {
			return provisionservice.Response{}, fmt.Errorf("inspect registered sandbox account %s: %w", account.Username(), err)
		}
		if !found || flags&(userFlagAccountDisable|userFlagPasswordExpired) != 0 {
			setupComplete = false
		}
	}
	if setupComplete && !request.RefreshOnly {
		repair, err := CredentialsNeedRepair(installation, func(account windowssandbox.SandboxRuntimeAccount) (windows.Token, error) {
			return logonRegisteredRuntimeAccount(identity, account)
		})
		if err != nil {
			return provisionservice.Response{}, fmt.Errorf("check registered sandbox credentials: %w", err)
		}
		setupComplete = !repair
	}
	if !setupComplete {
		// Account state can change outside the setup lock; refresh never repairs it.
		if request.RefreshOnly {
			return provisionservice.Response{}, errors.New("registration refresh cannot repair sandbox setup")
		}
		// Setup resolves this name for ACL trustees; it must still name the held token's user.
		setupOwnerSID, err := runtimeResolveSIDString(identity.Account)
		if err != nil {
			return provisionservice.Response{}, fmt.Errorf("resolve registered sandbox setup owner: %w", err)
		}
		if setupOwnerSID != identity.UserSID {
			return provisionservice.Response{}, errors.New("sandbox setup account no longer matches the authenticated owner")
		}
		if setup == nil {
			return provisionservice.Response{}, unavailable("the packaged setup helper " + packagedSetupHelperName + " is not wired")
		}
		if err := setup(identity, request); err != nil {
			return provisionservice.Response{}, fmt.Errorf("provision registered sandbox accounts and permissions: %w", err)
		}
	}
	if !hasFullName {
		return provisionservice.Response{}, errors.New("the sandbox service has no package identity")
	}
	if err := ProvisionRegisteredRuntime(identity, installation, fullName, stopped); err != nil {
		LogError(EventProvisioningFailed, "Codex sandbox provisioning failed: "+ErrorMessage(err))
		return provisionservice.Response{}, fmt.Errorf("registered sandbox provisioning failed: %w", err)
	}
	LogInformation(EventProvisioningSucceeded, "Codex sandbox provisioning completed successfully.")
	return provisionservice.Response{Kind: provisionservice.ResponseOK}, nil
}

// checkRegistrationRefreshAdmission ports the refresh-only gate: startup may
// update registrations but must never create accounts or repair teardown.
func checkRegistrationRefreshAdmission(
	record *windowssandbox.InstallationRecord,
	runtime *windowssandbox.RuntimeRegistration,
	identity *ClientIdentity,
	setupComplete bool,
) error {
	family, hasFamily, err := runtimeProcessPackageFamily()
	if err != nil {
		return err
	}
	if record.UserSID != identity.UserSID ||
		record.CodexHome != identity.CodexHome ||
		!hasFamily ||
		!strings.EqualFold(runtime.PackageFamily, family) ||
		!runtime.CanResumeRegistration() ||
		!setupComplete {
		return errors.New("registration refresh requires unchanged sandbox ownership and settings")
	}
	for _, entry := range runtime.Accounts {
		username := entry.Account.Username()
		flags, found, err := runtimeLocalUserFlags(username)
		if err != nil {
			return err
		}
		resolved, err := runtimeResolveSIDString(username)
		if err != nil {
			return err
		}
		if !found ||
			flags&(userFlagAccountDisable|userFlagPasswordExpired) != 0 ||
			resolved != entry.UserSID {
			return errors.New("registration refresh cannot repair or replace a sandbox account")
		}
	}
	return nil
}
