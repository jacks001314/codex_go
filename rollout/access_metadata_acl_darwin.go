//go:build darwin && cgo

package rollout

/*
#include <errno.h>
#include <sys/acl.h>
*/
import "C"

import (
	"errors"
	"fmt"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// copyPlatformRolloutACL replaces the destination's entire ACL with the
// source's ACL. COPYFILE_ACL merges inherited entries and drops inherited
// source entries, so it is unsuitable for preserving the exact ACL order and
// deny rules. A delete-deny ACL is rejected before publication because it can
// make both the staged file and a failed replacement impossible to remove.
func copyPlatformRolloutACL(sourcePath, destinationPath string) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	sourceFD, err := unix.Open(sourcePath, unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer unix.Close(sourceFD)
	destinationFD, err := unix.Open(destinationPath, unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer unix.Close(destinationFD)

	acl := C.acl_get_fd(C.int(sourceFD))
	sourceHasNoACL := acl == nil
	if sourceHasNoACL {
		if err := currentErrno(); err != syscall.ENOENT {
			return err
		}
		// No source ACL: clear any ACL inherited by the replacement.
		acl = C.acl_init(0)
		if acl == nil {
			return currentErrno()
		}
	}
	defer C.acl_free(unsafe.Pointer(acl))

	entryID := C.int(0)
	for {
		var entry unsafe.Pointer
		if C.acl_get_entry(acl, entryID, (*unsafe.Pointer)(unsafe.Pointer(&entry))) != 0 {
			err := currentErrno()
			// macOS reports EINVAL after the final ACL entry.
			if err == syscall.EINVAL {
				break
			}
			return err
		}
		var tag C.acl_tag_t
		var permissions C.acl_permset_mask_t
		if C.acl_get_tag_type(entry, &tag) != 0 ||
			C.acl_get_permset_mask_np(entry, &permissions) != 0 {
			return currentErrno()
		}
		if tag == C.ACL_EXTENDED_DENY && permissions&C.ACL_DELETE != 0 {
			return fmt.Errorf("%w: cannot replace a rollout with a delete-deny ACL", syscall.EPERM)
		}
		entryID++
	}

	if C.acl_set_fd(C.int(destinationFD), acl) == 0 {
		return nil
	}
	err = currentErrno()
	// Filesystems without ACL support reject even an empty ACL. There is
	// nothing to preserve only when the source had no ACL.
	if sourceHasNoACL && (errors.Is(err, syscall.ENOTSUP) || err == syscall.ENOTSUP) {
		return nil
	}
	return err
}

func currentErrno() error {
	return syscall.Errno(C.errno)
}
