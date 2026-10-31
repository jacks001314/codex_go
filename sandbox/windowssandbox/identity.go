package windowssandbox

import (
	"encoding/base64"
	"fmt"
	"os"

	json "github.com/goccy/go-json"
)

type SandboxCredentials struct {
	Username string
	Password string
	Domain   string
}

var (
	runElevatedSetupForCredentials         = RunElevatedSetup
	runSetupRefreshForCredentials          = RunSetupRefreshForRequest
	selectSandboxCredentialsForCredentials = SelectSandboxCredentials
	sandboxAccountFlagsForCredentials      = readSandboxAccountFlags
)

func RequireLogonSandboxCreds(codexHome string) (*SandboxCredentials, error) {
	return selectSandboxCredentialsForCredentials(codexHome, SandboxNetworkIdentityOffline)
}

func RequireLogonSandboxCredsForPermissions(
	permissions *ResolvedWindowsSandboxPermissions,
	commandCWD string,
	envMap map[string]string,
	codexHome string,
	readRootsOverride []string,
	readRootsOverrideSet bool,
	readRootsIncludePlatformDefaults bool,
	writeRootsOverride []string,
	writeRootsOverrideSet bool,
	denyReadPathsOverride []string,
	denyWritePathsOverride []string,
	proxyEnforced bool,
	proxySettingsMode ProxySettingsMode,
) (*SandboxCredentials, error) {
	return requireLogonSandboxCredsForPermissions(
		permissions,
		commandCWD,
		envMap,
		codexHome,
		readRootsOverride,
		readRootsOverrideSet,
		readRootsIncludePlatformDefaults,
		writeRootsOverride,
		writeRootsOverrideSet,
		denyReadPathsOverride,
		denyWritePathsOverride,
		proxyEnforced,
		proxySettingsMode,
		true,
		true,
	)
}

func requireLogonSandboxCredsForPermissions(
	permissions *ResolvedWindowsSandboxPermissions,
	commandCWD string,
	envMap map[string]string,
	codexHome string,
	readRootsOverride []string,
	readRootsOverrideSet bool,
	readRootsIncludePlatformDefaults bool,
	writeRootsOverride []string,
	writeRootsOverrideSet bool,
	denyReadPathsOverride []string,
	denyWritePathsOverride []string,
	proxyEnforced bool,
	proxySettingsMode ProxySettingsMode,
	allowSetupElevation bool,
	verifyStoredCredentials bool,
) (*SandboxCredentials, error) {
	if permissions == nil || codexHome == "" {
		return nil, ErrInvalidRequest
	}
	neededRead := cloneStrings(readRootsOverride)
	if !readRootsOverrideSet {
		neededRead = GatherReadRoots(commandCWD, permissions, envMap, codexHome)
	}
	neededWrite := cloneStrings(writeRootsOverride)
	if !writeRootsOverrideSet {
		neededWrite = GatherWriteRootsForPermissions(permissions, commandCWD, envMap)
	}
	networkIdentity := SandboxNetworkIdentityFromPermissions(permissions, proxyEnforced)
	marker, err := ReadSetupMarker(codexHome)
	if err != nil {
		return nil, err
	}
	desiredProxySettings := DesiredOfflineProxySettings(marker, proxySettingsMode, envMap, networkIdentity)
	setupReason := ""
	var identity *SandboxCredentials
	if marker.VersionMatches() {
		if reason := marker.RequestMismatchReason(networkIdentity, desiredProxySettings); reason != "" {
			setupReason = reason
		} else {
			if verifyStoredCredentials {
				needsRepair, err := storedCredentialsNeedRepairForCredentials(codexHome)
				if err != nil {
					return nil, err
				}
				if needsRepair {
					setupReason = "stored sandbox account credentials were rejected"
				}
			}
			if setupReason != "" {
				// Full setup rotates both accounts after ownership validation.
			} else {
				selected, err := selectSandboxCredentialsForCredentials(codexHome, networkIdentity)
				if err != nil {
					return nil, err
				}
				if selected == nil {
					setupReason = "sandbox users missing or incompatible with marker version"
				}
				identity = selected
			}
		}
	} else {
		setupReason = "sandbox setup marker missing or incompatible"
	}

	if identity != nil {
		// Cleanup may also have removed the group, so repair missing or disabled
		// accounts before the ACL refresh can fail. Expired passwords also need a
		// full setup because a refresh cannot rotate them or update the stored
		// credentials (Rust #46239).
		for _, username := range []string{OfflineUsername, OnlineUsername} {
			flags, found, err := sandboxAccountFlagsForCredentials(username)
			if err != nil {
				continue
			}
			if !sandboxAccountNeedsRepair(flags, found) {
				continue
			}
			const reason = "sandbox account is missing, disabled, or password expired"
			if err := removeStaleSandboxUsersFile(codexHome, reason); err != nil {
				return nil, err
			}
			setupReason = reason
			identity = nil
			break
		}
	}

	request := &SandboxSetupRequest{
		CodexHome:     codexHome,
		CommandCWD:    commandCWD,
		Env:           envMap,
		ProxyEnforced: proxyEnforced,
		Permissions:   permissions,
		Overrides: SetupRootOverrides{
			ReadRoots:                        neededRead,
			ReadRootsSet:                     true,
			ReadRootsIncludePlatformDefaults: readRootsIncludePlatformDefaults,
			WriteRoots:                       neededWrite,
			WriteRootsSet:                    true,
			DenyReadPaths:                    cloneStrings(denyReadPathsOverride),
			DenyWritePaths:                   cloneStrings(denyWritePathsOverride),
		},
		OfflineProxySettings: &desiredProxySettings,
	}
	if identity == nil {
		if setupReason != "" {
			_ = LogNoteInDir(SandboxDir(codexHome), "sandbox setup required: "+setupReason)
		} else {
			_ = LogNoteInDir(SandboxDir(codexHome), "sandbox setup required")
		}
		if !allowSetupElevation {
			return nil, fmt.Errorf("%w: Windows sandbox setup is missing or out of date; run the Windows sandbox setup explicitly", ErrSetupElevationDisallowed)
		}
		if err := runElevatedSetupForCredentials(request); err != nil {
			return nil, err
		}
		identity, err = selectSandboxCredentialsForCredentials(codexHome, networkIdentity)
		if err != nil {
			return nil, err
		}
	}
	if err := runSetupRefreshForCredentials(request); err != nil {
		return nil, err
	}
	if identity == nil {
		return nil, fmt.Errorf("Windows sandbox setup is missing or out of date; rerun the sandbox setup with elevation")
	}
	return identity, nil
}

func RefreshLogonSandboxCredsForPermissions(
	permissions *ResolvedWindowsSandboxPermissions,
	commandCWD string,
	envMap map[string]string,
	codexHome string,
	readRootsOverride []string,
	readRootsOverrideSet bool,
	readRootsIncludePlatformDefaults bool,
	writeRootsOverride []string,
	writeRootsOverrideSet bool,
	denyReadPathsOverride []string,
	denyWritePathsOverride []string,
	proxyEnforced bool,
	proxySettingsMode ProxySettingsMode,
) (*SandboxCredentials, error) {
	return refreshLogonSandboxCredsForPermissions(
		permissions,
		commandCWD,
		envMap,
		codexHome,
		readRootsOverride,
		readRootsOverrideSet,
		readRootsIncludePlatformDefaults,
		writeRootsOverride,
		writeRootsOverrideSet,
		denyReadPathsOverride,
		denyWritePathsOverride,
		proxyEnforced,
		proxySettingsMode,
		true,
	)
}

func refreshLogonSandboxCredsForPermissions(
	permissions *ResolvedWindowsSandboxPermissions,
	commandCWD string,
	envMap map[string]string,
	codexHome string,
	readRootsOverride []string,
	readRootsOverrideSet bool,
	readRootsIncludePlatformDefaults bool,
	writeRootsOverride []string,
	writeRootsOverrideSet bool,
	denyReadPathsOverride []string,
	denyWritePathsOverride []string,
	proxyEnforced bool,
	proxySettingsMode ProxySettingsMode,
	allowSetupElevation bool,
) (*SandboxCredentials, error) {
	if err := RemoveSandboxUsersFile(codexHome); err != nil {
		return nil, err
	}
	return requireLogonSandboxCredsForPermissions(
		permissions,
		commandCWD,
		envMap,
		codexHome,
		readRootsOverride,
		readRootsOverrideSet,
		readRootsIncludePlatformDefaults,
		writeRootsOverride,
		writeRootsOverrideSet,
		denyReadPathsOverride,
		denyWritePathsOverride,
		proxyEnforced,
		proxySettingsMode,
		allowSetupElevation,
		false,
	)
}

func DesiredOfflineProxySettings(marker *SetupMarker, proxySettingsMode ProxySettingsMode, envMap map[string]string, networkIdentity SandboxNetworkIdentity) OfflineProxySettings {
	if marker.VersionMatches() && proxySettingsMode == ProxySettingsPreserve {
		return marker.OfflineProxySettings()
	}
	return OfflineProxySettingsFromEnv(envMap, networkIdentity)
}

func SelectSandboxCredentials(codexHome string, identity SandboxNetworkIdentity) (*SandboxCredentials, error) {
	marker, err := ReadSetupMarker(codexHome)
	if err != nil {
		return nil, err
	}
	if !marker.VersionMatches() {
		return nil, nil
	}
	users, err := ReadSandboxUsersFile(codexHome)
	if err != nil {
		return nil, err
	}
	if !users.VersionMatches() {
		return nil, nil
	}
	record := users.Offline
	if identity == SandboxNetworkIdentityOnline {
		record = users.Online
	}
	password, err := DecodeSandboxUserPassword(record)
	if err != nil {
		return nil, err
	}
	return &SandboxCredentials{Username: record.Username, Password: password, Domain: "."}, nil
}

func DecodeSandboxUserPassword(record SandboxUserRecord) (string, error) {
	blob, err := base64.StdEncoding.DecodeString(record.Password)
	if err != nil {
		return "", fmt.Errorf("base64 decode password: %w", err)
	}
	decrypted, err := DPAPIUnprotect(blob)
	if err != nil {
		return "", err
	}
	return string(decrypted), nil
}

// sandboxAccountNeedsRepair reports whether a managed account must be repaired
// before setup can be considered complete (Rust #46239 read_local_user_flags
// gate). A missing account and a disabled or password-expired account all
// require full setup.
func sandboxAccountNeedsRepair(flags uint32, found bool) bool {
	if !found {
		return true
	}
	const (
		userFlagAccountDisable  = 0x0002
		userFlagPasswordExpired = 0x800000
	)
	return flags&(userFlagAccountDisable|userFlagPasswordExpired) != 0
}

// removeStaleSandboxUsersFile drops credentials that an older setup (or
// service) could mistake for proof of completed setup once an account needs
// repair (Rust #46239 remove_sandbox_users_file).
func removeStaleSandboxUsersFile(codexHome string, reason string) error {
	path := SandboxUsersPath(codexHome)
	_ = LogNoteInDir(SandboxDir(codexHome), reason+"; deleting "+path)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete %s: %w", path, err)
	}
	return nil
}

func SandboxSetupIsComplete(codexHome string) (bool, error) {
	marker, err := ReadSetupMarker(codexHome)
	if err != nil {
		return false, nil
	}
	if !marker.VersionMatches() {
		return false, nil
	}
	users, err := ReadSandboxUsersFile(codexHome)
	if err != nil {
		return false, nil
	}
	if !users.VersionMatches() {
		return false, nil
	}
	if !CurrentSetupRuntime().IsLegacy() {
		ready, err := registeredSetupIsReady()
		if err != nil || !ready {
			return false, nil
		}
	}
	return true, nil
}

// registeredSetupIsReady checks the receipt Go can verify for an opt-in
// registered Core. The service-owned installation record and staged alias do
// not exist in Go's direct-setup architecture, so the observable requirement is
// that the requesting process actually carries an OS package identity
// (Rust `registered_setup_is_ready`).
func registeredSetupIsReady() (bool, error) {
	return CurrentProcessHasPackageIdentity()
}

func WriteSandboxUsersFile(codexHome string, users *SandboxUsersFile) error {
	return writeSandboxUsersFile(codexHome, users, func(path string, data []byte) error {
		return os.WriteFile(path, data, 0o600)
	})
}

// WriteSandboxUsersFileForProvisioning replaces the credentials file through a
// fresh handle relative to the pinned secrets directory instead of writing
// through a caller-controlled destination entry (Rust #42342).
func WriteSandboxUsersFileForProvisioning(codexHome string, users *SandboxUsersFile) error {
	return writeSandboxUsersFile(codexHome, users, WriteFileAtomically)
}

func writeSandboxUsersFile(codexHome string, users *SandboxUsersFile, write func(string, []byte) error) error {
	if users == nil {
		return ErrInvalidRequest
	}
	if err := os.MkdirAll(SandboxSecretsDir(codexHome), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(users, "", "  ")
	if err != nil {
		return err
	}
	return write(SandboxUsersPath(codexHome), data)
}

func ReadSandboxUsersFile(codexHome string) (*SandboxUsersFile, error) {
	data, err := os.ReadFile(SandboxUsersPath(codexHome))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var users SandboxUsersFile
	if err := json.Unmarshal(data, &users); err != nil {
		return nil, err
	}
	return &users, nil
}

func RemoveSandboxUsersFile(codexHome string) error {
	err := os.Remove(SandboxUsersPath(codexHome))
	if err == nil || os.IsNotExist(err) {
		return nil
	}
	return err
}
