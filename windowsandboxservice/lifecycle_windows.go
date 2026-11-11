//go:build windows

package windowsandboxservice

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
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
	uninstalling atomic.Bool
	preinstalled bool
	packageName  string
	loadRecord   func() (*windowssandbox.InstallationRecord, error)
	saveRecord   func(*windowssandbox.InstallationRecord) (*windowssandbox.InstallationRecord, error)
	subscribe    func(packageName, ownerSID string, uninstalling *atomic.Bool, token windows.Token) (*packageUninstallSubscription, error)
}

type lifecycleInstallation struct {
	record       *windowssandbox.InstallationRecord
	codexHome    string
	token        windows.Token
	handles      []windows.Handle
	guard        windows.Handle
	subscription *packageUninstallSubscription
}

// lifecyclePackageName is the seam the lifecycle captures its own package full
// name through (Rust Package::Current().Id().FullName()).
var lifecyclePackageName = func() (string, error) {
	fullName, present, err := windowssandbox.CurrentProcessPackageFullName()
	if err != nil {
		return "", err
	}
	if !present {
		return "", errors.New("the sandbox service has no package identity")
	}
	return fullName, nil
}

// NewLifecycle returns a lifecycle bound to the running package and the real
// installation store. A service without package identity cannot observe its own
// uninstall, so construction fails instead of running blind (Rust
// PackageLifecycle::new).
func NewLifecycle() (*Lifecycle, error) {
	packageName, err := lifecyclePackageName()
	if err != nil {
		return nil, fmt.Errorf("read the current package identity: %w", err)
	}
	return &Lifecycle{
		packageName:  packageName,
		preinstalled: lifecyclePreinstalled(),
		loadRecord:   windowssandbox.LoadInstallation,
		saveRecord:   windowssandbox.SaveInstallationRecord,
		subscribe:    subscribePackageUninstalling,
	}, nil
}

// Uninstalling exposes the pending-uninstall flag the package uninstall event
// and the stop reason share (Rust PackageLifecycle::uninstalling).
func (lifecycle *Lifecycle) Uninstalling() *atomic.Bool {
	if lifecycle == nil {
		return nil
	}
	return &lifecycle.uninstalling
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

	codexHome, handles, guard, err := pinOwnerHome(record.CodexHome, token)
	if err != nil {
		closeHandles(handles)
		LogError(EventServiceFailed, "skipping sandbox file cleanup because the home could not be pinned: "+ErrorMessage(err))
		codexHome, handles, guard = "", nil, 0
	}
	// A first owner must be observable: the service retires only once the
	// owner's package is gone, so an unwatched installation is refused rather
	// than cleaned up blind.
	subscription, err := lifecycle.subscribeUninstalls(record.UserSID, token)
	if err != nil {
		closeHandles(handles)
		if guard != 0 {
			_ = windows.CloseHandle(guard)
		}
		return nil, err
	}
	lifecycle.installation = &lifecycleInstallation{
		record:       saved,
		codexHome:    codexHome,
		token:        token,
		handles:      handles,
		guard:        guard,
		subscription: subscription,
	}
	return saved, nil
}

// subscribeUninstalls opens the owner's package uninstall notification stream
// (Rust PackageCatalog::OpenForCurrentUser plus PackageUninstalling).
func (lifecycle *Lifecycle) subscribeUninstalls(ownerSID string, token windows.Token) (*packageUninstallSubscription, error) {
	subscribe := lifecycle.subscribe
	if subscribe == nil {
		subscribe = subscribePackageUninstalling
	}
	subscription, err := subscribe(lifecycle.packageName, ownerSID, &lifecycle.uninstalling, token)
	if err != nil {
		return nil, fmt.Errorf(
			"%w: %s",
			unavailable("subscribe to authenticated package uninstall notifications"),
			ErrorMessage(err),
		)
	}
	return subscription, nil
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
	// Restoring the owner migrates a saved junction, so it must serialize with
	// provisioning and uninstall (Rust acquire_sandbox_setup_lock).
	lock, err := windowssandbox.AcquireSandboxSetupLock(pipeSetupLockTimeoutMillis)
	if err != nil {
		return err
	}
	defer lock.Release()
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
	var pins []windows.Handle
	defer func() { closeHandles(pins) }()
	migratedHome := ""
	if err := withOwnerImpersonation(token, func() error {
		home, migrated, err := resolveRestoredHome(record.CodexHome, record.Runtime, &pins)
		if err != nil {
			return err
		}
		if migrated {
			migratedHome = home
		}
		return nil
	}); err != nil {
		_ = token.Close()
		return err
	}
	if migratedHome != "" {
		record.CodexHome = migratedHome
		if record.DesktopInstallation != nil {
			// A historical junction cannot authorize deleting its target on uninstall.
			record.DesktopInstallation.CreatedCodexHome = false
		}
		if err := windowssandbox.SaveInstallation(record); err != nil {
			_ = token.Close()
			return err
		}
	}
	lock.Release()
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

// guardPresent reports whether the home's own directory guard is still
// retained; only then may cleanup act on owner-scoped paths.
func (installation *lifecycleInstallation) guardPresent() bool {
	return installation != nil && installation.guard != 0
}

// releaseGuard drops the home's directory guard and its own pin while keeping
// the ancestors pinned (Rust installation.directory_guard.take() followed by
// directory_handles.pop()).
func (installation *lifecycleInstallation) releaseGuard() {
	if !installation.guardPresent() {
		return
	}
	_ = windows.CloseHandle(installation.guard)
	installation.guard = 0
	if last := len(installation.handles) - 1; last >= 0 {
		_ = windows.CloseHandle(installation.handles[last])
		installation.handles = installation.handles[:last]
	}
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
	// Drop the uninstall registration before the pinned directories so the
	// catalog cannot deliver an event against handles that are already closed
	// (Rust PackageLifecycle::drop).
	lifecycle.installation.subscription.Close()
	closeHandles(lifecycle.installation.handles)
	if lifecycle.installation.guard != 0 {
		_ = windows.CloseHandle(lifecycle.installation.guard)
	}
	if lifecycle.installation.token != 0 {
		_ = lifecycle.installation.token.Close()
	}
	lifecycle.installation = nil
}

// pinOwnerHome pins every ancestor up to and including home as the owner, then
// guards the home itself so it cannot be converted into a reparse point before
// cleanup (Rust register_authenticated_user).
func pinOwnerHome(home string, token windows.Token) (string, []windows.Handle, windows.Handle, error) {
	var handles []windows.Handle
	var guard windows.Handle
	err := withOwnerImpersonation(token, func() error {
		if err := pinExistingAncestors(home, &handles); err != nil {
			return err
		}
		if len(handles) == 0 {
			return errors.New("pin the registered home: no directory was pinned")
		}
		created, err := windowssandbox.CreateDirectoryGuard(handles[len(handles)-1])
		if err != nil {
			return err
		}
		// Reject a conversion that happened before the handle-relative guard existed.
		probe, err := pinDirectory(home, windows.FILE_READ_ATTRIBUTES, windowssandbox.DirectoryOpenExisting)
		if err != nil {
			_ = windows.CloseHandle(created)
			return err
		}
		_ = windows.CloseHandle(probe)
		guard = created
		return nil
	})
	if err != nil {
		return "", handles, 0, err
	}
	return home, handles, guard, nil
}
