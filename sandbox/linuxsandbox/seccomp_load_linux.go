package linuxsandbox

import (
	"fmt"
	"runtime"
	"unsafe"

	seccomp "github.com/elastic/go-seccomp-bpf"
	"golang.org/x/net/bpf"
	"golang.org/x/sys/unix"
)

func loadNetworkSeccomp(groups []seccomp.SyscallGroup) error {
	instructions, err := compileNetworkSeccomp(groups, runtime.GOARCH)
	if err != nil {
		return err
	}
	raw, err := bpf.Assemble(instructions)
	if err != nil {
		return err
	}
	if len(raw) == 0 || len(raw) > 4096 {
		return fmt.Errorf("invalid seccomp program length %d", len(raw))
	}
	filters := make([]unix.SockFilter, len(raw))
	for i, instruction := range raw {
		filters[i] = unix.SockFilter{Code: instruction.Op, Jt: instruction.Jt, Jf: instruction.Jf, K: instruction.K}
	}
	program := unix.SockFprog{Len: uint16(len(filters)), Filter: &filters[0]}
	// no_new_privs belongs to the calling OS thread. Keep the prerequisite
	// and filter installation on that thread even if either syscall parks.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return err
	}
	result, _, errno := unix.Syscall(unix.SYS_SECCOMP, unix.SECCOMP_SET_MODE_FILTER, unix.SECCOMP_FILTER_FLAG_TSYNC, uintptr(unsafe.Pointer(&program)))
	runtime.KeepAlive(filters)
	if errno != 0 {
		return errno
	}
	// With TSYNC, a positive return value identifies an incompatible thread;
	// errno remains zero and no synchronized filter was installed.
	if result != 0 {
		return fmt.Errorf("seccomp thread synchronization failed for thread %d", result)
	}
	return nil
}
