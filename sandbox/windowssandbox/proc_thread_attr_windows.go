//go:build windows

package windowssandbox

import (
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Desktop app context attributes (winbase.h). The policy keeps the initial
// child and its descendants inside the caller's package environment so they can
// launch executables from its protected directory.
const (
	procThreadAttributeDesktopAppPolicy                  = 0x00020012
	processCreationDesktopAppBreakawayDisableProcessTree = 2
	processCreationDesktopAppBreakawayOverride           = 4
)

// procThreadAttributeJobList carries one job handle so the child joins that job
// during creation (winbase.h PROC_THREAD_ATTRIBUTE_JOB_LIST). x/sys/windows
// exposes the pseudoconsole and handle-list attributes but not this one.
const procThreadAttributeJobList = 0x0002000D

func NewProcThreadAttributeList() (*ProcThreadAttributeList, error) {
	return NewProcThreadAttributeListWithCount(1)
}

func NewProcThreadAttributeListWithCount(attrCount uint32) (*ProcThreadAttributeList, error) {
	if attrCount == 0 {
		return nil, ErrInvalidRequest
	}
	impl, err := windows.NewProcThreadAttributeList(attrCount)
	if err != nil {
		return nil, err
	}
	return &ProcThreadAttributeList{impl: impl}, nil
}

func (l *ProcThreadAttributeList) SetHandleList(handles []uintptr) error {
	if l == nil || l.impl == nil {
		return ErrInvalidRequest
	}
	if len(handles) == 0 {
		return ErrInvalidRequest
	}
	l.handleList = append(l.handleList[:0], handles...)
	return l.container().Update(
		windows.PROC_THREAD_ATTRIBUTE_HANDLE_LIST,
		unsafe.Pointer(&l.handleList[0]),
		uintptr(len(l.handleList))*unsafe.Sizeof(l.handleList[0]),
	)
}

func (l *ProcThreadAttributeList) SetPseudoconsole(handle uintptr) error {
	if l == nil || l.impl == nil || handle == 0 {
		return ErrInvalidRequest
	}
	// PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE carries the HPCON value itself, not a
	// pointer to it. Both Rust ports spell the attribute that way
	// (codex-rs/windows-sandbox-rs/src/proc_thread_attr.rs `set_pseudoconsole`:
	// `hpc as *mut c_void`, and codex-rs/utils/pty/src/win/procthreadattr.rs
	// `set_pty`), and the Microsoft ConPTY sample passes hPC directly.
	// Passing &hPC instead leaves the console unattached: CreateProcess still
	// succeeds and the child runs, but nothing ever reaches the pseudoconsole's
	// output pipe. The three-state matrix in
	// sandbox/windowssandbox/conpty/conpty_raw_probe_windows_test.go records
	// the difference (plan §17).
	err := l.container().Update(
		windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE,
		handleBitsAsPointer(handle),
		unsafe.Sizeof(handle),
	)
	runtime.KeepAlive(l)
	return err
}

// SetJob attaches the child to a job object atomically at creation, mirroring
// Rust's `ProcThreadAttributeList::set_job`
// (codex-rs/utils/pty/src/win/procthreadattr.rs). The ConPTY launcher needs it:
// the job must contain the console child from its first instruction, because a
// window between CreateProcess and AssignProcessToJobObject would let the child
// start its own tree outside the kill-on-close job.
func (l *ProcThreadAttributeList) SetJob(handle uintptr) error {
	if l == nil || l.impl == nil || handle == 0 {
		return ErrInvalidRequest
	}
	l.jobList = append(l.jobList[:0], handle)
	err := l.container().Update(
		procThreadAttributeJobList,
		unsafe.Pointer(&l.jobList[0]),
		uintptr(len(l.jobList))*unsafe.Sizeof(l.jobList[0]),
	)
	runtime.KeepAlive(l)
	return err
}

// handleBitsAsPointer reinterprets a Windows handle or HPCON value as the
// opaque pointer Win32 expects for the pseudoconsole attribute, without a
// uintptr-to-unsafe.Pointer conversion that go vet's unsafeptr check rejects.
func handleBitsAsPointer(handle uintptr) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&handle))
}

// PreserveDesktopAppContext ports Rust's
// `ProcThreadAttributeList::preserve_desktop_app_context` (#46575): sandboxed
// descendants keep the caller's desktop app context so packaged processes can
// still launch children from their protected package directory.
func (l *ProcThreadAttributeList) PreserveDesktopAppContext() error {
	if l == nil || l.impl == nil {
		return ErrInvalidRequest
	}
	l.desktopAppPolicy = processCreationDesktopAppBreakawayDisableProcessTree |
		processCreationDesktopAppBreakawayOverride
	err := l.container().Update(
		procThreadAttributeDesktopAppPolicy,
		unsafe.Pointer(&l.desktopAppPolicy),
		unsafe.Sizeof(l.desktopAppPolicy),
	)
	runtime.KeepAlive(l)
	return err
}

func (l *ProcThreadAttributeList) WindowsList() *windows.ProcThreadAttributeList {
	if l == nil || l.impl == nil {
		return nil
	}
	return l.container().List()
}

func (l *ProcThreadAttributeList) Close() {
	if l == nil || l.impl == nil {
		return
	}
	l.container().Delete()
	l.impl = nil
	l.handleList = nil
	l.jobList = nil
}

func (l *ProcThreadAttributeList) container() *windows.ProcThreadAttributeListContainer {
	return l.impl.(*windows.ProcThreadAttributeListContainer)
}
