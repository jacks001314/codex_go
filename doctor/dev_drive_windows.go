//go:build windows

package doctor

import (
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Rust 6efcdad4c3 (#38795): on Windows, report whether the active Git
// worktree is on a trusted Dev Drive and provide remediation when it is not.
const (
	devDriveVolumeFlag       = uint32(0x0000_2000)
	trustedVolumeFlag        = uint32(0x0000_4000)
	queryPersistentVolState  = uint32(0x0009_023c)
	windowsDevDriveMinBuild  = uint32(22_621)
	devDriveMaxVolumePathLen = 512
)

func windowsDevDriveCheck(cwd string) *DoctorCheck {
	return windowsDevDriveCheckFromInputs(cwd, windowsVolumeFlags)
}

func windowsDevDriveCheckFromInputs(cwd string, volumeFlags func(string) (uint32, bool)) *DoctorCheck {
	repoRoot := gitRepoRootForDoctor(cwd)
	if repoRoot == "" {
		return NewCheck("git.worktree.dev_drive", "git", CheckStatusOK, "no Git worktree is active")
	}
	if build := windowsBuildNumber(); build < windowsDevDriveMinBuild {
		return NewCheck("git.worktree.dev_drive", "git", CheckStatusOK, "Windows Dev Drives are unavailable on this Windows version")
	}
	flags, ok := volumeFlags(repoRoot)
	if !ok {
		return NewCheck("git.worktree.dev_drive", "git", CheckStatusWarning, "the active Git worktree's Windows Dev Drive state could not be inspected").
			Detail("filesystem error: unavailable")
	}
	switch {
	case flags&devDriveVolumeFlag == 0:
		return NewCheck("git.worktree.dev_drive", "git", CheckStatusWarning, "this worktree is not on a Windows Dev Drive; moving it to a trusted Dev Drive can significantly improve repository and filesystem performance").
			Remediate("create a trusted Windows Dev Drive for source repositories: https://learn.microsoft.com/en-us/windows/dev-drive/")
	case flags&trustedVolumeFlag == 0:
		return NewCheck("git.worktree.dev_drive", "git", CheckStatusWarning, "the active Git worktree is on an untrusted Windows Dev Drive").
			Remediate("ask your administrator to trust the Windows Dev Drive: https://learn.microsoft.com/en-us/windows/dev-drive/#how-do-i-designate-a-dev-drive-as-trusted")
	default:
		return NewCheck("git.worktree.dev_drive", "git", CheckStatusOK, "the active Git worktree is on a trusted Windows Dev Drive")
	}
}

// windowsBuildNumber returns the OS build number via RtlGetVersion.
func windowsBuildNumber() uint32 {
	info := windows.RtlGetVersion()
	if info == nil {
		return 0
	}
	return info.BuildNumber
}

type persistentVolumeState struct {
	Flags    uint32
	Mask     uint32
	Version  uint32
	Reserved uint32
}

// windowsVolumeFlags resolves the persistent volume state flags for the
// volume containing path (Rust volume_flags / GetVolumePathNameW +
// DeviceIoControl).
func windowsVolumeFlags(path string) (uint32, bool) {
	pathPointer, err := windows.UTF16PtrFromString(strings.TrimRight(path, `\/`))
	if err != nil {
		return 0, false
	}
	volumePath := make([]uint16, devDriveMaxVolumePathLen)
	if err := windows.GetVolumePathName(pathPointer, &volumePath[0], uint32(len(volumePath))); err != nil {
		return 0, false
	}
	root := windows.UTF16ToString(volumePath)
	if root == "" {
		return 0, false
	}
	rootPointer, err := windows.UTF16PtrFromString(root)
	if err != nil {
		return 0, false
	}
	handle, err := windows.CreateFile(
		rootPointer,
		windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS,
		0,
	)
	if err != nil {
		return 0, false
	}
	defer windows.CloseHandle(handle)
	state := persistentVolumeState{}
	var bytesReturned uint32
	if err := windows.DeviceIoControl(
		handle,
		queryPersistentVolState,
		nil,
		0,
		(*byte)(unsafe.Pointer(&state)),
		uint32(unsafe.Sizeof(state)),
		&bytesReturned,
		nil,
	); err != nil {
		return 0, false
	}
	if bytesReturned < uint32(unsafe.Sizeof(state)) {
		return 0, false
	}
	return state.Flags, true
}
