package windowssandbox

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"unicode/utf16"

	json "github.com/goccy/go-json"
)

const (
	// InstallationKey stores desktop-created ownership outside the service key
	// so package updates can replace that key (Rust INSTALLATION_KEY).
	InstallationKey = `SOFTWARE\OpenAI\Codex\WindowsSandboxService`
	// InstallationValue is the bounded REG_SZ record value.
	InstallationValue = "ProvisionedInstallation"
	// CoreInstallationKey preserves registered-Core state while old services
	// delete the parent key on uninstall (Rust CORE_INSTALLATION_KEY).
	CoreInstallationKey = InstallationKey + `\RegisteredCore`
	// AppCoreRunnerAlias is the package-scoped execution alias for the app's
	// sandbox runner.
	AppCoreRunnerAlias = "codex-core-command-runner.exe"

	maxInstallationValueUnits = 4096
)

var (
	installationRecordRead   = readInstallationRecordValue
	installationRecordWrite  = writeInstallationRecordValue
	installationRecordDelete = deleteInstallationRecordValue
	installationRecordFlush  = flushInstallationRecordKey
)

// DesktopInstallation records ownership of desktop-created directories.
type DesktopInstallation struct {
	CreatedCodexHome bool   `json:"created_codex_home"`
	CacheHome        string `json:"cache_home"`
}

// InstallationRecord is the authenticated sandbox owner shared across service
// restarts and package updates (Rust InstallationRecord).
type InstallationRecord struct {
	UserSID             string               `json:"user_sid"`
	CodexHome           string               `json:"codex_home"`
	SessionID           uint32               `json:"session_id"`
	DesktopInstallation *DesktopInstallation `json:"desktop_installation"`
	Runtime             *RuntimeRegistration `json:"runtime,omitempty"`
}

// SandboxRuntimeAccount names the managed sandbox account a registration
// belongs to.
type SandboxRuntimeAccount string

const (
	SandboxRuntimeAccountOffline SandboxRuntimeAccount = "offline"
	SandboxRuntimeAccountOnline  SandboxRuntimeAccount = "online"
)

// Username returns the managed account name for a runtime account.
func (a SandboxRuntimeAccount) Username() string {
	switch a {
	case SandboxRuntimeAccountOffline:
		return OfflineUsername
	case SandboxRuntimeAccountOnline:
		return OnlineUsername
	default:
		return ""
	}
}

// RuntimeAccountRegistration is one account's app-runtime registration state.
type RuntimeAccountRegistration struct {
	// CleanupLogonPending is persisted before temporarily enabling an account
	// and cleared only after re-disabling it.
	CleanupLogonPending bool                  `json:"cleanup_logon_pending"`
	Account             SandboxRuntimeAccount `json:"account"`
	UserSID             string                `json:"user_sid"`
	// AliasPath is the OS-resolved alias written by the service, never inferred
	// from a user name.
	AliasPath *string `json:"alias_path"`
}

// RuntimeRegistration is written before registration so a service restart
// cannot lose cleanup ownership.
type RuntimeRegistration struct {
	PackageFamily string                       `json:"package_family"`
	Accounts      []RuntimeAccountRegistration `json:"accounts"`
	// MetadataRoots are owner-authorized AppData roots recorded before granting
	// metadata access; former roots are retained through redirection and
	// package updates until cleanup.
	MetadataRoots []string `json:"metadata_roots,omitempty"`
	// ReadyPackage is committed only after both registrations and metadata
	// grants succeed.
	ReadyPackage *string `json:"ready_package"`
	// Retiring fences setup during normal teardown; a restart must not replay
	// it.
	Retiring *string `json:"retiring"`
}

// UnmarshalJSON accepts the legacy "cleanup" alias but refuses to drop a
// non-null legacy cleanup journal.
func (r *RuntimeRegistration) UnmarshalJSON(data []byte) error {
	var raw struct {
		PackageFamily string                       `json:"package_family"`
		Accounts      []RuntimeAccountRegistration `json:"accounts"`
		MetadataRoots []string                     `json:"metadata_roots"`
		ReadyPackage  *string                      `json:"ready_package"`
		Retiring      json.RawMessage              `json:"retiring"`
		Cleanup       json.RawMessage              `json:"cleanup"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if len(raw.Retiring) > 0 && len(raw.Cleanup) > 0 {
		return errors.New("duplicate sandbox cleanup journal field")
	}
	journal := raw.Retiring
	if len(journal) == 0 {
		journal = raw.Cleanup
	}
	var retiring *string
	if len(journal) > 0 && string(journal) != "null" {
		var value string
		if err := json.Unmarshal(journal, &value); err != nil {
			return err
		}
		retiring = &value
	}
	*r = RuntimeRegistration{
		PackageFamily: raw.PackageFamily,
		Accounts:      raw.Accounts,
		MetadataRoots: raw.MetadataRoots,
		ReadyPackage:  raw.ReadyPackage,
		Retiring:      retiring,
	}
	return nil
}

// ReadyForPackage reports a committed registration receipt for the current
// package version.
func (r *RuntimeRegistration) ReadyForPackage(fullName string) bool {
	return r != nil && r.CanResumeRegistration() && r.ReadyPackage != nil && *r.ReadyPackage == fullName
}

// CanResumeRegistration reports complete account ownership. It allows
// registration to resume, not runtime execution: callers must still
// authenticate the owner and verify the live account SIDs/settings.
func (r *RuntimeRegistration) CanResumeRegistration() bool {
	if r == nil || r.Retiring != nil || len(r.Accounts) != 2 {
		return false
	}
	if r.Accounts[0].Account == r.Accounts[1].Account {
		return false
	}
	for _, account := range r.Accounts {
		if account.AliasPath == nil || account.CleanupLogonPending {
			return false
		}
	}
	return true
}

// RuntimeRegistration returns the registered-Core state or the same error Rust
// reports when it is missing.
func (r *InstallationRecord) RuntimeRegistration() (*RuntimeRegistration, error) {
	if r == nil || r.Runtime == nil {
		return nil, errors.New("registered runtime ownership is missing")
	}
	return r.Runtime, nil
}

// AdmitOwner refuses a different owner, home or package, and refuses to
// provision while a persisted cleanup is unfinished.
func (r *InstallationRecord) AdmitOwner(owner *InstallationRecord, family string) error {
	runtime, err := r.RuntimeRegistration()
	if err != nil {
		return err
	}
	if owner == nil {
		return errors.New("missing sandbox owner record")
	}
	if r.UserSID != owner.UserSID || r.CodexHome != owner.CodexHome || !strings.EqualFold(runtime.PackageFamily, family) {
		return errors.New("registered sandbox resources belong to a different owner or package")
	}
	if runtime.Retiring != nil {
		return errors.New("registered sandbox cleanup must finish before provisioning")
	}
	for _, account := range runtime.Accounts {
		if account.CleanupLogonPending {
			return errors.New("registered sandbox cleanup must finish before provisioning")
		}
	}
	return nil
}

// LoadInstallation reads the Core record first, then the legacy parent record.
func LoadInstallation() (*InstallationRecord, error) {
	record, err := loadInstallationFrom(CoreInstallationKey)
	if err != nil {
		return nil, err
	}
	if record != nil {
		if record.Runtime == nil {
			return nil, errors.New("Core installation record is missing runtime state")
		}
		return record, nil
	}
	return loadInstallationFrom(InstallationKey)
}

// SaveInstallation persists and flushes ownership before an operation may
// outlive this process. The caller must hold the sandbox setup mutex across its
// read/modify/write.
func SaveInstallation(record *InstallationRecord) error {
	if record == nil {
		return errors.New("missing installation record")
	}
	key := InstallationKey
	if record.Runtime != nil {
		key = CoreInstallationKey
	}
	if err := saveInstallationTo(key, record); err != nil {
		return err
	}
	return installationRecordFlush(key)
}

// RemoveInstallation retires legacy ownership. Registered Core ownership
// belongs to its post-service-exit finalizer.
func RemoveInstallation() error {
	record, err := LoadInstallation()
	if err != nil {
		return err
	}
	if record == nil {
		return nil
	}
	if record.Runtime != nil {
		return errors.New("registered Core ownership must be retired after service exit")
	}
	if err := installationRecordDelete(InstallationKey); err != nil {
		return err
	}
	return installationRecordFlush(InstallationKey)
}

func loadInstallationFrom(key string) (*InstallationRecord, error) {
	data, found, err := installationRecordRead(key)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}
	return decodeInstallationValue(data)
}

func saveInstallationTo(key string, record *InstallationRecord) error {
	text, err := installationValueText(record)
	if err != nil {
		return err
	}
	return installationRecordWrite(key, text)
}

// installationValueText serializes the bounded REG_SZ payload.
func installationValueText(record *InstallationRecord) (string, error) {
	payload, err := json.Marshal(record)
	if err != nil {
		return "", fmt.Errorf("serialize protected sandbox installation record: %w", err)
	}
	if len(utf16.Encode([]rune(string(payload))))+1 > maxInstallationValueUnits {
		return "", errors.New("sandbox installation record exceeds its size limit")
	}
	return string(payload), nil
}

// encodeInstallationValue serializes the bounded REG_SZ payload including its
// trailing NUL for registry readers that return raw bytes.
func encodeInstallationValue(record *InstallationRecord) ([]byte, error) {
	text, err := installationValueText(record)
	if err != nil {
		return nil, err
	}
	units := utf16.Encode([]rune(text))
	data := make([]byte, (len(units)+1)*2)
	for index, unit := range units {
		binary.LittleEndian.PutUint16(data[index*2:], unit)
	}
	return data, nil
}

// decodeInstallationValue mirrors Rust load_from's length and NUL checks.
func decodeInstallationValue(data []byte) (*InstallationRecord, error) {
	if len(data) == 0 || len(data)%2 != 0 || len(data) > maxInstallationValueUnits*2 {
		return nil, errors.New("sandbox installation record has an invalid length")
	}
	if data[len(data)-2] != 0 || data[len(data)-1] != 0 {
		return nil, errors.New("sandbox installation record is not null-terminated")
	}
	units := make([]uint16, len(data)/2-1)
	for index := range units {
		units[index] = binary.LittleEndian.Uint16(data[index*2:])
		if units[index] == 0 {
			return nil, errors.New("sandbox installation record is malformed")
		}
	}
	if !validInstallationUTF16(units) {
		return nil, errors.New("decode sandbox installation record")
	}
	var record InstallationRecord
	if err := json.Unmarshal([]byte(string(utf16.Decode(units))), &record); err != nil {
		return nil, fmt.Errorf("parse sandbox installation record: %w", err)
	}
	return &record, nil
}

func validInstallationUTF16(units []uint16) bool {
	for index := 0; index < len(units); index++ {
		unit := units[index]
		if !utf16.IsSurrogate(rune(unit)) {
			continue
		}
		if unit >= 0xDC00 || index+1 >= len(units) {
			return false
		}
		next := units[index+1]
		if next < 0xDC00 || next > 0xDFFF {
			return false
		}
		index++
	}
	return true
}
