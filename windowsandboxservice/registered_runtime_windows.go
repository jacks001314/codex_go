//go:build windows

package windowsandboxservice

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"codex_go/sandbox/windowssandbox"
)

const (
	// runtimeSandboxUsersGroup is the managed-account membership Rust
	// requires before a runtime account may be registered (SANDBOX_USERS_GROUP).
	runtimeSandboxUsersGroup = "CodexSandboxUsers"
	// runtimeAdministratorsSID is S-1-5-32-544; a managed runtime account must
	// never hold it.
	runtimeAdministratorsSID = "S-1-5-32-544"
	// runtimeProfileNoUI keeps service profile operations non-interactive.
	runtimeProfileNoUI = 0x00000001
	// runtimeTokenGroupLimit mirrors the Rust bound on decoded group lists.
	runtimeTokenGroupLimit = 256
	// runtimeTokenGroupBytes bounds one token group query.
	runtimeTokenGroupBytes = 65536
	// netapiSuccess and netapiUserNotFound are NetUserGetInfo results.
	netapiSuccess      = 0
	netapiUserNotFound = 2221
)

// ErrRuntimeSetupInterrupted escapes request handling: pending resources are
// retained until this service exits (Rust RegistrationInterrupted).
var ErrRuntimeSetupInterrupted = errors.New("runtime setup interrupted; stopping service")

var (
	modRuntimeNetapi32           = windows.NewLazySystemDLL("netapi32.dll")
	modRuntimeUserenv            = windows.NewLazySystemDLL("userenv.dll")
	procRuntimeNetUserGetInfo    = modRuntimeNetapi32.NewProc("NetUserGetInfo")
	procRuntimeNetUserSetInfo    = modRuntimeNetapi32.NewProc("NetUserSetInfo")
	procRuntimeNetApiBufferFree  = modRuntimeNetapi32.NewProc("NetApiBufferFree")
	procRuntimeLoadUserProfile   = modRuntimeUserenv.NewProc("LoadUserProfileW")
	procRuntimeUnloadUserProfile = modRuntimeUserenv.NewProc("UnloadUserProfile")
)

// runtimeUserInfo1 is the NetUserGetInfo level-1 layout; only Flags is read.
type runtimeUserInfo1 struct {
	Name        *uint16
	Password    *uint16
	PasswordAge uint32
	Privilege   uint32
	HomeDir     *uint16
	Comment     *uint16
	Flags       uint32
	ScriptPath  *uint16
}

// runtimeProfileInfo mirrors PROFILEINFOW.
type runtimeProfileInfo struct {
	Size        uint32
	Flags       uint32
	UserName    *uint16
	ProfilePath *uint16
	DefaultPath *uint16
	ServerName  *uint16
	PolicyPath  *uint16
	Profile     windows.Handle
}

// runtimeGroup is one token group with its attributes.
type runtimeGroup struct {
	SID        string
	Attributes uint32
}

// runtimeResolveSIDString resolves an account or group name to its string SID
// (Rust resolve_sid plus string_from_sid_bytes).
var runtimeResolveSIDString = lookupRuntimeSIDString

// runtimeUserSIDString reads a token's user SID (Rust get_user_sid_bytes).
var runtimeUserSIDString = readRuntimeTokenUserSID

// runtimeTokenGroupSIDs reads a token's groups (Rust token_groups).
var runtimeTokenGroupSIDs = readRuntimeTokenGroups

// runtimeLocalUserFlags reads local account flags, reporting false when the
// account does not exist on this machine (Rust local_user_flags).
var runtimeLocalUserFlags = readRuntimeLocalUserFlags

// runtimeLoadUserProfile loads an account profile with PI_NOUI (Rust
// AccountProfile::load_profile).
var runtimeLoadUserProfile = loadRuntimeUserProfile

// runtimeUnloadUserProfile unloads an account profile (Rust
// AccountProfile::unload_profile).
var runtimeUnloadUserProfile = unloadRuntimeUserProfile

func lookupRuntimeSIDString(name string) (string, error) {
	namePtr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return "", err
	}
	var size uint32
	var domainSize uint32
	var use uint32
	err = windows.LookupAccountName(nil, namePtr, nil, &size, nil, &domainSize, &use)
	if err != nil && !errors.Is(err, windows.ERROR_INSUFFICIENT_BUFFER) {
		return "", err
	}
	if size == 0 {
		return "", fmt.Errorf("resolve SID for %s: empty SID buffer", name)
	}
	buffer := make([]byte, size)
	domain := make([]uint16, domainSize)
	if domainSize == 0 {
		domain = nil
	}
	var domainPtr *uint16
	if len(domain) > 0 {
		domainPtr = &domain[0]
	}
	if err := windows.LookupAccountName(
		nil,
		namePtr,
		(*windows.SID)(unsafe.Pointer(&buffer[0])),
		&size,
		domainPtr,
		&domainSize,
		&use,
	); err != nil {
		return "", err
	}
	sid := (*windows.SID)(unsafe.Pointer(&buffer[0]))
	if !sid.IsValid() {
		return "", fmt.Errorf("resolve SID for %s: invalid SID", name)
	}
	return sid.String(), nil
}

func readRuntimeTokenUserSID(token windows.Token) (string, error) {
	user, err := token.GetTokenUser()
	if err != nil {
		return "", err
	}
	if user == nil || user.User.Sid == nil {
		return "", errors.New("token has no user SID")
	}
	return user.User.Sid.String(), nil
}

func readRuntimeTokenGroups(token windows.Token, maxBytes uint32) ([]runtimeGroup, error) {
	var size uint32
	err := windows.GetTokenInformation(token, windows.TokenGroups, nil, 0, &size)
	if err != nil && !errors.Is(err, windows.ERROR_INSUFFICIENT_BUFFER) {
		return nil, err
	}
	if size == 0 {
		return nil, nil
	}
	if maxBytes != 0 && size > maxBytes {
		return nil, fmt.Errorf("token groups exceed %d bytes", maxBytes)
	}
	buffer := make([]byte, size)
	if err := windows.GetTokenInformation(token, windows.TokenGroups, &buffer[0], size, &size); err != nil {
		return nil, err
	}
	return decodeRuntimeTokenGroups(buffer)
}

func decodeRuntimeTokenGroups(buffer []byte) ([]runtimeGroup, error) {
	stride := int(unsafe.Sizeof(windows.SIDAndAttributes{}))
	const headerSize = 8
	if len(buffer) < headerSize || stride <= 0 {
		return nil, errors.New("truncated token group header")
	}
	count := int(*(*uint32)(unsafe.Pointer(&buffer[0])))
	if count < 0 || count > (len(buffer)-headerSize)/stride {
		return nil, errors.New("truncated token groups")
	}
	groups := make([]runtimeGroup, 0, count)
	for index := 0; index < count; index++ {
		entry := *(*windows.SIDAndAttributes)(unsafe.Pointer(&buffer[headerSize+index*stride]))
		if entry.Sid == nil || !entry.Sid.IsValid() {
			return nil, errors.New("invalid token group SID")
		}
		groups = append(groups, runtimeGroup{SID: entry.Sid.String(), Attributes: entry.Attributes})
	}
	return groups, nil
}

func readRuntimeLocalUserFlags(name string) (uint32, bool, error) {
	namePtr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return 0, false, err
	}
	var buffer unsafe.Pointer
	status, _, _ := procRuntimeNetUserGetInfo.Call(
		0,
		uintptr(unsafe.Pointer(namePtr)),
		1,
		uintptr(unsafe.Pointer(&buffer)),
	)
	switch uint32(status) {
	case netapiSuccess:
	case netapiUserNotFound:
		return 0, false, nil
	default:
		return 0, false, fmt.Errorf("read local sandbox user %s: %w", name, hresultError(uint32(status)))
	}
	if buffer == nil {
		return 0, false, fmt.Errorf("read local sandbox user %s: empty user information", name)
	}
	defer procRuntimeNetApiBufferFree.Call(uintptr(buffer))
	flags := (*runtimeUserInfo1)(buffer).Flags
	return flags, true, nil
}

func loadRuntimeUserProfile(token windows.Token, username string) (windows.Handle, error) {
	namePtr, err := windows.UTF16PtrFromString(username)
	if err != nil {
		return 0, err
	}
	profile := runtimeProfileInfo{
		Size:     uint32(unsafe.Sizeof(runtimeProfileInfo{})),
		Flags:    runtimeProfileNoUI,
		UserName: namePtr,
	}
	loaded, _, callErr := procRuntimeLoadUserProfile.Call(uintptr(token), uintptr(unsafe.Pointer(&profile)))
	if loaded == 0 {
		return 0, fmt.Errorf("load managed runtime account profile: %w", callErr)
	}
	return profile.Profile, nil
}

func unloadRuntimeUserProfile(token windows.Token, profile windows.Handle) error {
	unloaded, _, callErr := procRuntimeUnloadUserProfile.Call(uintptr(token), uintptr(profile))
	if unloaded == 0 {
		return fmt.Errorf("unload managed runtime account profile: %w", callErr)
	}
	return nil
}

// RuntimeAccountProfile is one managed sandbox account with its loaded user
// profile. The caller owns it until it unloads or closes it, and it must stay
// loaded while Windows finishes an in-flight package registration (Rust
// AccountProfile).
type RuntimeAccountProfile struct {
	// UserSID is the account SID read from the loaded token.
	UserSID string
	token   windows.Token
	profile windows.Handle
}

// Token exposes the account's logon token so callers can impersonate it.
func (profile *RuntimeAccountProfile) Token() windows.Token {
	if profile == nil {
		return 0
	}
	return profile.token
}

// UnloadProfile unloads the Win32 profile while keeping the logon token.
func (profile *RuntimeAccountProfile) UnloadProfile() error {
	if profile == nil || profile.profile == 0 {
		return nil
	}
	if err := runtimeUnloadUserProfile(profile.token, profile.profile); err != nil {
		return err
	}
	profile.profile = 0
	return nil
}

// Close unloads the profile and closes the logon token.
func (profile *RuntimeAccountProfile) Close() error {
	if profile == nil {
		return nil
	}
	unloadErr := profile.UnloadProfile()
	if profile.token != 0 {
		closeErr := profile.token.Close()
		if unloadErr == nil {
			unloadErr = closeErr
		}
		profile.token = 0
	}
	return unloadErr
}

// LoadRuntimeAccountProfile validates the logon target and loads its profile.
func LoadRuntimeAccountProfile(
	token windows.Token,
	account windowssandbox.SandboxRuntimeAccount,
	expectedSID string,
) (*RuntimeAccountProfile, error) {
	userSID, err := ValidateRuntimeTarget(token, account, expectedSID)
	if err != nil {
		return nil, err
	}
	profile, err := runtimeLoadUserProfile(token, account.Username())
	if err != nil {
		return nil, err
	}
	return &RuntimeAccountProfile{UserSID: userSID, token: token, profile: profile}, nil
}

// ValidateRuntimeTarget refuses a logon whose SID changed, whose account was
// replaced, that is an administrator or that is outside the sandbox group
// (Rust validate_target).
func ValidateRuntimeTarget(
	token windows.Token,
	account windowssandbox.SandboxRuntimeAccount,
	expectedSID string,
) (string, error) {
	userSID, err := runtimeUserSIDString(token)
	if err != nil {
		return "", err
	}
	if expectedSID != "" && expectedSID != userSID {
		return "", errors.New("managed runtime account SID changed")
	}
	resolved, err := runtimeResolveSIDString(account.Username())
	if err != nil {
		return "", err
	}
	if resolved != userSID {
		return "", errors.New("managed runtime account was replaced")
	}
	groups, err := runtimeTokenGroupSIDs(token, runtimeTokenGroupBytes)
	if err != nil {
		return "", fmt.Errorf("read runtime account groups: %w", err)
	}
	if len(groups) > runtimeTokenGroupLimit {
		return "", errors.New("invalid runtime account groups")
	}
	sandboxGroup, err := runtimeResolveSIDString(runtimeSandboxUsersGroup)
	if err != nil {
		return "", err
	}
	sandboxMember := false
	for _, group := range groups {
		if group.SID == runtimeAdministratorsSID {
			return "", errors.New("managed runtime account is an administrator")
		}
		if group.SID == sandboxGroup && group.Attributes&windows.SE_GROUP_ENABLED != 0 {
			sandboxMember = true
		}
	}
	if !sandboxMember {
		return "", errors.New("runtime account is not in CodexSandboxUsers")
	}
	return userSID, nil
}

// ValidateRuntimeRecord rejects a registration record that names the same
// account twice or more accounts than the runtime owns (Rust validate_record).
func ValidateRuntimeRecord(record *windowssandbox.InstallationRecord) error {
	runtime, err := record.RuntimeRegistration()
	if err != nil {
		return err
	}
	if len(runtime.Accounts) > 2 {
		return errors.New("runtime registration record has too many accounts")
	}
	if len(runtime.Accounts) == 2 && runtime.Accounts[0].Account == runtime.Accounts[1].Account {
		return errors.New("runtime registration record contains duplicate accounts")
	}
	return nil
}

// PrepareRuntimeCleanup proves cleanup still owns every recorded account under
// the caller's setup lock, before any logon is obtained (Rust
// prepare_cleanup).
func PrepareRuntimeCleanup(record *windowssandbox.InstallationRecord) (*windowssandbox.InstallationRecord, error) {
	if err := ValidateRuntimeRecord(record); err != nil {
		return nil, err
	}
	runtime, err := record.RuntimeRegistration()
	if err != nil {
		return nil, err
	}
	for _, account := range runtime.Accounts {
		_, local, err := runtimeLocalUserFlags(account.Account.Username())
		if err != nil {
			return nil, err
		}
		if !local {
			continue
		}
		resolved, err := runtimeResolveSIDString(account.Account.Username())
		if err != nil {
			return nil, err
		}
		if resolved != account.UserSID {
			return nil, errors.New("managed runtime account was replaced before cleanup")
		}
	}
	if runtime.Retiring != nil {
		return nil, errors.New("interrupted runtime cleanup requires repair")
	}
	for _, account := range runtime.Accounts {
		if account.CleanupLogonPending {
			return nil, errors.New("cleanup logon recovery is pending")
		}
	}
	return record, nil
}

// runtimeLogonExistingAccount is the seam provision/repair log on through.
var runtimeLogonExistingAccount = windowssandbox.LogonExistingSandboxAccount

// runtimeRegistrationPollInterval and runtimeRegistrationSleep pace the
// terminal-status loop (Rust sleeps 50ms between status reads).
var (
	runtimeRegistrationPollInterval = 50 * time.Millisecond
	runtimeRegistrationSleep        = time.Sleep
)

// ProvisionRegisteredRuntime registers both managed accounts in the current
// package and grants their AppData metadata access (Rust
// registered_runtime::provision). The caller holds the setup mutex from owner
// admission through readiness, and no runner launch performs package
// deployment.
func ProvisionRegisteredRuntime(
	identity *ClientIdentity,
	record *windowssandbox.InstallationRecord,
	fullName string,
	stopped func() bool,
) error {
	if identity == nil {
		return errors.New("missing authenticated provisioning client")
	}
	if record == nil {
		return errors.New("missing installation record")
	}
	if err := ValidateRuntimeRecord(record); err != nil {
		return err
	}
	runtime, err := record.RuntimeRegistration()
	if err != nil {
		return err
	}
	family := runtime.PackageFamily
	if family == "" {
		return errors.New("registered runtime ownership is missing a package family")
	}
	profiles := make([]*RuntimeAccountProfile, 0, 2)
	installed := make([]bool, 0, 2)
	aliasPaths := make([]string, 0, 2)
	accounts := make([]windowssandbox.RuntimeAccountRegistration, 0, 2)
	closed := make([]bool, 0, 2)
	release := true
	defer func() {
		if !release {
			return
		}
		for index, profile := range profiles {
			if profile != nil && !closed[index] {
				_ = profile.Close()
			}
		}
	}()
	for _, account := range []windowssandbox.SandboxRuntimeAccount{
		windowssandbox.SandboxRuntimeAccountOffline,
		windowssandbox.SandboxRuntimeAccountOnline,
	} {
		token, err := logonRegisteredRuntimeAccount(identity, account)
		if err != nil {
			return fmt.Errorf("log on managed runtime account %s: %w", account, err)
		}
		expectedSID := ""
		for _, entry := range runtime.Accounts {
			if entry.Account == account {
				expectedSID = entry.UserSID
				break
			}
		}
		profile, err := LoadRuntimeAccountProfile(token, account, expectedSID)
		if err != nil {
			_ = token.Close()
			return fmt.Errorf("prepare managed runtime profile for %s: %w", account, err)
		}
		profiles = append(profiles, profile)
		closed = append(closed, false)
		packages, err := runtimeRegisteredPackages(profile.UserSID, family)
		if err != nil {
			return fmt.Errorf("query existing runtime packages for %s: %w", account, err)
		}
		if expectedSID == "" && len(packages) != 0 {
			return errors.New("sandbox account has a runtime registration not owned by this service")
		}
		hasPackage := false
		for _, name := range packages {
			if name == fullName {
				hasPackage = true
				break
			}
		}
		aliasPath, err := registeredRunnerAlias(profile.Token(), family)
		if err != nil {
			return fmt.Errorf("resolve runtime alias location for %s: %w", account, err)
		}
		accounts = append(accounts, windowssandbox.RuntimeAccountRegistration{
			Account:   account,
			UserSID:   profile.UserSID,
			AliasPath: &aliasPath,
		})
		aliasPaths = append(aliasPaths, aliasPath)
		installed = append(installed, hasPackage)
	}
	receiptCurrent := runtime.ReadyForPackage(fullName) && sameRuntimeAccounts(runtime.Accounts, accounts)
	if receiptCurrent {
		for _, hasPackage := range installed {
			if !hasPackage {
				receiptCurrent = false
				break
			}
		}
	}
	// Both exact SIDs are durable before either package registration or metadata
	// grant, so an idempotent setup check never revokes a valid launch receipt.
	runtime.Accounts = accounts
	if !receiptCurrent {
		runtime.ReadyPackage = nil
		if err := windowssandbox.SaveInstallation(record); err != nil {
			return fmt.Errorf("persist managed runtime account registration: %w", err)
		}
	}
	for index, profile := range profiles {
		if !installed[index] {
			operation, err := startRuntimePackageRegistration(profile, fullName)
			if err != nil {
				return fmt.Errorf("start managed runtime package registration: %w", err)
			}
			status, err := waitForRuntimeRegistration(operation, stopped)
			if err != nil {
				if errors.Is(err, ErrRuntimeSetupInterrupted) {
					// The typed error bypasses response handling and cleanup: keep the
					// in-flight operation and its profile alive until this own-process
					// service exits.
					operation.Retain()
					release = false
					return ErrRuntimeSetupInterrupted
				}
				operation.Close()
				return err
			}
			result := readRegistrationResult(operation, status)
			operation.Close()
			if err := result.check(func() bool {
				return registeredRuntimeTargetReady(profile, aliasPaths[index], fullName, family)
			}); err != nil {
				return err
			}
			packages, err := runtimeRegisteredPackages(profile.UserSID, family)
			if err != nil {
				return fmt.Errorf("verify managed runtime package registration: %w", err)
			}
			registered := false
			for _, name := range packages {
				if name == fullName {
					registered = true
					break
				}
			}
			if !registered {
				return errors.New("runtime registration completed without the expected package")
			}
		}
		if err := GrantRuntimeMetadata(identity.Token, profile.UserSID, record); err != nil {
			return fmt.Errorf("grant managed runtime metadata permissions: %w", err)
		}
		if err := profile.UnloadProfile(); err != nil {
			return err
		}
		if err := profile.Close(); err != nil {
			return err
		}
		closed[index] = true
	}
	if !receiptCurrent {
		ready := fullName
		runtime.ReadyPackage = &ready
		if err := windowssandbox.SaveInstallation(record); err != nil {
			return fmt.Errorf("persist completed managed runtime registration: %w", err)
		}
	}
	return nil
}

// logonRegisteredRuntimeAccount logs on one managed account as the pinned owner.
func logonRegisteredRuntimeAccount(identity *ClientIdentity, account windowssandbox.SandboxRuntimeAccount) (windows.Token, error) {
	var token windows.Token
	err := withOwnerImpersonation(identity.Token, func() error {
		var pins []windows.Handle
		if err := pinExistingAncestors(windowssandbox.SandboxSecretsDir(identity.CodexHome), &pins); err != nil {
			return err
		}
		defer closeHandles(pins)
		loggedOn, err := runtimeLogonExistingAccount(identity.CodexHome, account)
		token = loggedOn
		return err
	})
	if err != nil {
		return 0, err
	}
	return token, nil
}

// registeredRuntimeTargetReady reports whether a registration that failed with
// the packaged-service privilege error still satisfies its postcondition: the
// execution alias exists as a reparse point when queried as the target user and
// the exact package is registered for the target SID (Rust #60944).
func registeredRuntimeTargetReady(profile *RuntimeAccountProfile, aliasPath string, fullName string, family string) bool {
	if profile == nil {
		return false
	}
	aliasPresent, err := runtimeAliasIsReparsePoint(profile.Token(), aliasPath)
	if err != nil || !aliasPresent {
		return false
	}
	packages, err := runtimeRegisteredPackages(profile.UserSID, family)
	if err != nil {
		return false
	}
	for _, name := range packages {
		if name == fullName {
			return true
		}
	}
	return false
}

// runtimeAliasIsReparsePoint inspects the alias path as the managed account,
// without following the reparse point (Rust symlink_metadata).
func runtimeAliasIsReparsePoint(profileToken windows.Token, aliasPath string) (bool, error) {
	if strings.TrimSpace(aliasPath) == "" {
		return false, nil
	}
	var present bool
	err := withOwnerImpersonation(profileToken, func() error {
		info, err := os.Lstat(aliasPath)
		if err != nil {
			return err
		}
		data, ok := info.Sys().(*syscall.Win32FileAttributeData)
		if !ok {
			return nil
		}
		present = data.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0
		return nil
	})
	return present, err
}

// registeredRunnerAlias is the package-scoped alias path the receipt records.
func registeredRunnerAlias(profileToken windows.Token, family string) (string, error) {
	localAppData, err := runtimeKnownFolderPath(profileToken, &knownFolderLocalAppData, 0)
	if err != nil {
		return "", err
	}
	return filepath.Join(localAppData, "Microsoft", "WindowsApps", family, windowssandbox.AppCoreRunnerAlias), nil
}

// startRuntimePackageRegistration deploys the package as the managed account.
func startRuntimePackageRegistration(profile *RuntimeAccountProfile, fullName string) (*AsyncOperation, error) {
	var operation *AsyncOperation
	err := withOwnerImpersonation(profile.Token(), func() error {
		manager, err := OpenPackageManager()
		if err != nil {
			return err
		}
		defer manager.Close()
		started, err := manager.RegisterPackageByFullNameAsync(fullName)
		operation = started
		return err
	})
	if err != nil {
		return nil, err
	}
	return operation, nil
}

// waitForRuntimeRegistration polls until the deployment reaches a terminal
// status, reporting an unreadable status once and honoring service shutdown.
func waitForRuntimeRegistration(operation *AsyncOperation, stopped func() bool) (int32, error) {
	statusErrorLogged := false
	for {
		if stopped != nil && stopped() {
			return 0, ErrRuntimeSetupInterrupted
		}
		status, err := operation.Status()
		switch {
		case err != nil:
			if !statusErrorLogged {
				LogError(
					EventServiceFailed,
					"runtime registration status unavailable; retaining request: "+ErrorMessage(err),
				)
				statusErrorLogged = true
			}
		case status == asyncStatusStarted:
		case status == asyncStatusCompleted, status == asyncStatusCanceled, status == asyncStatusError:
			return status, nil
		}
		runtimeRegistrationSleep(runtimeRegistrationPollInterval)
	}
}

// sameRuntimeAccounts reports an identical recorded account set.
func sameRuntimeAccounts(recorded []windowssandbox.RuntimeAccountRegistration, current []windowssandbox.RuntimeAccountRegistration) bool {
	if len(recorded) != len(current) {
		return false
	}
	for index := range recorded {
		left := recorded[index]
		right := current[index]
		if left.Account != right.Account || left.UserSID != right.UserSID || left.CleanupLogonPending != right.CleanupLogonPending {
			return false
		}
		if (left.AliasPath == nil) != (right.AliasPath == nil) {
			return false
		}
		if left.AliasPath != nil && *left.AliasPath != *right.AliasPath {
			return false
		}
	}
	return true
}

// CredentialsNeedRepair mirrors Rust provisioning/registered.rs::credentials_need_repair:
// a credential mismatch on either account requires a full setup only after the
// recorded account identities still prove ownership. Every non-credential error
// is returned unchanged and never rotates passwords.
func CredentialsNeedRepair(
	record *windowssandbox.InstallationRecord,
	logon func(windowssandbox.SandboxRuntimeAccount) (windows.Token, error),
) (bool, error) {
	if err := ValidateRuntimeRecord(record); err != nil {
		return false, err
	}
	runtime, err := record.RuntimeRegistration()
	if err != nil {
		return false, err
	}
	needsRepair := false
	for _, account := range []windowssandbox.SandboxRuntimeAccount{
		windowssandbox.SandboxRuntimeAccountOffline,
		windowssandbox.SandboxRuntimeAccountOnline,
	} {
		expectedSID := ""
		for _, entry := range runtime.Accounts {
			if entry.Account == account {
				expectedSID = entry.UserSID
				break
			}
		}
		token, err := logon(account)
		if err != nil {
			var mismatch *windowssandbox.SandboxAccountCredentialMismatch
			if errors.As(err, &mismatch) {
				needsRepair = true
				continue
			}
			return false, err
		}
		_, validateErr := ValidateRuntimeTarget(token, account, expectedSID)
		_ = token.Close()
		if validateErr != nil {
			return false, validateErr
		}
	}
	if !needsRepair {
		return false, nil
	}
	// Never reset recorded replacement accounts or unrecorded runtime
	// registrations. An initial migration has no recorded account SIDs yet.
	for _, account := range []windowssandbox.SandboxRuntimeAccount{
		windowssandbox.SandboxRuntimeAccountOffline,
		windowssandbox.SandboxRuntimeAccountOnline,
	} {
		sid, err := runtimeResolveSIDString(account.Username())
		if err != nil {
			return false, err
		}
		recorded := ""
		for _, entry := range runtime.Accounts {
			if entry.Account == account {
				recorded = entry.UserSID
				break
			}
		}
		if recorded != "" {
			if sid != recorded {
				return false, errors.New("managed runtime account was replaced")
			}
			continue
		}
		packages, err := runtimeRegisteredPackages(sid, runtime.PackageFamily)
		if err != nil {
			return false, err
		}
		if len(packages) != 0 {
			return false, errors.New("sandbox account has a runtime registration not owned by this service")
		}
	}
	return true, nil
}
