//go:build linux

package linuxsandbox

import (
	"os"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// linuxSandboxExitCode runs only at the helper process boundary, after owned
// resources have been closed. Re-raising preserves WIFSIGNALED, including
// SIGQUIT, for which os/signal.Reset alone leaves Go's crash handler installed.
func linuxSandboxExitCode(status syscall.WaitStatus) int {
	if !status.Signaled() {
		return status.ExitStatus()
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	sig := status.Signal()
	maskBytes := uintptr(8)
	if runtime.GOARCH == "mips" || runtime.GOARCH == "mipsle" || runtime.GOARCH == "mips64" || runtime.GOARCH == "mips64le" {
		maskBytes = 16
	}
	// A zero kernel sigaction is SIG_DFL on every Linux architecture, regardless
	// of the architecture-specific ordering of its handler, flags and mask.
	var action [128]byte
	_, _, errno := unix.RawSyscall6(unix.SYS_RT_SIGACTION, uintptr(sig), uintptr(unsafe.Pointer(&action[0])), 0, maskBytes, 0, 0)
	if errno == 0 || sig == syscall.SIGKILL {
		// This process is terminating. An empty mask also avoids native-word
		// endianness differences in sigsets on 32-bit Linux architectures.
		_, _, errno = unix.RawSyscall6(unix.SYS_RT_SIGPROCMASK, unix.SIG_SETMASK, uintptr(unsafe.Pointer(&action[0])), 0, maskBytes, 0, 0)
		if errno == 0 {
			_ = unix.Tgkill(os.Getpid(), unix.Gettid(), sig)
		}
	}
	runtime.KeepAlive(action)
	return 128 + int(sig)
}
