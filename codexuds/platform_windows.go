//go:build windows

package codexuds

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows app-server socket protection (Rust codex-uds:
// windows_security.rs, windows_socket_validation.rs, windows_peer.rs).
//
// The rendezvous directory is created with a protected, inheritable DACL that
// grants the current user full access and nobody else anything. An existing
// directory is only accepted when it already satisfies that contract, because
// changing a DACL cannot revoke handles another user already holds. Before a
// client sends data it also requires the peer to be the same, non-elevated
// user, so an implicitly discovered socket can never be another account's
// process.

const (
	// socketDirectorySddlTemplate grants the user full access with object and
	// container inheritance behind a protected DACL (Rust's
	// "O:{sid}D:P(A;OICI;FA;;;{sid})").
	socketDirectorySddlTemplate = "O:%sD:P(A;OICI;FA;;;%s)"
	// sioAFUnixGetPeerPid is SIO_AF_UNIX_GETPEERPID, the kernel-reported peer
	// process of a connected AF_UNIX socket.
	//
	// The control code is `_WSAIOR(IOC_VENDOR, 256)` (afunix.h), i.e.
	// IOC_OUT|IOC_VENDOR|0x100. The function code is what selects the operation,
	// so a value without it is rejected with WSAEOPNOTSUPP.
	sioAFUnixGetPeerPid = 0x58000100
	// fileAllAccess is FILE_ALL_ACCESS, the single ACE mask the daemon grants.
	fileAllAccess = 0x1F01FF
	// maxSocketPathBytes is the AF_UNIX sun_path capacity. Winsock expects the
	// Win32 path encoded as UTF-8, and a longer path cannot be addressed.
	maxSocketPathBytes = 108
)

// checkSocketPath rejects a path Winsock cannot encode into sun_path (Rust
// uds_windows::sockaddr_un).
func checkSocketPath(socketPath string) error {
	if strings.ContainsRune(socketPath, 0) {
		return errors.New("socket path may not contain interior null bytes")
	}
	if len(socketPath) >= maxSocketPathBytes {
		return errors.New("socket path must be shorter than SUN_LEN")
	}
	return nil
}

// preparePrivateSocketDirectory creates socketDir with a protected, user-only
// DACL, or accepts an existing directory that already meets that contract.
func preparePrivateSocketDirectory(socketDir string) error {
	sid, err := currentUserSIDString()
	if err != nil {
		return err
	}
	descriptor, err := windows.SecurityDescriptorFromString(fmt.Sprintf(socketDirectorySddlTemplate, sid, sid))
	if err != nil {
		return fmt.Errorf("failed to build the private socket directory descriptor: %w", err)
	}
	path, err := absolutePrivatePath(socketDir, true)
	if err != nil {
		return err
	}
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	attributes := &windows.SecurityAttributes{
		Length:             uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		SecurityDescriptor: descriptor,
	}
	if err := windows.CreateDirectory(pointer, attributes); err != nil {
		if !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
			return fmt.Errorf("failed to create the private socket directory %s: %w", socketDir, err)
		}
		if _, guard, validateErr := validatePrivateDirectory(path); validateErr == nil {
			closeGuard(guard)
			return nil
		}
		// An earlier build of this CLI created the directory with the default
		// DACL inside the user's own CODEX_HOME. Rust rejects such a directory
		// because another account could already hold handles to it; inside a
		// user-owned profile only this user (and administrators) can reach it, so
		// the private contract is applied instead of breaking the installation.
		if err := repairPrivateDirectory(path, descriptor); err != nil {
			return err
		}
		_, guard, validateErr := validatePrivateDirectory(path)
		closeGuard(guard)
		return validateErr
	}
	return nil
}

// repairPrivateDirectory applies the user-only DACL to an existing directory
// that this user owns, leaving anything else unsafe untouched.
func repairPrivateDirectory(path string, descriptor *windows.SECURITY_DESCRIPTOR) error {
	current, err := currentUserSID()
	if err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("failed to inspect the socket directory %s: %w", path, err)
	}
	if !info.IsDir() || info.Mode()&fs.ModeSymlink != 0 {
		return errors.New("untrusted socket directory")
	}
	owner, err := directoryOwner(path)
	if err != nil {
		return err
	}
	if owner == nil || !windows.EqualSid(owner, current) {
		return errors.New("socket directory is not private to the current user")
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return err
	}
	if err := windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		dacl,
		nil,
	); err != nil {
		return fmt.Errorf("failed to secure the socket directory %s: %w", path, err)
	}
	return nil
}

// directoryOwner reads the owner SID of a named directory.
func directoryOwner(path string) (*windows.SID, error) {
	descriptor, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return nil, err
	}
	if descriptor == nil {
		return nil, nil
	}
	owner, _, err := descriptor.Owner()
	if err != nil {
		return nil, err
	}
	return owner, nil
}

// validatePrivateSocketPath validates the directory that holds socketPath
// without creating or changing it, and returns the validated path plus a guard
// that pins the directory for as long as the caller keeps it open.
func validatePrivateSocketPath(socketPath string) (string, *os.File, error) {
	if err := checkSocketPath(socketPath); err != nil {
		return "", nil, err
	}
	absolute, err := filepath.Abs(strings.TrimSpace(socketPath))
	if err != nil {
		return "", nil, err
	}
	if strings.TrimSpace(filepath.Base(absolute)) == "" || filepath.Dir(absolute) == absolute {
		return "", nil, errors.New("socket path must name a file inside a directory")
	}
	directory, guard, err := validatePrivateDirectory(filepath.Dir(absolute))
	if err != nil {
		return "", nil, err
	}
	return filepath.Join(directory, filepath.Base(absolute)), guard, nil
}

// validatePrivateDirectory opens directory as a pinned handle and requires the
// exact protected, inheritable, user-only contract.
func validatePrivateDirectory(directory string) (string, *os.File, error) {
	path, err := absolutePrivatePath(directory, false)
	if err != nil {
		return "", nil, err
	}
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", nil, err
	}
	handle, err := windows.CreateFile(
		pointer,
		windows.READ_CONTROL|windows.FILE_READ_ATTRIBUTES|windows.FILE_LIST_DIRECTORY,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		return path, nil, fmt.Errorf("failed to open the socket directory %s: %w", path, err)
	}
	// The guard deliberately omits FILE_SHARE_DELETE so the validated directory
	// cannot be replaced while a connection is being set up.
	guard := os.NewFile(uintptr(handle), path)
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		closeGuard(guard)
		return path, nil, err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 ||
		info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		closeGuard(guard)
		return path, nil, errors.New("untrusted socket directory")
	}
	trusted, err := directoryIsPrivateToCurrentUser(handle)
	if err != nil {
		closeGuard(guard)
		return path, nil, err
	}
	if !trusted {
		closeGuard(guard)
		return path, nil, errors.New("socket directory is not private to the current user")
	}
	return path, guard, nil
}

// directoryIsPrivateToCurrentUser accepts exactly the protected, inheritable
// user-only ACL the daemon sets at bind time (Rust
// windows_socket_validation::validate_private_directory).
func directoryIsPrivateToCurrentUser(handle windows.Handle) (bool, error) {
	descriptor, err := windows.GetSecurityInfo(
		handle,
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		return false, err
	}
	current, err := currentUserSID()
	if err != nil {
		return false, err
	}
	owner, _, err := descriptor.Owner()
	if err != nil {
		return false, err
	}
	if owner == nil || !windows.EqualSid(owner, current) {
		return false, nil
	}
	control, _, err := descriptor.Control()
	if err != nil {
		return false, err
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return false, err
	}
	if control&windows.SE_DACL_PROTECTED == 0 || dacl == nil || dacl.AceCount != 1 {
		return false, nil
	}
	var ace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(dacl, 0, &ace); err != nil {
		return false, err
	}
	if ace == nil || ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
		return false, nil
	}
	if ace.Header.AceFlags != windows.CONTAINER_INHERIT_ACE|windows.OBJECT_INHERIT_ACE {
		return false, nil
	}
	if ace.Mask != fileAllAccess {
		return false, nil
	}
	return windows.EqualSid((*windows.SID)(unsafe.Pointer(&ace.SidStart)), current), nil
}

// ensureNonElevatedPeer requires the socket peer to belong to the current user
// with neither process elevated. Call it before sending application data.
func ensureNonElevatedPeer(conn net.Conn) error {
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		return errors.New("peer authentication requires a unix socket connection")
	}
	raw, err := unixConn.SyscallConn()
	if err != nil {
		return err
	}
	var peerPID uint32
	var returned uint32
	var ioctlErr error
	if err := raw.Control(func(fd uintptr) {
		ioctlErr = windows.WSAIoctl(
			windows.Handle(fd),
			sioAFUnixGetPeerPid,
			nil,
			0,
			(*byte)(unsafe.Pointer(&peerPID)),
			uint32(unsafe.Sizeof(peerPID)),
			&returned,
			nil,
			0,
		)
	}); err != nil {
		return err
	}
	if ioctlErr != nil {
		return fmt.Errorf("%w: %v", ErrPeerIdentityUnavailable, ioctlErr)
	}
	if peerPID == 0 {
		return ErrPeerIdentityUnavailable
	}
	return requireNonElevatedSameUser(peerPID)
}

// requireNonElevatedSameUser compares the peer process's token with this
// process's own (Rust uds::windows_peer::ensure_non_elevated_peer).
func requireNonElevatedSameUser(peerPID uint32) error {
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, peerPID)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(process)
	var peerToken windows.Token
	if err := windows.OpenProcessToken(process, windows.TOKEN_QUERY, &peerToken); err != nil {
		return err
	}
	defer peerToken.Close()
	currentToken := windows.GetCurrentProcessToken()
	currentUser, err := currentToken.GetTokenUser()
	if err != nil {
		return err
	}
	peerUser, err := peerToken.GetTokenUser()
	if err != nil {
		return err
	}
	sameUser := windows.EqualSid(currentUser.User.Sid, peerUser.User.Sid)
	for _, token := range []windows.Token{currentToken, peerToken} {
		if !sameUser || token.IsElevated() {
			return ErrPeerNotAllowed
		}
	}
	return nil
}

func currentUserSID() (*windows.SID, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	if user == nil || user.User.Sid == nil {
		return nil, errors.New("current user token has no SID")
	}
	return user.User.Sid, nil
}

func currentUserSIDString() (string, error) {
	sid, err := currentUserSID()
	if err != nil {
		return "", err
	}
	return sid.String(), nil
}

// absolutePrivatePath resolves the parent of path and joins the leaf back, so
// the directory being secured is never resolved through its own reparse point
// (Rust windows_security::prepare_private_directory).
func absolutePrivatePath(path string, createParent bool) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	parent := filepath.Dir(absolute)
	name := filepath.Base(absolute)
	if parent == absolute || name == "" || name == "." || name == string(filepath.Separator) {
		return "", errors.New("private directory must not be a filesystem root")
	}
	if createParent {
		if err := os.MkdirAll(parent, 0o700); err != nil {
			return "", err
		}
	}
	resolved, err := resolveFinalPath(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(resolved, name), nil
}

// resolveFinalPath follows every link on the way to path, including a Windows
// mount-point reparse point, which filepath.EvalSymlinks does not.
func resolveFinalPath(path string) (string, error) {
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	handle, err := windows.CreateFile(
		pointer,
		windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS,
		0,
	)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(handle)
	size := uint32(len(path) + 64)
	for {
		buffer := make([]uint16, size)
		written, err := windows.GetFinalPathNameByHandle(handle, &buffer[0], size, 0)
		if err != nil {
			return "", err
		}
		if written < size {
			return strings.TrimPrefix(windows.UTF16ToString(buffer[:written]), `\\?\`), nil
		}
		size = written + 1
	}
}

func pathExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func closeGuard(guard *os.File) {
	if guard != nil {
		_ = guard.Close()
	}
}

// connectUnixSocket dials socketPath directly: Windows encodes the path into
// the AF_UNIX sun_path field and rejects an over-long path before connecting
// (Rust uds::windows platform has no symlink retry).
func connectUnixSocket(ctx context.Context, socketPath string) (net.Conn, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var dialer net.Dialer
	return dialer.DialContext(ctx, "unix", strings.TrimSpace(socketPath))
}

// prepareControlSocketPath refuses to take over a socket that already answers
// and removes only a path that is genuinely stale (Rust
// app-server-transport::prepare_control_socket_path).
func prepareControlSocketPath(socketPath string) error {
	if err := checkSocketPath(socketPath); err != nil {
		return err
	}
	conn, err := net.Dial("unix", socketPath)
	if err == nil {
		_ = conn.Close()
		return fmt.Errorf("app-server control socket is already in use at %s", socketPath)
	}
	if !pathExists(socketPath) {
		return nil
	}
	// Windows represents the rendezvous as a regular path, so existence is the
	// only stale-path signal available (Rust is_stale_socket_path).
	if err := os.Remove(socketPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove the stale app-server control socket %s: %w", socketPath, err)
	}
	return nil
}
