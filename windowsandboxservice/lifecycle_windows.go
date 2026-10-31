//go:build windows

package windowsandboxservice

import (
	"errors"
	"fmt"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"

	"codex_go/sandbox/windowssandbox"
	"codex_go/sandbox/windowssandbox/provisionservice"
)

// Lifecycle owns the authenticated owner of the recorded sandbox: the saved
// installation record, the owner token and the pinned ancestor/home handles that
// keep uninstall cleanup bound to those exact directories (Rust
// package_lifecycle::PackageLifecycle).
type Lifecycle struct {
	mu           sync.Mutex
	installation *lifecycleInstallation
	loadRecord   func() (*windowssandbox.InstallationRecord, error)
	saveRecord   func(*windowssandbox.InstallationRecord) (*windowssandbox.InstallationRecord, error)
}

type lifecycleInstallation struct {
	record    *windowssandbox.InstallationRecord
	codexHome string
	token     windows.Token
	handles   []windows.Handle
}

// NewLifecycle returns a lifecycle bound to the real installation store.
func NewLifecycle() *Lifecycle {
	return &Lifecycle{
		loadRecord: windowssandbox.LoadInstallation,
		saveRecord: windowssandbox.SaveInstallationRecord,
	}
}

// RegisterOwner ports PackageLifecycle::register_authenticated_user: previous
// ownership must match, desktop ownership is merged, the record is saved under
// the setup lock, and the first registered owner pins its home and retains the
// token. A home that cannot be pinned is logged and left unpinned so cleanup
// skips file removal instead of acting on an unpinned path.
func (lifecycle *Lifecycle) RegisterOwner(record *windowssandbox.InstallationRecord, token windows.Token, _ windowssandbox.SetupRuntime) (*windowssandbox.InstallationRecord, error) {
	if lifecycle == nil {
		return nil, errors.New("missing sandbox lifecycle")
	}
	if record == nil {
		return nil, errors.New("missing installation record")
	}
	if token == 0 {
		return nil, errors.New("missing authenticated owner token")
	}
	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()

	var previous *windowssandbox.InstallationRecord
	if lifecycle.installation != nil {
		previous = lifecycle.installation.record
	} else {
		loaded, err := lifecycle.loadRecord()
		if err != nil {
			return nil, err
		}
		previous = loaded
	}
	if previous != nil {
		// A missing watcher does not retire its owner; other clients can still use
		// elevated setup.
		if previous.UserSID != record.UserSID || previous.CodexHome != record.CodexHome {
			return nil, unavailable("installation is already registered to a different owner or home")
		}
		if previous.DesktopInstallation != nil {
			record.DesktopInstallation = previous.DesktopInstallation
		}
	}
	saved, err := lifecycle.saveRecord(record)
	if err != nil {
		return nil, err
	}
	if lifecycle.installation != nil && lifecycle.installation.codexHome != "" {
		// A restored owner must immediately use newly registered desktop ownership.
		lifecycle.installation.record = saved
		if token != lifecycle.installation.token {
			_ = token.Close()
		}
		return saved, nil
	}

	codexHome, handles, err := pinOwnerHome(record.CodexHome, token)
	if err != nil {
		closeHandles(handles)
		LogError(EventServiceFailed, "skipping sandbox file cleanup because the home could not be pinned: "+ErrorMessage(err))
		codexHome, handles = "", nil
	}
	lifecycle.installation = &lifecycleInstallation{
		record:    saved,
		codexHome: codexHome,
		token:     token,
		handles:   handles,
	}
	return saved, nil
}

// ServeProvisioningRequest provisions one authenticated request with this
// lifecycle as the uninstall owner registrar (Rust service::handle_request).
func (lifecycle *Lifecycle) ServeProvisioningRequest(
	identity *ClientIdentity,
	request *ServiceRequest,
	sandboxSID []byte,
	stopped func() bool,
	policy PolicyValidator,
	setup SetupRunner,
) (provisionservice.Response, error) {
	if err := validateProvisioningPolicy(identity, request, policy); err != nil {
		return provisionservice.Response{}, err
	}
	return RunProvisioning(identity, request, sandboxSID, stopped, lifecycle.RegisterOwner, setup)
}

// querySessionUserToken opens the logged-in user token of one session
// (Rust WTSQueryUserToken).
var querySessionUserToken = func(sessionID uint32) (windows.Token, error) {
	var token windows.Token
	if err := windows.WTSQueryUserToken(sessionID, &token); err != nil {
		return 0, err
	}
	return token, nil
}

// listWindowsSessions enumerates current sessions so an owner whose session ID
// changed while the service was stopped can still be restored (Rust
// WTSEnumerateSessionsW).
var listWindowsSessions = func() ([]uint32, error) {
	var sessions *windows.WTS_SESSION_INFO
	var count uint32
	if err := windows.WTSEnumerateSessions(0, 0, 1, &sessions, &count); err != nil {
		return nil, err
	}
	defer windows.WTSFreeMemory(uintptr(unsafe.Pointer(sessions)))
	entries := unsafe.Slice(sessions, int(count))
	sessionIDs := make([]uint32, 0, len(entries))
	for _, entry := range entries {
		sessionIDs = append(sessionIDs, entry.SessionID)
	}
	return sessionIDs, nil
}

// RestoreAuthenticatedUser ports PackageLifecycle::restore_authenticated_user:
// a retained installation needs no work, a record owned by another package
// family is skipped, and the session user must be the recorded owner before its
// token is registered and its home pinned.
func (lifecycle *Lifecycle) RestoreAuthenticatedUser(sessionID uint32) error {
	if lifecycle == nil {
		return errors.New("missing sandbox lifecycle")
	}
	lifecycle.mu.Lock()
	retained := lifecycle.installation != nil
	lifecycle.mu.Unlock()
	if retained {
		return nil
	}
	record, err := lifecycle.loadRecord()
	if err != nil || record == nil {
		return err
	}
	if record.Runtime != nil {
		current, err := windowssandbox.InstallationIsCurrentPackage(record)
		if err != nil {
			return err
		}
		if !current {
			return nil
		}
	}
	token, err := querySessionUserToken(sessionID)
	if err != nil {
		return fmt.Errorf("open the logged-in user token: %w", err)
	}
	userSID, err := tokenUserSIDBytes(token)
	if err != nil {
		_ = token.Close()
		return err
	}
	if windowssandbox.StringFromSIDBytes(userSID) != record.UserSID {
		_ = token.Close()
		return errors.New("logged-in user does not match the recorded sandbox owner")
	}
	restored := *record
	restored.SessionID = sessionID
	if _, err := lifecycle.RegisterOwner(&restored, token, windowssandbox.SetupRuntimeLegacy); err != nil {
		_ = token.Close()
		return err
	}
	return nil
}

// RestoreRecordedOwner ports PackageLifecycle::restore_logged_in_owner: retry
// the recorded session, then any other session whose user matches the recorded
// owner SID, because session IDs can change while the service is stopped.
func (lifecycle *Lifecycle) RestoreRecordedOwner() error {
	if lifecycle == nil {
		return errors.New("missing sandbox lifecycle")
	}
	record, err := lifecycle.loadRecord()
	if err != nil || record == nil {
		return err
	}
	recordedErr := lifecycle.RestoreAuthenticatedUser(record.SessionID)
	if recordedErr == nil {
		return nil
	}
	sessionIDs, err := listWindowsSessions()
	if err != nil {
		return fmt.Errorf("list current Windows sessions: %w", err)
	}
	for _, sessionID := range sessionIDs {
		if sessionID == record.SessionID {
			continue
		}
		if lifecycle.RestoreAuthenticatedUser(sessionID) == nil {
			return nil
		}
	}
	return recordedErr
}

// CodexHome reports the home retained for uninstall cleanup, or "" when the
// owner's home could not be pinned.
func (lifecycle *Lifecycle) CodexHome() string {
	if lifecycle == nil {
		return ""
	}
	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()
	if lifecycle.installation == nil {
		return ""
	}
	return lifecycle.installation.codexHome
}

// Close releases the retained pins and the owner token.
func (lifecycle *Lifecycle) Close() {
	if lifecycle == nil {
		return
	}
	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()
	if lifecycle.installation == nil {
		return
	}
	closeHandles(lifecycle.installation.handles)
	if lifecycle.installation.token != 0 {
		_ = lifecycle.installation.token.Close()
	}
	lifecycle.installation = nil
}

// pinOwnerHome pins every ancestor up to and including home as the owner, then
// guards the home itself so it cannot be converted into a reparse point before
// cleanup (Rust register_authenticated_user).
func pinOwnerHome(home string, token windows.Token) (string, []windows.Handle, error) {
	var handles []windows.Handle
	err := withOwnerImpersonation(token, func() error {
		if err := pinExistingAncestors(home, &handles); err != nil {
			return err
		}
		if len(handles) == 0 {
			return errors.New("pin the registered home: no directory was pinned")
		}
		guard, err := windowssandbox.CreateDirectoryGuard(handles[len(handles)-1])
		if err != nil {
			return err
		}
		// Reject a conversion that happened before the handle-relative guard existed.
		probe, err := pinDirectory(home, windows.FILE_READ_ATTRIBUTES, windowssandbox.DirectoryOpenExisting)
		if err != nil {
			_ = windows.CloseHandle(guard)
			return err
		}
		_ = windows.CloseHandle(probe)
		handles = append(handles, guard)
		return nil
	})
	if err != nil {
		return "", handles, err
	}
	return home, handles, nil
}
