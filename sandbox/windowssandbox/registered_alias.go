package windowssandbox

import (
	"errors"
	"path/filepath"
	"strings"
)

// SandboxRuntimeAccountForUsername maps a managed sandbox account name to the
// runtime account its registration belongs to.
func SandboxRuntimeAccountForUsername(username string) (SandboxRuntimeAccount, error) {
	switch {
	case strings.EqualFold(strings.TrimSpace(username), OfflineUsername):
		return SandboxRuntimeAccountOffline, nil
	case strings.EqualFold(strings.TrimSpace(username), OnlineUsername):
		return SandboxRuntimeAccountOnline, nil
	default:
		return "", errors.New("app runtime registration requires a managed sandbox account")
	}
}

// AuthorizeRunnerReceipt refuses a setup receipt owned by another user, Codex
// home or package version (Rust authorize_runner_receipt).
func AuthorizeRunnerReceipt(record *InstallationRecord, owner string, codexHome string, packageFullName string) error {
	if record == nil {
		return errors.New("registered Core setup has not completed")
	}
	if record.UserSID != owner {
		return errors.New("registered Core setup belongs to another owner")
	}
	if !installationHomeMatches(record.CodexHome, codexHome) {
		return errors.New("registered Core setup belongs to another Codex home")
	}
	registration, err := record.RuntimeRegistration()
	if err != nil {
		return err
	}
	if registration.Retiring != nil {
		return errors.New("registered Core setup is being removed")
	}
	if !registration.ReadyForPackage(packageFullName) {
		return errors.New("registered Core setup is not complete for the installed app; retry sandbox setup")
	}
	return nil
}

// RunnerAliasForAccount returns the service-written execution alias for one
// managed account. The alias is never inferred from the account name.
func RunnerAliasForAccount(registration *RuntimeRegistration, account SandboxRuntimeAccount, accountSID string) (string, error) {
	if registration == nil {
		return "", errors.New("registered Core setup has not completed")
	}
	var entry *RuntimeAccountRegistration
	for i := range registration.Accounts {
		if registration.Accounts[i].Account == account {
			entry = &registration.Accounts[i]
			break
		}
	}
	if entry == nil {
		return "", errors.New("managed account registration is not ready; retry setup")
	}
	if entry.UserSID != accountSID {
		return "", errors.New("managed runtime account changed; retry setup")
	}
	if entry.AliasPath == nil {
		return "", errors.New("registered Core alias is not ready; retry setup")
	}
	alias := *entry.AliasPath
	// Runtime receipts store Windows paths even when the service state is read
	// from another host, so validate the alias without host path semantics.
	if !isWindowsSandboxAbs(alias) || windowsBase(alias) != AppCoreRunnerAlias {
		return "", errors.New("registered Core setup contains an invalid alias")
	}
	return alias, nil
}

// windowsBase reports the final element of a Windows path.
func windowsBase(path string) string {
	path = strings.TrimRight(path, `\/`)
	if index := strings.LastIndexAny(path, `\/`); index >= 0 {
		return path[index+1:]
	}
	return path
}

// installationHomeMatches accepts the exact home the client recorded or its
// symlink-resolved form. The service stores the path the client sent, so both
// spellings can name the same receipt.
func installationHomeMatches(stored string, home string) bool {
	if stored == "" || home == "" {
		return false
	}
	clean := filepath.Clean(home)
	if stored == clean {
		return true
	}
	resolved, err := filepath.EvalSymlinks(clean)
	return err == nil && stored == resolved
}
