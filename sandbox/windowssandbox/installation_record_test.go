package windowssandbox

import (
	"bytes"
	"encoding/binary"
	"reflect"
	"strings"
	"testing"
	"unicode/utf16"

	json "github.com/goccy/go-json"
)

const readyPackageFullName = "OpenAI.Codex_1.0.0.0_arm64__publisher"

func installationFixture() *InstallationRecord {
	return &InstallationRecord{
		UserSID:   "S-1-5-21-1-2-3-1000",
		CodexHome: `C:\Users\owner\.codex`,
		SessionID: 1,
		DesktopInstallation: &DesktopInstallation{
			CreatedCodexHome: true,
			CacheHome:        `C:\Users\owner\.cache`,
		},
		Runtime: &RuntimeRegistration{
			PackageFamily: "OpenAI.Codex_publisher",
			Accounts: []RuntimeAccountRegistration{{
				Account: SandboxRuntimeAccountOffline,
				UserSID: "S-1-5-21-1-2-3-1001",
			}},
		},
	}
}

func retiredInstallationFixture() *InstallationRecord {
	record := installationFixture()
	generation := "one-cleanup-generation"
	record.Runtime.Retiring = &generation
	return record
}

func readyRuntimeFixture() *RuntimeRegistration {
	runtime := *installationFixture().Runtime
	runtime.ReadyPackage = stringPointer(readyPackageFullName)
	runtime.Accounts[0].AliasPath = stringPointer(`C:\aliases\offline.exe`)
	runtime.Accounts = append(runtime.Accounts, RuntimeAccountRegistration{
		Account:   SandboxRuntimeAccountOnline,
		UserSID:   "S-1-5-21-1-2-3-1002",
		AliasPath: stringPointer(`C:\aliases\online.exe`),
	})
	return &runtime
}

func stringPointer(value string) *string { return &value }

func encodeInstallationValueText(text string) ([]byte, error) {
	units := utf16.Encode([]rune(text))
	data := make([]byte, (len(units)+1)*2)
	for index, unit := range units {
		binary.LittleEndian.PutUint16(data[index*2:], unit)
	}
	return data, nil
}

func TestInstallationJournalRoundTripRetainsOwnershipAndFence(t *testing.T) {
	for _, record := range []*InstallationRecord{installationFixture(), retiredInstallationFixture()} {
		data, err := json.Marshal(record)
		if err != nil {
			t.Fatalf("Marshal() error = %v", err)
		}
		var restored InstallationRecord
		if err := json.Unmarshal(data, &restored); err != nil {
			t.Fatalf("Unmarshal() error = %v", err)
		}
		if !reflect.DeepEqual(&restored, record) {
			t.Fatalf("restored = %#v, want %#v", &restored, record)
		}
	}
}

func TestProductionRecordWithoutOptionalCoreOrDesktopFieldsLoads(t *testing.T) {
	payload := []byte(`{"user_sid":"S-1-5-21-1-2-3-1000","codex_home":"C:\\Users\\owner\\.codex","session_id":1}`)
	var record InstallationRecord
	if err := json.Unmarshal(payload, &record); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if record.Runtime != nil || record.DesktopInstallation != nil || record.UserSID != "S-1-5-21-1-2-3-1000" {
		t.Fatalf("record = %#v", record)
	}
}

func TestInstallationJournalSerializesDesktopAndRuntimeNullsLikeRust(t *testing.T) {
	payload, err := json.Marshal(&InstallationRecord{UserSID: "sid", CodexHome: "home", SessionID: 1})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	text := string(payload)
	if !strings.Contains(text, `"desktop_installation":null`) {
		t.Fatalf("desktop_installation must serialize as null: %s", text)
	}
	if strings.Contains(text, `"runtime"`) {
		t.Fatalf("absent runtime must be omitted: %s", text)
	}
	ready, err := json.Marshal(installationFixture())
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	for _, want := range []string{`"ready_package":null`, `"retiring":null`, `"alias_path":null`, `"cleanup_logon_pending":false`} {
		if !strings.Contains(string(ready), want) {
			t.Fatalf("record %s missing %s", ready, want)
		}
	}
}

func TestMetadataRootHistorySurvivesRoundTrip(t *testing.T) {
	record := installationFixture()
	record.Runtime.MetadataRoots = []string{`C:\Users\owner\AppData\Roaming`, `D:\Redirected\Roaming`}
	for _, candidate := range []*InstallationRecord{record, func() *InstallationRecord {
		updated := installationFixture()
		updated.Runtime.MetadataRoots = append([]string{}, record.Runtime.MetadataRoots...)
		updated.Runtime.ReadyPackage = stringPointer(readyPackageFullName)
		return updated
	}()} {
		payload, err := json.Marshal(candidate)
		if err != nil {
			t.Fatalf("Marshal() error = %v", err)
		}
		var restored InstallationRecord
		if err := json.Unmarshal(payload, &restored); err != nil {
			t.Fatalf("Unmarshal() error = %v", err)
		}
		if !reflect.DeepEqual(&restored, candidate) {
			t.Fatalf("restored = %#v, want %#v", &restored, candidate)
		}
	}
}

func TestOlderRuntimeReceiptsDefaultCleanupLogonAndMetadataRoots(t *testing.T) {
	record := installationFixture()
	payload, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var value map[string]any
	if err := json.Unmarshal(payload, &value); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	runtimeValue := value["runtime"].(map[string]any)
	delete(runtimeValue, "metadata_roots")
	accounts := runtimeValue["accounts"].([]any)
	delete(accounts[0].(map[string]any), "cleanup_logon_pending")
	older, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var restored InstallationRecord
	if err := json.Unmarshal(older, &restored); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if !reflect.DeepEqual(&restored, record) {
		t.Fatalf("restored = %#v, want %#v", &restored, record)
	}
}

func TestLegacyNullCleanupAliasPreservesActiveRecord(t *testing.T) {
	record := installationFixture()
	payload, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var value map[string]any
	if err := json.Unmarshal(payload, &value); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	runtimeValue := value["runtime"].(map[string]any)
	delete(runtimeValue, "retiring")
	runtimeValue["cleanup"] = nil
	legacy, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var restored InstallationRecord
	if err := json.Unmarshal(legacy, &restored); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if !reflect.DeepEqual(&restored, record) {
		t.Fatalf("restored = %#v, want %#v", &restored, record)
	}
}

func TestLegacyNonNullCleanupJournalCannotDropItsFence(t *testing.T) {
	for _, stage := range []string{"native_started", "native_complete"} {
		payload := `{"user_sid":"sid","codex_home":"home","session_id":1,"desktop_installation":null,"runtime":{"package_family":"OpenAI.Codex_publisher","accounts":[],"ready_package":null,"cleanup":{"id":"one-cleanup-generation","stage":"` + stage + `","accounts":[]}}}`
		var record InstallationRecord
		if err := json.Unmarshal([]byte(payload), &record); err == nil {
			t.Fatalf("stage %s: legacy cleanup journal must not decode", stage)
		}
	}
}

func TestReadinessRequiresReceiptForCurrentPackage(t *testing.T) {
	runtime := readyRuntimeFixture()
	if !runtime.ReadyForPackage(readyPackageFullName) || !runtime.CanResumeRegistration() {
		t.Fatal("ready runtime must be resumable")
	}
	if runtime.ReadyForPackage("OpenAI.Codex_2.0.0.0_arm64__publisher") {
		t.Fatal("a different package version must not be ready")
	}
	runtime.ReadyPackage = nil
	if runtime.ReadyForPackage(readyPackageFullName) || !runtime.CanResumeRegistration() {
		t.Fatal("missing receipt must keep registration resumable but not ready")
	}
}

func TestReadinessRequiresBothDistinctAccountReceipts(t *testing.T) {
	cases := map[string]func(*RuntimeRegistration){
		"missing account": func(runtime *RuntimeRegistration) { runtime.Accounts = runtime.Accounts[:1] },
		"missing alias":   func(runtime *RuntimeRegistration) { runtime.Accounts[1].AliasPath = nil },
		"duplicate role":  func(runtime *RuntimeRegistration) { runtime.Accounts[1].Account = SandboxRuntimeAccountOffline },
		"extra account":   func(runtime *RuntimeRegistration) { runtime.Accounts = append(runtime.Accounts, runtime.Accounts[0]) },
	}
	for name, mutate := range cases {
		runtime := readyRuntimeFixture()
		mutate(runtime)
		if runtime.ReadyForPackage(readyPackageFullName) || runtime.CanResumeRegistration() {
			t.Fatalf("%s: runtime = %#v", name, runtime)
		}
	}
}

func TestReadinessIsRevokedByRetirementFence(t *testing.T) {
	runtime := readyRuntimeFixture()
	generation := "one-cleanup-generation"
	runtime.Retiring = &generation
	if runtime.ReadyForPackage(readyPackageFullName) || runtime.CanResumeRegistration() {
		t.Fatal("retiring runtime must not be ready or resumable")
	}
}

func TestAdmissionAcceptsPackageFamilyCasing(t *testing.T) {
	for _, stored := range []string{"OpenAI.Codex_publisher", "openai.codex_PUBLISHER"} {
		record := installationFixture()
		record.Runtime.PackageFamily = stored
		for _, requested := range []string{"OpenAI.Codex_publisher", "OPENAI.CODEX_PUBLISHER"} {
			if err := record.AdmitOwner(record, requested); err != nil {
				t.Fatalf("stored %q requested %q: error = %v", stored, requested, err)
			}
		}
	}
}

func TestAdmissionRejectsOtherOwnerHomeAndFamily(t *testing.T) {
	record := installationFixture()
	cases := []struct {
		home   string
		sid    string
		family string
	}{
		{record.CodexHome, "S-1-5-21-1-2-3-2000", record.Runtime.PackageFamily},
		{`C:\Users\owner\other`, record.UserSID, record.Runtime.PackageFamily},
		{record.CodexHome, record.UserSID, "OpenAI.CodexBeta_publisher"},
		{record.CodexHome, record.UserSID, "OpenAI.Codex_otherpublisher"},
	}
	for _, testCase := range cases {
		requested := installationFixture()
		requested.CodexHome = testCase.home
		requested.UserSID = testCase.sid
		if err := record.AdmitOwner(requested, testCase.family); err == nil {
			t.Fatalf("admission must fail for %#v", testCase)
		}
	}
}

func TestReturnedOwnerCannotCancelPersistedRetirementFence(t *testing.T) {
	record := retiredInstallationFixture()
	payload, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var restored InstallationRecord
	if err := json.Unmarshal(payload, &restored); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if err := restored.AdmitOwner(installationFixture(), installationFixture().Runtime.PackageFamily); err == nil {
		t.Fatal("a returning owner must not cancel the retirement fence")
	}
}

func TestInterruptedCleanupLogonBlocksAdmissionAfterReload(t *testing.T) {
	original := installationFixture()
	original.Runtime = readyRuntimeFixture()
	original.Runtime.Accounts[0].CleanupLogonPending = true
	payload, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var restored InstallationRecord
	if err := json.Unmarshal(payload, &restored); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if restored.Runtime.ReadyForPackage(readyPackageFullName) {
		t.Fatal("interrupted cleanup must revoke readiness")
	}
	if err := restored.AdmitOwner(original, "OpenAI.Codex_publisher"); err == nil {
		t.Fatal("interrupted cleanup must block admission")
	}
	restored.Runtime.Accounts[0].CleanupLogonPending = false
	if !restored.Runtime.ReadyForPackage(readyPackageFullName) {
		t.Fatal("cleared cleanup must restore readiness")
	}
	if err := restored.AdmitOwner(original, "OpenAI.Codex_publisher"); err != nil {
		t.Fatalf("cleared cleanup admission error = %v", err)
	}
}

func TestInstallationValueEncodingIsBoundedAndNullTerminated(t *testing.T) {
	record := installationFixture()
	data, err := encodeInstallationValue(record)
	if err != nil {
		t.Fatalf("encodeInstallationValue() error = %v", err)
	}
	if data[len(data)-1] != 0 || data[len(data)-2] != 0 {
		t.Fatal("record must be NUL-terminated")
	}
	restored, err := decodeInstallationValue(data)
	if err != nil {
		t.Fatalf("decodeInstallationValue() error = %v", err)
	}
	if !reflect.DeepEqual(restored, record) {
		t.Fatalf("restored = %#v, want %#v", restored, record)
	}
	if _, err := decodeInstallationValue(data[:len(data)-1]); err == nil {
		t.Fatal("odd length must be rejected")
	}
	if _, err := decodeInstallationValue(bytes.Repeat([]byte{0}, 2*(maxInstallationValueUnits+1))); err == nil {
		t.Fatal("oversized record must be rejected")
	}
	if _, err := decodeInstallationValue([]byte{0x41, 0x00, 0x42, 0x00}); err == nil {
		t.Fatal("record without NUL termination must be rejected")
	}
	oversized := installationFixture()
	oversized.CodexHome = strings.Repeat("a", maxInstallationValueUnits*2)
	if _, err := encodeInstallationValue(oversized); err == nil {
		t.Fatal("oversized record must not encode")
	}
}

func stubInstallationRegistry(t *testing.T) (map[string]string, *[]string) {
	t.Helper()
	originalRead, originalWrite, originalDelete, originalFlush := installationRecordRead, installationRecordWrite, installationRecordDelete, installationRecordFlush
	values := map[string]string{}
	flushed := []string{}
	installationRecordRead = func(key string) ([]byte, bool, error) {
		text, ok := values[key]
		if !ok {
			return nil, false, nil
		}
		data, err := encodeInstallationValueText(text)
		return data, true, err
	}
	installationRecordWrite = func(key string, text string) error {
		values[key] = text
		return nil
	}
	installationRecordDelete = func(key string) error {
		delete(values, key)
		return nil
	}
	installationRecordFlush = func(key string) error {
		flushed = append(flushed, key)
		return nil
	}
	t.Cleanup(func() {
		installationRecordRead, installationRecordWrite, installationRecordDelete, installationRecordFlush = originalRead, originalWrite, originalDelete, originalFlush
	})
	return values, &flushed
}

func TestLoadInstallationPrefersCoreRecord(t *testing.T) {
	values, _ := stubInstallationRegistry(t)
	core := installationFixture()
	core.Runtime = readyRuntimeFixture()
	coreText, err := installationValueText(core)
	if err != nil {
		t.Fatalf("encode error = %v", err)
	}
	values[CoreInstallationKey] = coreText
	legacy := installationFixture()
	legacy.Runtime = nil
	legacyText, err := installationValueText(legacy)
	if err != nil {
		t.Fatalf("encode error = %v", err)
	}
	values[InstallationKey] = legacyText
	loaded, err := LoadInstallation()
	if err != nil {
		t.Fatalf("LoadInstallation() error = %v", err)
	}
	if loaded == nil || loaded.Runtime == nil || !reflect.DeepEqual(loaded, core) {
		t.Fatalf("loaded = %#v", loaded)
	}
}

func TestLoadInstallationRejectsCoreRecordWithoutRuntime(t *testing.T) {
	values, _ := stubInstallationRegistry(t)
	broken := installationFixture()
	broken.Runtime = nil
	text, err := installationValueText(broken)
	if err != nil {
		t.Fatalf("encode error = %v", err)
	}
	values[CoreInstallationKey] = text
	if _, err := LoadInstallation(); err == nil {
		t.Fatal("Core record without runtime must be rejected")
	}
}

func TestLoadInstallationFallsBackToLegacyRecord(t *testing.T) {
	values, _ := stubInstallationRegistry(t)
	legacy := installationFixture()
	legacy.Runtime = nil
	text, err := installationValueText(legacy)
	if err != nil {
		t.Fatalf("encode error = %v", err)
	}
	values[InstallationKey] = text
	loaded, err := LoadInstallation()
	if err != nil {
		t.Fatalf("LoadInstallation() error = %v", err)
	}
	if loaded == nil || !reflect.DeepEqual(loaded, legacy) {
		t.Fatalf("loaded = %#v", loaded)
	}
}

func TestSaveInstallationChoosesKeyAndFlushes(t *testing.T) {
	values, flushed := stubInstallationRegistry(t)
	core := installationFixture()
	core.Runtime = readyRuntimeFixture()
	if err := SaveInstallation(core); err != nil {
		t.Fatalf("SaveInstallation() error = %v", err)
	}
	if _, ok := values[CoreInstallationKey]; !ok {
		t.Fatal("registered runtime must save into the Core key")
	}
	if len(*flushed) != 1 || (*flushed)[0] != CoreInstallationKey {
		t.Fatalf("flushed = %v", *flushed)
	}
	legacy := installationFixture()
	legacy.Runtime = nil
	if err := SaveInstallation(legacy); err != nil {
		t.Fatalf("SaveInstallation() error = %v", err)
	}
	if _, ok := values[InstallationKey]; !ok {
		t.Fatal("legacy ownership must save into the parent key")
	}
	if err := SaveInstallation(nil); err == nil {
		t.Fatal("nil record must not save")
	}
}

func TestRemoveInstallationRetiresOnlyLegacyOwnership(t *testing.T) {
	values, _ := stubInstallationRegistry(t)
	legacy := installationFixture()
	legacy.Runtime = nil
	text, err := installationValueText(legacy)
	if err != nil {
		t.Fatalf("encode error = %v", err)
	}
	values[InstallationKey] = text
	if err := RemoveInstallation(); err != nil {
		t.Fatalf("RemoveInstallation() error = %v", err)
	}
	if _, ok := values[InstallationKey]; ok {
		t.Fatal("legacy record must be removed")
	}
	core := installationFixture()
	core.Runtime = readyRuntimeFixture()
	coreText, err := installationValueText(core)
	if err != nil {
		t.Fatalf("encode error = %v", err)
	}
	values[CoreInstallationKey] = coreText
	if err := RemoveInstallation(); err == nil {
		t.Fatal("registered Core ownership must not be removed here")
	}
}

func TestInstallationRuntimeRegistrationErrorMatchesRust(t *testing.T) {
	var record InstallationRecord
	if _, err := record.RuntimeRegistration(); err == nil || err.Error() != "registered runtime ownership is missing" {
		t.Fatalf("error = %v", err)
	}
	if err := record.AdmitOwner(nil, "family"); err == nil {
		t.Fatal("missing runtime must reject admission")
	}
}
