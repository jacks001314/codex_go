//go:build windows

package windowsandboxservice

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"

	"codex_go/sandbox/windowssandbox"
	"codex_go/sandbox/windowssandbox/provisionservice"
)

const (
	desktopInstallationMarker = ".desktop-created"
	fileNameNormalized        = 0x0000_0000
	volumeNameDOS             = 0x0000_0000
	processImageMaxUnits      = 32768
)

var (
	procImpersonateNamedPipeClient = advapi32.NewProc("ImpersonateNamedPipeClient")

	packageFamilyFor = provisionservice.ProcessPackageFamily
	executablePath   = os.Executable
	evalSymlinks     = filepath.EvalSymlinks

	loadRegisteredRuntime = func() (*windowssandbox.InstallationRecord, error) {
		record, err := windowssandbox.LoadInstallation()
		if err != nil || record == nil || record.Runtime == nil {
			return nil, err
		}
		return record, nil
	}
)

// AuthorizedClientProcess is a client that passed package-identity admission
// before any request byte was dispatched (Rust AuthorizedClientProcess).
type AuthorizedClientProcess struct {
	handle windows.Handle
}

// Handle returns the borrowed process handle.
func (process *AuthorizedClientProcess) Handle() windows.Handle {
	if process == nil {
		return 0
	}
	return process.handle
}

// Close releases the process handle.
func (process *AuthorizedClientProcess) Close() {
	if process == nil || process.handle == 0 {
		return
	}
	_ = windows.CloseHandle(process.handle)
	process.handle = 0
}

// AuthorizeClientProcess binds the connected pipe instance to the packaged
// Codex client that opened it (Rust authorize_client_process).
func AuthorizeClientProcess(pipe windows.Handle) (*AuthorizedClientProcess, error) {
	var processID uint32
	if err := windows.GetNamedPipeClientProcessId(pipe, &processID); err != nil {
		return nil, fmt.Errorf("identify the provisioning client process: %w", err)
	}
	if processID == 0 {
		return nil, errors.New("identify the provisioning client process: no client is connected")
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, processID)
	if err != nil {
		return nil, fmt.Errorf("open the provisioning client process: %w", err)
	}
	process := &AuthorizedClientProcess{handle: handle}
	clientFamily, clientPresent, err := packageFamilyFor(handle)
	if err != nil {
		process.Close()
		return nil, fmt.Errorf("read provisioning client package identity: %w", err)
	}
	serviceFamily, servicePresent, err := packageFamilyFor(windows.CurrentProcess())
	if err != nil {
		process.Close()
		return nil, fmt.Errorf("read provisioning service package identity: %w", err)
	}
	switch {
	case clientPresent && servicePresent && clientFamily != serviceFamily:
		process.Close()
		return nil, errors.New("provisioning client package does not match the service package")
	case clientPresent && servicePresent:
	case !clientPresent:
		process.Close()
		return nil, errors.New("provisioning clients must run with an installed Codex package identity")
	default:
		process.Close()
		return nil, errors.New("provisioning service has no package identity")
	}
	return process, nil
}

// AuthorizeClient refuses a pipe client whose impersonated user is not the user
// that owns the authorized client process (Rust authorize_client).
func AuthorizeClient(process *AuthorizedClientProcess, clientToken windows.Token) error {
	if process == nil || process.handle == 0 {
		return errors.New("missing authorized provisioning client")
	}
	var processToken windows.Token
	if err := windows.OpenProcessToken(process.handle, windows.TOKEN_QUERY, &processToken); err != nil {
		return fmt.Errorf("open the provisioning client process token: %w", err)
	}
	defer processToken.Close()
	processUser, err := tokenUserSIDBytes(processToken)
	if err != nil {
		return fmt.Errorf("read the client process user: %w", err)
	}
	impersonatedUser, err := tokenUserSIDBytes(clientToken)
	if err != nil {
		return fmt.Errorf("read the impersonated client user: %w", err)
	}
	if !bytes.Equal(processUser, impersonatedUser) {
		return errors.New("provisioning client process does not belong to the impersonated user")
	}
	return nil
}

// AuthorizeSetupRuntime admits registered Core only from the service's installed
// package version and prevents legacy setup from replacing accounts owned by a
// registered runtime (Rust package_identity::registered).
func AuthorizeSetupRuntime(process *AuthorizedClientProcess, request *ServiceRequest) (windowssandbox.SetupRuntime, error) {
	if request == nil {
		return "", errors.New("missing provisioning request")
	}
	if request.Provision != nil && request.Provision.RegisteredCore {
		if err := authorizeRuntimeClientProcess(process); err != nil {
			return "", err
		}
		return windowssandbox.SetupRuntimeRegistered, nil
	}
	if request.Register != nil {
		// Registration does not provision or replace sandbox accounts.
		return windowssandbox.SetupRuntimeLegacy, nil
	}
	registered, err := loadRegisteredRuntime()
	if err != nil {
		return "", err
	}
	if registered != nil {
		return "", errors.New("registered Core owns these sandbox accounts; legacy provisioning is not permitted")
	}
	return windowssandbox.SetupRuntimeLegacy, nil
}

func authorizeRuntimeClientProcess(process *AuthorizedClientProcess) error {
	if process == nil || process.handle == 0 {
		return errors.New("missing authorized provisioning client")
	}
	clientFamily, clientPresent, err := packageFamilyFor(process.handle)
	if err != nil {
		return fmt.Errorf("read runtime registration client package family: %w", err)
	}
	serviceFamily, servicePresent, err := packageFamilyFor(windows.CurrentProcess())
	if err != nil {
		return fmt.Errorf("read runtime registration service package family: %w", err)
	}
	if err := RequireRuntimePackageFamily(optionalString(clientFamily, clientPresent), optionalString(serviceFamily, servicePresent)); err != nil {
		return err
	}
	image, err := processImagePath(process.handle)
	if err != nil {
		return err
	}
	canonicalImage, err := evalSymlinks(image)
	if err != nil {
		return fmt.Errorf("canonicalize provisioning client image: %w", err)
	}
	service, err := executablePath()
	if err != nil {
		return fmt.Errorf("read the service image: %w", err)
	}
	canonicalService, err := evalSymlinks(service)
	if err != nil {
		return fmt.Errorf("canonicalize the service image: %w", err)
	}
	if !isInstalledServiceImage(canonicalImage, filepath.Dir(canonicalService)) {
		return errors.New("registered Core caller is not from this installed service version")
	}
	return nil
}

// ClientIdentity is the authenticated requesting user captured before policy
// validation and provisioning (Rust ipc::authentication::ClientIdentity).
type ClientIdentity struct {
	Account             string
	CodexHome           string
	UserSID             string
	SessionID           uint32
	Runtime             windowssandbox.SetupRuntime
	Token               windows.Token
	DesktopInstallation *windowssandbox.DesktopInstallation
	DirectoryHandles    []windows.Handle
}

// Close releases the retained access token and directory pins.
func (identity *ClientIdentity) Close() {
	if identity == nil {
		return
	}
	if identity.Token != 0 {
		_ = identity.Token.Close()
		identity.Token = 0
	}
	closeHandles(identity.DirectoryHandles)
	identity.DirectoryHandles = nil
}

// AuthenticateClient impersonates the connected client, binds the impersonated
// token to the authorized process and captures the owner, sandbox generation and
// pinned Codex home used by the provisioning worker (Rust authenticate_client).
// The impersonation is reverted before returning; later file work must act
// through identity.Token explicitly.
func AuthenticateClient(pipe windows.Handle, process *AuthorizedClientProcess, sandboxSID []byte, request *ServiceRequest) (*ClientIdentity, error) {
	if process == nil || process.handle == 0 {
		return nil, errors.New("missing authorized provisioning client")
	}
	if request == nil {
		return nil, errors.New("missing provisioning request")
	}
	if len(sandboxSID) == 0 {
		return nil, errors.New("missing sandbox group SID")
	}
	// Impersonation is per-thread on Windows; keep this goroutine on one thread
	// until it is reverted.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	lock, err := windowssandbox.AcquireSandboxSetupLock(pipeSetupLockTimeoutMillis)
	if err != nil {
		return nil, err
	}
	current, resolveErr := resolveSandboxSID()
	lock.Release()
	if resolveErr != nil || !bytes.Equal(current, sandboxSID) {
		return nil, errors.New(provisionservice.GroupChanged)
	}
	impersonated, _, callErr := procImpersonateNamedPipeClient.Call(uintptr(pipe))
	if impersonated == 0 {
		return nil, fmt.Errorf("impersonate provisioning client: %w", callErr)
	}
	defer func() { _ = windows.RevertToSelf() }()

	var token windows.Token
	if err := windows.OpenThreadToken(windows.CurrentThread(), windows.TOKEN_QUERY|windows.TOKEN_IMPERSONATE, true, &token); err != nil {
		return nil, fmt.Errorf("open provisioning client token: %w", err)
	}
	retainToken := false
	defer func() {
		if !retainToken {
			_ = token.Close()
		}
	}()
	if err := AuthorizeClient(process, token); err != nil {
		return nil, fmt.Errorf("authorize the packaged Codex provisioning client: %w", err)
	}
	session, err := tokenSessionID(token)
	if err != nil {
		return nil, err
	}
	if session == 0 {
		return nil, errors.New("non-interactive service accounts cannot request provisioning")
	}
	isSandboxMember, err := token.IsMember((*windows.SID)(unsafe.Pointer(&sandboxSID[0])))
	if err != nil {
		return nil, fmt.Errorf("check sandbox group membership: %w", err)
	}
	if isSandboxMember {
		return nil, errors.New("sandbox accounts cannot request provisioning")
	}
	userSIDBytes, err := tokenUserSIDBytes(token)
	if err != nil {
		return nil, fmt.Errorf("read provisioning client identity: %w", err)
	}
	userSID := windowssandbox.StringFromSIDBytes(userSIDBytes)
	if userSID == "" {
		return nil, errors.New("read provisioning client identity: the user SID is malformed")
	}
	account, err := accountNameFromSIDBytes(userSIDBytes)
	if err != nil {
		return nil, err
	}
	setupRuntime, err := AuthorizeSetupRuntime(process, request)
	if err != nil {
		return nil, err
	}
	requestedHome := requestedCodexHome(request)
	var (
		codexHome string
		handles   []windows.Handle
	)
	if request.Register != nil {
		codexHome, handles, err = registerDesktopHome(requestedHome)
	} else {
		disposition := windowssandbox.DirectoryOpenOrCreate
		if request.Provision.RefreshOnly {
			disposition = windowssandbox.DirectoryOpenExisting
		}
		codexHome, handles, err = prepareCodexHome(
			requestedHome,
			setupRuntime == windowssandbox.SetupRuntimeRegistered,
			disposition,
		)
	}
	if err != nil {
		closeHandles(handles)
		return nil, err
	}
	// A missing profile or marker only weakens uninstall ownership; Rust logs the
	// failure and preserves the desktop directories.
	desktopInstallation, desktopErr := readDesktopInstallation(codexHome, token)
	if desktopErr != nil {
		desktopInstallation = nil
	}
	retainToken = true
	return &ClientIdentity{
		Account:             account,
		CodexHome:           codexHome,
		UserSID:             userSID,
		SessionID:           session,
		Runtime:             setupRuntime,
		Token:               token,
		DesktopInstallation: desktopInstallation,
		DirectoryHandles:    handles,
	}, nil
}

// registerDesktopHome pins an existing desktop Codex home for uninstall
// ownership without creating sandbox directories or changing their ACLs.
func registerDesktopHome(requested string) (string, []windows.Handle, error) {
	if err := windowssandbox.ValidateLocalDirectoryPath(requested); err != nil {
		return "", nil, err
	}
	var handles []windows.Handle
	if err := pinExistingAncestors(requested, &handles); err != nil {
		closeHandles(handles)
		return "", nil, err
	}
	// Uninstall removes sandbox files as SYSTEM; read access cannot grant that
	// authority.
	home, err := pinDirectory(
		requested,
		fileAddFile|fileAddSubdirectory|windows.WRITE_DAC,
		windowssandbox.DirectoryOpenExisting,
	)
	if err != nil {
		closeHandles(handles)
		return "", nil, err
	}
	// Bind the guard to the authorized directory before resolving its pathname.
	guard, err := windowssandbox.CreateDirectoryGuard(home)
	if err != nil {
		_ = windows.CloseHandle(home)
		closeHandles(handles)
		return "", nil, err
	}
	probe, err := pinDirectory(requested, windows.FILE_READ_ATTRIBUTES, windowssandbox.DirectoryOpenExisting)
	if err != nil {
		_ = windows.CloseHandle(guard)
		_ = windows.CloseHandle(home)
		closeHandles(handles)
		return "", nil, err
	}
	_ = windows.CloseHandle(probe)
	// Preserve the authorized directory's literal name, including trailing dots.
	path, err := finalDirectoryPath(home)
	if err != nil {
		_ = windows.CloseHandle(guard)
		_ = windows.CloseHandle(home)
		closeHandles(handles)
		return "", nil, err
	}
	handles = append(handles, home, guard)
	return path, handles, nil
}

// readDesktopInstallation records whether the desktop created the Codex home and
// the owner's cache root (Rust installation_record::read_desktop_installation).
func readDesktopInstallation(home string, token windows.Token) (*windowssandbox.DesktopInstallation, error) {
	profile, err := userProfileDirectory(token)
	if err != nil {
		return nil, err
	}
	cacheHome := filepath.Join(profile, ".cache")
	if err := windowssandbox.ValidateLocalDirectoryPath(cacheHome); err != nil {
		return nil, err
	}
	info, statErr := os.Stat(filepath.Join(home, desktopInstallationMarker))
	created := statErr == nil && info.Mode().IsRegular()
	return &windowssandbox.DesktopInstallation{CreatedCodexHome: created, CacheHome: cacheHome}, nil
}

func requestedCodexHome(request *ServiceRequest) string {
	if request.Register != nil {
		return request.Register.CodexHome
	}
	if request.Provision != nil {
		return request.Provision.CodexHome
	}
	return ""
}

func tokenUserSIDBytes(token windows.Token) ([]byte, error) {
	user, err := token.GetTokenUser()
	if err != nil {
		return nil, err
	}
	sid := user.User.Sid
	if sid == nil || !sid.IsValid() {
		return nil, errors.New("the token user SID is invalid")
	}
	length := sid.Len()
	data := make([]byte, length)
	copy(data, unsafe.Slice((*byte)(unsafe.Pointer(sid)), length))
	return data, nil
}

func accountNameFromSIDBytes(sidBytes []byte) (string, error) {
	if len(sidBytes) == 0 {
		return "", errors.New("SID is not a named Windows user")
	}
	sid := (*windows.SID)(unsafe.Pointer(&sidBytes[0]))
	name := make([]uint16, 256)
	domain := make([]uint16, 256)
	nameLength := uint32(len(name))
	domainLength := uint32(len(domain))
	var accountType uint32
	if err := windows.LookupAccountSid(nil, sid, &name[0], &nameLength, &domain[0], &domainLength, &accountType); err != nil {
		return "", fmt.Errorf("resolve provisioning client account: %w", err)
	}
	if accountType != windows.SidTypeUser || domainLength == 0 {
		return "", errors.New("SID is not a named Windows user")
	}
	return windows.UTF16ToString(domain[:domainLength]) + "\\" + windows.UTF16ToString(name[:nameLength]), nil
}

func tokenSessionID(token windows.Token) (uint32, error) {
	var session uint32
	var returned uint32
	if err := windows.GetTokenInformation(
		token,
		windows.TokenSessionId,
		(*byte)(unsafe.Pointer(&session)),
		uint32(unsafe.Sizeof(session)),
		&returned,
	); err != nil {
		return 0, fmt.Errorf("read provisioning client session: %w", err)
	}
	return session, nil
}

func processImagePath(process windows.Handle) (string, error) {
	buffer := make([]uint16, processImageMaxUnits)
	size := uint32(len(buffer))
	if err := windows.QueryFullProcessImageName(process, 0, &buffer[0], &size); err != nil {
		return "", fmt.Errorf("read provisioning client image: %w", err)
	}
	return windows.UTF16ToString(buffer[:size]), nil
}

func userProfileDirectory(token windows.Token) (string, error) {
	size := uint32(1024)
	for {
		buffer := make([]uint16, size)
		err := windows.GetUserProfileDirectory(token, &buffer[0], &size)
		if err == nil {
			return windows.UTF16ToString(buffer[:size]), nil
		}
		if !errors.Is(err, windows.ERROR_INSUFFICIENT_BUFFER) || size <= uint32(len(buffer)) {
			return "", fmt.Errorf("read sandbox owner's profile directory: %w", err)
		}
	}
}

func finalDirectoryPath(directory windows.Handle) (string, error) {
	buffer := make([]uint16, 260)
	for {
		length, err := windows.GetFinalPathNameByHandle(
			directory,
			&buffer[0],
			uint32(len(buffer)),
			fileNameNormalized|volumeNameDOS,
		)
		if err != nil {
			return "", fmt.Errorf("resolve authorized desktop home: %w", err)
		}
		if int(length) < len(buffer) {
			return windows.UTF16ToString(buffer[:length]), nil
		}
		buffer = make([]uint16, length)
	}
}

func optionalString(value string, present bool) *string {
	if !present {
		return nil
	}
	return &value
}
