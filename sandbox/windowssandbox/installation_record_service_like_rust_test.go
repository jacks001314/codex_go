//go:build windows

package windowssandbox

import (
	"strings"
	"testing"
)

const serviceTestInstallationHome = "C:\\Users\\owner\\.codex"

func stubInstallationStore(t *testing.T) map[string][]byte {
	t.Helper()
	store := map[string][]byte{}
	originalRead, originalWrite, originalDelete, originalFlush :=
		installationRecordRead, installationRecordWrite, installationRecordDelete, installationRecordFlush
	t.Cleanup(func() {
		installationRecordRead, installationRecordWrite, installationRecordDelete, installationRecordFlush =
			originalRead, originalWrite, originalDelete, originalFlush
	})
	installationRecordRead = func(key string) ([]byte, bool, error) {
		data, found := store[key]
		return data, found, nil
	}
	installationRecordWrite = func(key string, text string) error {
		encoded, err := encodeInstallationValueText(text)
		if err != nil {
			return err
		}
		store[key] = encoded
		return nil
	}
	installationRecordDelete = func(key string) error {
		delete(store, key)
		return nil
	}
	installationRecordFlush = func(string) error { return nil }
	return store
}

func stubInstallationFamily(t *testing.T, family string, present bool) {
	t.Helper()
	original := currentInstallationPackageFamily
	t.Cleanup(func() { currentInstallationPackageFamily = original })
	currentInstallationPackageFamily = func() (string, bool, error) { return family, present, nil }
}

func storeRegisteredRecord(t *testing.T, store map[string][]byte, record *InstallationRecord) {
	t.Helper()
	text, err := installationValueText(record)
	if err != nil {
		t.Fatalf("installationValueText() error = %v", err)
	}
	if err := installationRecordWrite(CoreInstallationKey, text); err != nil {
		t.Fatalf("installationRecordWrite() error = %v", err)
	}
}

func TestSaveInstallationRecordClearsACallerSuppliedRuntime(t *testing.T) {
	stubInstallationStore(t)
	record := &InstallationRecord{UserSID: "S-1-5-21-1-2-3-1000", CodexHome: serviceTestInstallationHome, SessionID: 2}
	saved, err := SaveInstallationRecord(record)
	if err != nil {
		t.Fatalf("SaveInstallationRecord() error = %v", err)
	}
	if saved.Runtime != nil {
		t.Fatalf("SaveInstallationRecord() persisted a runtime: %+v", saved.Runtime)
	}
	loaded, err := LoadInstallation()
	if err != nil || loaded == nil {
		t.Fatalf("LoadInstallation() = %+v, %v", loaded, err)
	}
	if loaded.UserSID != record.UserSID || loaded.CodexHome != record.CodexHome || loaded.SessionID != record.SessionID {
		t.Fatalf("persisted record = %+v", loaded)
	}
}

func TestSaveInstallationRecordRefusesADifferentRegisteredOwner(t *testing.T) {
	store := stubInstallationStore(t)
	stubInstallationFamily(t, "OpenAI.Codex_publisher", true)
	current := installationFixture()
	storeRegisteredRecord(t, store, current)

	_, err := SaveInstallationRecord(&InstallationRecord{
		UserSID:   "S-1-5-21-9-9-9",
		CodexHome: current.CodexHome,
		SessionID: 1,
	})
	if err == nil || !strings.Contains(err.Error(), "different owner") {
		t.Fatalf("SaveInstallationRecord() error = %v, want the owner refusal", err)
	}
}

func TestSaveInstallationRecordPreservesRegisteredCoreState(t *testing.T) {
	store := stubInstallationStore(t)
	stubInstallationFamily(t, "openai.codex_PUBLISHER", true)
	current := installationFixture()
	ready := readyPackageFullName
	current.Runtime.ReadyPackage = &ready
	storeRegisteredRecord(t, store, current)

	saved, err := SaveInstallationRecord(&InstallationRecord{
		UserSID:   current.UserSID,
		CodexHome: current.CodexHome,
		SessionID: 9,
	})
	if err != nil {
		t.Fatalf("SaveInstallationRecord() error = %v", err)
	}
	if saved.Runtime == nil || saved.Runtime.ReadyPackage == nil || *saved.Runtime.ReadyPackage != readyPackageFullName {
		t.Fatalf("SaveInstallationRecord() dropped fresh registered state: %+v", saved.Runtime)
	}
	if saved.SessionID != 9 {
		t.Fatalf("SaveInstallationRecord() session = %d, want the caller's 9", saved.SessionID)
	}
}

func TestSaveInstallationRecordRequiresTheServicePackageIdentity(t *testing.T) {
	store := stubInstallationStore(t)
	stubInstallationFamily(t, "", false)
	current := installationFixture()
	storeRegisteredRecord(t, store, current)

	_, err := SaveInstallationRecord(&InstallationRecord{UserSID: current.UserSID, CodexHome: current.CodexHome})
	if err == nil || !strings.Contains(err.Error(), "no package identity") {
		t.Fatalf("SaveInstallationRecord() error = %v, want the package-identity refusal", err)
	}
}

func TestInstallationIsCurrentPackageComparesCaseInsensitively(t *testing.T) {
	stubInstallationFamily(t, "openai.codex_publisher", true)
	record := installationFixture()
	current, err := InstallationIsCurrentPackage(record)
	if err != nil || !current {
		t.Fatalf("InstallationIsCurrentPackage() = %v, %v, want true", current, err)
	}
	record.Runtime.PackageFamily = "OpenAI.CodexBeta_publisher"
	current, err = InstallationIsCurrentPackage(record)
	if err != nil || current {
		t.Fatalf("InstallationIsCurrentPackage() = %v, %v, want false", current, err)
	}
	if _, err := InstallationIsCurrentPackage(&InstallationRecord{}); err == nil {
		t.Fatal("a legacy record must not report a current package")
	}
}
