//go:build windows

package windowssandbox

import (
	"errors"
	"fmt"

	"golang.org/x/sys/windows"
)

var (
	// Seams let tests drive the receipt, the caller identity and the running
	// package without a real service installation.
	loadInstallationForAlias = LoadInstallation
	currentRunnerOwnerSID    = readCurrentRunnerOwnerSID
	currentRunnerPackage     = func() (string, bool, error) {
		return PackageFullNameForProcess(windows.CurrentProcess())
	}
	runnerAccountSID = resolveSandboxAccountSID
)

// RegisteredRunnerAlias consumes the service setup receipt and returns the
// package-scoped execution alias for the managed account (Rust
// app_package::registered_runner_alias). Launching a command never installs a
// package, so a missing or stale receipt is an error instead of a setup trigger.
func RegisteredRunnerAlias(codexHome string, sandboxUsername string) (string, error) {
	account, err := SandboxRuntimeAccountForUsername(sandboxUsername)
	if err != nil {
		return "", err
	}
	record, err := loadInstallationForAlias()
	if err != nil {
		return "", err
	}
	if record == nil {
		return "", errors.New("registered Core setup has not completed")
	}
	packageFullName, hasPackage, err := currentRunnerPackage()
	if err != nil {
		return "", fmt.Errorf("query the current package: %w", err)
	}
	if !hasPackage {
		return "", errors.New("registered Core launch requires an installed app")
	}
	owner, err := currentRunnerOwnerSID()
	if err != nil {
		return "", err
	}
	if err := AuthorizeRunnerReceipt(record, owner, codexHome, packageFullName); err != nil {
		return "", err
	}
	registration, err := record.RuntimeRegistration()
	if err != nil {
		return "", err
	}
	accountSID, err := runnerAccountSID(sandboxUsername)
	if err != nil {
		return "", err
	}
	return RunnerAliasForAccount(registration, account, accountSID)
}

// readCurrentRunnerOwnerSID resolves the requesting process identity from its
// token rather than USERNAME (Rust runtime_ownership::current_setup_user).
func readCurrentRunnerOwnerSID() (string, error) {
	user, err := currentSetupUser()
	if err != nil {
		return "", err
	}
	return resolveSandboxAccountSID(user)
}
