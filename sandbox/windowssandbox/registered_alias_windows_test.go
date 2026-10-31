//go:build windows

package windowssandbox

import (
	"errors"
	"strings"
	"testing"
)

func installRunnerAliasSeams(
	t *testing.T,
	record func() (*InstallationRecord, error),
	owner string,
	pkg string,
	hasPackage bool,
	sidFor func(string) (string, error),
) {
	t.Helper()
	previousLoad := loadInstallationForAlias
	previousOwner := currentRunnerOwnerSID
	previousPackage := currentRunnerPackage
	previousSID := runnerAccountSID
	loadInstallationForAlias = record
	currentRunnerOwnerSID = func() (string, error) { return owner, nil }
	currentRunnerPackage = func() (string, bool, error) { return pkg, hasPackage, nil }
	runnerAccountSID = sidFor
	t.Cleanup(func() {
		loadInstallationForAlias = previousLoad
		currentRunnerOwnerSID = previousOwner
		currentRunnerPackage = previousPackage
		runnerAccountSID = previousSID
	})
}

func managedSIDForAccount(username string) (string, error) {
	if strings.EqualFold(username, OfflineUsername) {
		return "S-1-5-21-1-2-3-1001", nil
	}
	return "S-1-5-21-1-2-3-1002", nil
}

func TestRegisteredRunnerAliasConsumesServiceReceipt(t *testing.T) {
	owner := "S-1-5-21-1-2-3-1000"
	home := aliasRecord().CodexHome
	loadRecord := func() (*InstallationRecord, error) { return aliasRecord(), nil }

	t.Run("managed account required", func(t *testing.T) {
		installRunnerAliasSeams(t, loadRecord, owner, readyPackageFullName, true, managedSIDForAccount)
		if _, err := RegisteredRunnerAlias(home, "Administrator"); err == nil {
			t.Fatal("non-managed account must be refused")
		}
	})

	t.Run("missing receipt", func(t *testing.T) {
		installRunnerAliasSeams(t, func() (*InstallationRecord, error) { return nil, nil }, owner, readyPackageFullName, true, managedSIDForAccount)
		_, err := RegisteredRunnerAlias(home, OfflineUsername)
		if err == nil || !strings.Contains(err.Error(), "has not completed") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("installed app required", func(t *testing.T) {
		installRunnerAliasSeams(t, loadRecord, owner, readyPackageFullName, false, managedSIDForAccount)
		_, err := RegisteredRunnerAlias(home, OfflineUsername)
		if err == nil || !strings.Contains(err.Error(), "installed app") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("receipt owner", func(t *testing.T) {
		installRunnerAliasSeams(t, loadRecord, "S-1-5-21-9-9-9-1000", readyPackageFullName, true, managedSIDForAccount)
		_, err := RegisteredRunnerAlias(home, OfflineUsername)
		if err == nil || !strings.Contains(err.Error(), "another owner") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("account sid lookup", func(t *testing.T) {
		installRunnerAliasSeams(t, loadRecord, owner, readyPackageFullName, true, func(string) (string, error) {
			return "", errors.New("account is missing")
		})
		_, err := RegisteredRunnerAlias(home, OfflineUsername)
		if err == nil || !strings.Contains(err.Error(), "account is missing") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("service alias", func(t *testing.T) {
		installRunnerAliasSeams(t, loadRecord, owner, readyPackageFullName, true, managedSIDForAccount)
		alias, err := RegisteredRunnerAlias(home, OfflineUsername)
		if err != nil || alias != aliasRunnerPath {
			t.Fatalf("RegisteredRunnerAlias() = %q, %v", alias, err)
		}
	})
}
