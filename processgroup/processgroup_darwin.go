//go:build darwin

package processgroup

import (
	"errors"
	"sync"
	"syscall"
	"unsafe"

	"github.com/ebitengine/purego"
)

// Terminate sends SIGTERM to the process group. A denied group signal is
// retried against the group's individual members (Rust #46521), so it reports
// true when the group or at least one member accepted the signal.
func Terminate(pgid int) (bool, error) {
	return darwinSignals().send(pgid, syscall.SIGTERM)
}

// Kill sends SIGKILL to the process group with the same member fallback
// (Rust #46521).
func Kill(pgid int) error {
	_, err := darwinSignals().send(pgid, syscall.SIGKILL)
	return err
}

func darwinSignals() fallbackSignals {
	return fallbackSignals{
		group:   signalGroup,
		members: listProcessGroupMembers,
		groupOf: syscall.Getpgid,
		process: signalProcess,
	}
}

func signalGroup(pgid int, sig syscall.Signal) (bool, error) {
	if pgid <= 0 {
		return false, ErrInvalidProcessGroupID
	}
	err := syscall.Kill(-pgid, sig)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, syscall.ESRCH) {
		return false, nil
	}
	return false, err
}

func signalProcess(pid int, sig syscall.Signal) (bool, error) {
	if pid <= 0 {
		return false, ErrInvalidProcessGroupID
	}
	err := syscall.Kill(pid, sig)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, syscall.ESRCH) {
		return false, nil
	}
	return false, err
}

// listPgrpPids is libproc's proc_listpgrppids, the same call Rust's fallback
// uses. It resolves once; a missing symbol leaves it nil so the caller reports
// an error instead of silently dropping the signal.
var listPgrpPids = sync.OnceValue(func() func(pgid int32, buffer unsafe.Pointer, bufferSize int32) int32 {
	handle, err := purego.Dlopen("/usr/lib/libproc.dylib", purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		return nil
	}
	var list func(pgid int32, buffer unsafe.Pointer, bufferSize int32) int32
	purego.RegisterLibFunc(&list, handle, "proc_listpgrppids")
	return list
})

// listProcessGroupMembers mirrors Rust's expanding pid buffer.
func listProcessGroupMembers(pgid int) ([]int, error) {
	list := listPgrpPids()
	if list == nil {
		return nil, errors.New("libproc proc_listpgrppids is unavailable")
	}
	pids := make([]int32, 16)
	for {
		count := list(int32(pgid), unsafe.Pointer(&pids[0]), int32(len(pids))*int32(unsafe.Sizeof(pids[0])))
		if count < 0 {
			return nil, errors.New("proc_listpgrppids failed")
		}
		if int(count) < len(pids) {
			pids = pids[:count]
			break
		}
		expanded := make([]int32, len(pids)*2)
		copy(expanded, pids)
		pids = expanded
	}
	members := make([]int, 0, len(pids))
	for _, pid := range pids {
		members = append(members, int(pid))
	}
	return members, nil
}
